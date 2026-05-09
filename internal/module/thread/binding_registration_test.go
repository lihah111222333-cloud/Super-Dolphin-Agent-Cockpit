package thread

import (
	"context"
	"errors"
	"strings"
	"testing"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestPersistThreadStateProviderThreadImmutability(t *testing.T) {
	t.Parallel()

	t.Run("empty to filled is allowed", func(t *testing.T) {
		threads := &stubThreadStore{}
		bindings := &stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "", // empty — not yet set
			CodexThreadID:    "thread-1",
			Cwd:              "/repo",
		}}
		svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

		err := svc.persistThreadState(context.Background(), threadState{
			PublicThreadID:   "thread-1",
			ProviderThreadID: "real-uuid-123",
			AgentID:          "agent-1",
			Provider:         "codex",
			CWD:              "/repo",
			CreatedAt:        123,
		}, true)
		if err != nil {
			t.Fatalf("persistThreadState() error = %v, want nil (empty→fill allowed)", err)
		}
		if bindings.upsert.ProviderThreadID != "real-uuid-123" {
			t.Fatalf("binding upsert provider_thread_id = %q, want real-uuid-123", bindings.upsert.ProviderThreadID)
		}
	})

	t.Run("non-empty change is rejected", func(t *testing.T) {
		threads := &stubThreadStore{}
		bindings := &stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "provider-thread-1", // already set
			CodexThreadID:    "thread-1",
			Cwd:              "/repo",
		}}
		svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

		err := svc.persistThreadState(context.Background(), threadState{
			PublicThreadID:   "thread-1",
			ProviderThreadID: "provider-thread-2", // trying to change
			AgentID:          "agent-1",
			Provider:         "codex",
			CWD:              "/repo",
			CreatedAt:        123,
		}, true)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("persistThreadState() error = %v, want immutable rejection", err)
		}
	})
}

func TestPersistThreadStateNormalizesClaudeProviderThreadID(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:   "agent-1",
		ProviderThreadID: "agent-1",
		AgentID:          "agent-1",
		Provider:         "claude",
		CWD:              "/repo",
		CreatedAt:        123,
	}, true)
	if err != nil {
		t.Fatalf("persistThreadState() error = %v", err)
	}
	if bindings.upsert.ProviderThreadID != "" {
		t.Fatalf("binding upsert provider_thread_id = %q, want empty", bindings.upsert.ProviderThreadID)
	}
}

func TestPersistThreadStateKeepsCodexProviderThreadIDCompatible(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:   "agent-1",
		ProviderThreadID: "provider-thread-codex",
		AgentID:          "agent-1",
		Provider:         "codex",
		CWD:              "/repo",
		CreatedAt:        123,
	}, true)
	if err != nil {
		t.Fatalf("persistThreadState() error = %v", err)
	}
	if bindings.upsert.ProviderThreadID != "provider-thread-codex" {
		t.Fatalf("binding upsert provider_thread_id = %q, want provider-thread-codex", bindings.upsert.ProviderThreadID)
	}
}

func TestBindingRecoveryReporterRecordsProviderSessionUUID(t *testing.T) {
	t.Parallel()

	const sessionUUID = "11111111-2222-3333-4444-555555555555"
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "agent-1",
	}}
	reporter := NewBindingRecoveryReporter(bindings, silentLogger())

	if err := reporter.RecordProviderSessionUUID(context.Background(), "agent-1", sessionUUID); err != nil {
		t.Fatalf("RecordProviderSessionUUID() error = %v", err)
	}
	if len(bindings.sessionUpdates) != 1 {
		t.Fatalf("session updates = %d, want 1", len(bindings.sessionUpdates))
	}
	if got := bindings.sessionUpdates[0]; got.AgentID != "agent-1" || got.SessionUUID != sessionUUID {
		t.Fatalf("session update = %+v, want agent/session uuid", got)
	}
	if bindings.updateProviderThreadID.ProviderThreadID != sessionUUID {
		t.Fatalf("provider_thread_id update = %q, want %s", bindings.updateProviderThreadID.ProviderThreadID, sessionUUID)
	}
}

func TestBindingRecoveryReporterSkipsInvalidSessionUUID(t *testing.T) {
	t.Parallel()

	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "claude",
		ProviderThreadID: "agent-1",
	}}
	reporter := NewBindingRecoveryReporter(bindings, silentLogger())

	if err := reporter.RecordProviderSessionUUID(context.Background(), "agent-1", "agent-1"); err != nil {
		t.Fatalf("RecordProviderSessionUUID() error = %v", err)
	}
	if len(bindings.sessionUpdates) != 0 {
		t.Fatalf("session updates = %d, want none", len(bindings.sessionUpdates))
	}
	if bindings.updateProviderThreadID.ProviderThreadID != "" {
		t.Fatalf("provider_thread_id update = %q, want none", bindings.updateProviderThreadID.ProviderThreadID)
	}
}

func TestPersistThreadStateRejectsPublicThreadCollision(t *testing.T) {

	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID: "thread-1",
		AgentID:  "agent-other",
	}}
	bindings := &stubBindingStore{}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:   "thread-1",
		ProviderThreadID: "provider-thread-1",
		AgentID:          "agent-1",
		Provider:         "codex",
		CWD:              "/repo",
		CreatedAt:        123,
	}, true)
	if err == nil || !strings.Contains(err.Error(), `public thread id "thread-1"`) {
		t.Fatalf("persistThreadState() error = %v, want public id collision", err)
	}
	if bindings.upsert.AgentID != "" {
		t.Fatalf("binding upsert = %#v, want none", bindings.upsert)
	}
}

func TestPersistThreadStateRejectsOrphanPublicThreadCollision(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID: "thread-1",
	}}
	bindings := &stubBindingStore{}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:   "thread-1",
		ProviderThreadID: "provider-thread-1",
		AgentID:          "agent-1",
		Provider:         "codex",
		CWD:              "/repo",
		CreatedAt:        123,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "without a binding owner") {
		t.Fatalf("persistThreadState() error = %v, want orphan public id collision", err)
	}
}

func TestPersistThreadStateRollsBackInsertedBindingOnThreadUpsertFailure(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{upsertErr: errors.New("thread upsert failed")}
	bindings := &stubBindingStore{}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:   "thread-1",
		ProviderThreadID: "provider-thread-1",
		AgentID:          "agent-1",
		Provider:         "codex",
		CWD:              "/repo",
		CreatedAt:        123,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "thread upsert failed") {
		t.Fatalf("persistThreadState() error = %v, want thread upsert failure", err)
	}
	if len(bindings.deleteAgentIDs) != 1 || bindings.deleteAgentIDs[0] != "agent-1" {
		t.Fatalf("binding rollback deletes = %#v, want [agent-1]", bindings.deleteAgentIDs)
	}
	if bindings.binding != nil {
		t.Fatalf("binding after rollback = %#v, want nil", bindings.binding)
	}
}

func TestPersistThreadStateRestoresPreviousBindingOnThreadUpsertFailure(t *testing.T) {
	t.Parallel()

	previous := &bindingstore.Binding{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "",
		Cwd:              "",
		CreatedAt:        99,
	}
	threads := &stubThreadStore{upsertErr: errors.New("thread upsert failed")}
	bindings := &stubBindingStore{binding: previous}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:   "thread-1",
		ProviderThreadID: "provider-thread-1",
		AgentID:          "agent-1",
		Provider:         "codex",
		CWD:              "/repo",
		CreatedAt:        123,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "thread upsert failed") {
		t.Fatalf("persistThreadState() error = %v, want thread upsert failure", err)
	}
	if got := bindings.binding; got == nil || got.CodexThreadID != "" || got.Cwd != "" || got.ProviderThreadID != "provider-thread-1" {
		t.Fatalf("binding after rollback = %#v, want restored previous binding", got)
	}
	if len(bindings.upserts) != 2 {
		t.Fatalf("binding upserts = %#v, want write+rollback", bindings.upserts)
	}
}

func TestBindingRegistrationPersistsCodexIdentity(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:     "thread-identity",
		ProviderThreadID:   "provider-thread-identity",
		AgentID:            "agent-identity",
		Provider:           "codex",
		CWD:                "/repo",
		CodexHome:          "/realpath/.codex-providers/glm",
		CodexInstanceKey:   "glm",
		CodexModelProvider: "openai-compatible-glm",
		CreatedAt:          123,
	}, true)
	if err != nil {
		t.Fatalf("persistThreadState() error = %v", err)
	}
	if bindings.upsert.CodexHome != "/realpath/.codex-providers/glm" ||
		bindings.upsert.CodexInstanceKey != "glm" ||
		bindings.upsert.CodexModelProvider != "openai-compatible-glm" {
		t.Fatalf("codex identity upsert = (%q,%q,%q)",
			bindings.upsert.CodexHome,
			bindings.upsert.CodexInstanceKey,
			bindings.upsert.CodexModelProvider)
	}
}
