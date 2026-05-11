package thread

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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

	// Agent A resumes. The binding for agent A has provider "codex" and
	// provider_thread_id = "conflict-uuid", but that UUID is ALSO bound
	// to agent B. This triggers ensureProviderThreadAvailable → error.

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-A",
		AgentID:   "agent-A",
		Prompt:    "DAG改造执行",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}

	// The conflicting binding store: agent-A's binding claims
	// provider_thread_id "conflict-uuid", but GetByProviderThread for
	// that UUID returns agent-B's binding.
	bindings := &conflictBindingStore{
		agentBinding: &bindingstore.Binding{
			AgentID:          "agent-A",
			Provider:         "codex",
			ProviderThreadID: "conflict-uuid",
			CodexThreadID:    "thread-A",
			Cwd:              "/repo",
		},
		conflictBinding: &bindingstore.Binding{
			AgentID:          "agent-B",
			Provider:         "codex",
			ProviderThreadID: "conflict-uuid",
			CodexThreadID:    "thread-B",
			Cwd:              "/repo",
		},
	}

	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "conflict-uuid"}
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

	result, err := svc.Resume(context.Background(), ResumeRequest{
		ThreadID: "thread-A",
	})
	// Resume should still return a result (the session was established),
	// even though the binding persist failed.
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.ThreadID != "thread-A" {
		t.Fatalf("ThreadID = %q, want thread-A", result.ThreadID)
	}

	// Key assertion: thread.Started MUST NOT be emitted when the persist
	// failure was a binding conflict. Before the fix, startedCount == 1.
	if count := startedCount.Load(); count != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 (binding conflict must suppress event)", count)
	}
}

// TestResumeNonConflictPersistFailureStillEmitsThreadStarted verifies that
// when persistThreadState fails for a NON-conflict reason (e.g. transient
// DB error), thread.Started IS still emitted as a fallback — preserving
// the pre-fix behavior for non-binding-conflict failures.
func TestResumeNonConflictPersistFailureStillEmitsThreadStarted(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{
		thread: &threadstore.Thread{
			ThreadID:  "thread-1",
			AgentID:   "agent-1",
			Prompt:    "test",
			Model:     "gpt-5.5",
			Cwd:       "/repo",
			CreatedAt: 123,
			Status:    statusCreated,
		},
		upsertErr: errTransientDB, // simulate transient DB failure
	}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "provider-thread-1"}
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

	result, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if result.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", result.ThreadID)
	}

	// For non-conflict errors, thread.Started SHOULD be emitted as fallback.
	if count := startedCount.Load(); count != 1 {
		t.Fatalf("thread.Started emitted %d times; want 1 (non-conflict should still emit)", count)
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
	agentBinding    *bindingstore.Binding
	conflictBinding *bindingstore.Binding
}

func (s *conflictBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s.agentBinding != nil && s.agentBinding.AgentID == agentID {
		b := *s.agentBinding
		return &b, nil
	}
	return s.stubBindingStore.GetByAgentID(context.Background(), agentID)
}

func (s *conflictBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*bindingstore.Binding, error) {
	if s.conflictBinding != nil &&
		s.conflictBinding.Provider == provider &&
		s.conflictBinding.ProviderThreadID == providerThreadID {
		b := *s.conflictBinding
		return &b, nil
	}
	return s.stubBindingStore.GetByProviderThread(context.Background(), provider, providerThreadID)
}

// errTransientDB is a sentinel error for simulating non-conflict DB failures.
var errTransientDB = errors.New("transient database error")
