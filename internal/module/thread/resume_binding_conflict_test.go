package thread

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

// TestResumeBindingConflictSuppressesThreadStartedEvent is a regression test
// for a production incident (2026-05-11) where a codex agent's resume emitted
// thread.Started with a provider_thread_id owned by a different agent. The
// frontend detected a provider_mismatch and tried to reload history using the
// wrong UUID, resulting in empty conversation history.
//
// Root cause: persistResumedSession unconditionally called
// publishThreadStarted even when persistThreadState failed due to a binding
// conflict (provider_thread_id already bound to another agent).
//
// This test verifies that when Resume succeeds at the session level but
// persistThreadState fails due to a binding conflict, thread.Started is
// NOT emitted — preventing the provider_mismatch cascade.
func TestResumeBindingConflictSuppressesThreadStartedEvent(t *testing.T) {
	t.Parallel()

	const conflictUUID = "11111111-2222-3333-4444-555555555571"
	rolloutPath := writeExistingProviderHistoryFile(t, conflictUUID)
	// Agent A resumes. The binding for agent A has provider "codex" and
	// provider_thread_id = conflictUUID, but that UUID is ALSO bound
	// to agent B. This triggers ensureProviderThreadAvailable → error.

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-A",
		AgentID:        "agent-A",
		Prompt:         "DAG改造执行",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}

	// The conflicting binding store: agent-A's binding claims
	// provider_thread_id conflictUUID, but GetByProviderThread for
	// that UUID returns agent-B's binding.
	bindings := &conflictBindingStore{
		agentBinding: &BindingRecord{
			AgentID:          "agent-A",
			Provider:         "codex",
			ProviderThreadID: conflictUUID,
			CodexThreadID:    "thread-A",
			RolloutPath:      rolloutPath,
			Cwd:              "/repo",
		},
		conflictBinding: &BindingRecord{
			AgentID:          "agent-B",
			Provider:         "codex",
			ProviderThreadID: conflictUUID,
			CodexThreadID:    "thread-B",
			Cwd:              "/repo",
		},
	}
	authorizeRecoveryTestBinding(bindings.agentBinding)

	// Agent B has an active session — this is not an orphan binding.
	agentBSession := &stubSession{threadID: conflictUUID, rolloutPath: rolloutPath}
	sessions := &stubSessionProvider{}
	sessions.sessions = map[string]contract.Session{"agent-B": agentBSession}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: conflictUUID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}

	// Wire emitStarted to a counter so we can detect spurious events.
	var startedCount atomic.Int32
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	svc.emitStarted = func(ev threaddto.Started) {
		startedCount.Add(1)
	}

	_, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-A",
	})
	// Resume should now return an error because the binding conflict
	// is with an active agent — the zombie session is killed.
	if err == nil {
		t.Fatal("Resume() should return error on active binding conflict")
	}
	if !isBindingConflictError(err) {
		t.Fatalf("Resume() error should be binding conflict, got: %v", err)
	}

	// Key assertion: thread.Started MUST NOT be emitted when the persist
	// failure was a binding conflict. Before the fix, startedCount == 1.
	if count := startedCount.Load(); count != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 (binding conflict must suppress event)", count)
	}
}

// TestResumeNonConflictPersistFailureFailsFast verifies that when
// persistThreadState fails for a non-conflict reason (e.g. transient DB
// error), Resume returns the error and does not emit a synthetic
// thread.Started event.
func TestResumeNonConflictPersistFailureFailsFast(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-2222-3333-4444-555555555572"
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID)
	threads := &stubThreadStore{
		thread: &ThreadRecord{
			ThreadID:       "thread-1",
			AgentID:        "agent-1",
			Prompt:         "test",
			Model:          "gpt-5.5",
			Cwd:            "/repo",
			CreatedAt:      123,
			Status:         statusCreated,
			ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
		},
		upsertErr: errTransientDB, // simulate transient DB failure
	}
	bindings := &stubBindingStore{binding: &BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-1",
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: providerThreadID, rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}
	var startedCount atomic.Int32
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	svc.emitStarted = func(ev threaddto.Started) {
		startedCount.Add(1)
	}

	if _, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"}); !errors.Is(err, errTransientDB) {
		t.Fatalf("Resume() error = %v, want %v", err, errTransientDB)
	}
	if count := startedCount.Load(); count != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0", count)
	}
}

// TestResumeEvictsStaleBindingWhenBlockingAgentIsDead verifies that when
// a provider_thread_id is claimed by a dead agent (no active session),
// the stale binding is evicted and resume succeeds.
func TestResumeEvictsStaleBindingWhenBlockingAgentIsDead(t *testing.T) {
	// This scenario starts from the persisted binding shape; legacy default-home
	// injection would create a different identity mismatch before stale eviction.
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")

	const conflictUUID = "11111111-2222-3333-4444-555555555573"
	rolloutPath := writeExistingProviderHistoryFile(t, conflictUUID)
	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-A",
		AgentID:        "agent-A",
		Prompt:         "eviction-test",
		Model:          "gpt-5.5",
		Cwd:            "/repo",
		CreatedAt:      123,
		Status:         statusCreated,
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}

	bindings := &conflictBindingStore{
		agentBinding: &BindingRecord{
			AgentID:          "agent-A",
			Provider:         "codex",
			ProviderThreadID: conflictUUID,
			CodexThreadID:    "thread-A",
			RolloutPath:      rolloutPath,
			Cwd:              "/repo",
		},
		conflictBinding: &BindingRecord{
			AgentID:          "agent-B",
			Provider:         "codex",
			ProviderThreadID: conflictUUID,
			CodexThreadID:    "thread-B",
			Cwd:              "/repo",
		},
	}
	authorizeRecoveryTestBinding(bindings.agentBinding)

	// Agent B has NO active session — it's a stale/orphan binding.
	// Use the sessions map so GetSession("agent-B") returns not-found
	// instead of falling through to the shared session field.
	sessions := &stubSessionProvider{sessions: make(map[string]contract.Session)}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: conflictUUID, rolloutPath: rolloutPath}
			sessions.sessions["agent-A"] = session
			return session, nil
		},
	}

	orch := &stubThreadOrchestration{}

	var startedCount atomic.Int32
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)
	svc.emitStarted = func(ev threaddto.Started) {
		startedCount.Add(1)
	}

	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-A",
	})
	// Resume should succeed because the stale binding is evicted.
	if err != nil {
		t.Fatalf("Resume() error = %v; want nil (stale binding should be evicted)", err)
	}
	if result.ThreadID != "thread-A" {
		t.Fatalf("ThreadID = %q, want thread-A", result.ThreadID)
	}
}

// -- Test-only stubs -------------------------------------------------------

// conflictBindingStore simulates the production scenario where agent A's
// binding has a provider_thread_id that is already claimed by agent B.
// GetByAgentID returns agentBinding, but GetByProviderThread returns
// conflictBinding (different agent) — causing ensureProviderThreadAvailable
// to reject the persist.
type conflictBindingStore struct {
	stubBindingStore
	agentBinding    *BindingRecord
	conflictBinding *BindingRecord
}

func (s *conflictBindingStore) GetByAgentID(_ context.Context, agentID string) (*BindingRecord, error) {
	if s.agentBinding != nil && s.agentBinding.AgentID == agentID {
		b := *s.agentBinding
		return &b, nil
	}
	return s.stubBindingStore.GetByAgentID(context.Background(), agentID)
}

func (s *conflictBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*BindingRecord, error) {
	if s.conflictBinding != nil &&
		s.conflictBinding.Provider == provider &&
		s.conflictBinding.ProviderThreadID == providerThreadID {
		b := *s.conflictBinding
		return &b, nil
	}
	return s.stubBindingStore.GetByProviderThread(context.Background(), provider, providerThreadID)
}

func (s *conflictBindingStore) UpdateProviderThreadID(_ context.Context, params BindingProviderThreadIDUpdate) error {
	// Simulate eviction: when the conflict binding's provider_thread_id is
	// cleared, GetByProviderThread should no longer return it.
	if s.conflictBinding != nil && s.conflictBinding.AgentID == params.AgentID {
		s.conflictBinding.ProviderThreadID = params.ProviderThreadID
		s.conflictBinding.UpdatedAt = params.UpdatedAt
	}
	if s.agentBinding != nil && s.agentBinding.AgentID == params.AgentID {
		s.agentBinding.ProviderThreadID = params.ProviderThreadID
		s.agentBinding.UpdatedAt = params.UpdatedAt
	}
	return s.stubBindingStore.UpdateProviderThreadID(context.Background(), params)
}

// errTransientDB is a sentinel error for simulating non-conflict DB failures.
var errTransientDB = errors.New("transient database error")
