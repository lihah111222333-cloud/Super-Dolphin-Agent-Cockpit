package thread

import (
	"context"
	"errors"
	"reflect"
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
		RolloutPath:      writeExistingProviderHistoryFile(t),
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

func TestBindingRecoveryReporterDoesNotPromoteProviderThreadIDWithoutHistoryFile(t *testing.T) {
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
	if bindings.updateProviderThreadID.ProviderThreadID != "" {
		t.Fatalf("provider_thread_id update = %q, want none without history file", bindings.updateProviderThreadID.ProviderThreadID)
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

func TestThreadBindingStoreAdapterPreservesBindingFields(t *testing.T) {
	t.Parallel()

	source := &bindingstore.Binding{
		AgentID:            "agent-adapter",
		Provider:           "codex",
		ProviderThreadID:   "provider-thread-adapter",
		CodexThreadID:      "thread-adapter",
		RolloutPath:        "/tmp/rollout.jsonl",
		Cwd:                "/repo",
		ParentAgentID:      "parent-agent",
		AgentType:          "worker",
		AgentMemoryScope:   "project",
		Archived:           true,
		CreatedAt:          101,
		UpdatedAt:          202,
		SessionUUID:        "019e2c35-42ef-75b3-9f73-31cf7cc4cf2e",
		CodexHome:          "/Users/dev/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}
	bindings := &stubBindingStore{binding: source}
	adapter := newThreadBindingStorePort(bindings)

	got, err := adapter.GetByAgentID(context.Background(), "agent-adapter")
	if err != nil {
		t.Fatalf("GetByAgentID() error = %v", err)
	}
	assertThreadBindingRecord(t, got, *source)

	err = adapter.Upsert(context.Background(), newBindingUpsertParams(threadBindingRecord{
		AgentID:            " agent-next ",
		Provider:           " codex ",
		ProviderThreadID:   " provider-next ",
		CodexThreadID:      " thread-next ",
		RolloutPath:        " /tmp/next.jsonl ",
		SessionUUID:        " 019e2c35-42ef-75b3-9f73-31cf7cc4cf2f ",
		Cwd:                " /repo/next ",
		ParentAgentID:      " parent-next ",
		AgentType:          " worker ",
		AgentMemoryScope:   " project ",
		CreatedAt:          303,
		UpdatedAt:          404,
		CodexHome:          " /Users/dev/.codex-next ",
		CodexInstanceKey:   " next ",
		CodexModelProvider: " openai-compatible ",
	}))
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	assertThreadBindingUpsertParams(t, bindings.upsert)
}

func assertThreadBindingRecord(t *testing.T, got *threadBindingRecord, want bindingstore.Binding) {
	t.Helper()
	if got == nil {
		t.Fatal("binding record = nil")
	}
	wantRecord := threadBindingRecord{
		AgentID:            want.AgentID,
		Provider:           want.Provider,
		ProviderThreadID:   want.ProviderThreadID,
		CodexThreadID:      want.CodexThreadID,
		RolloutPath:        want.RolloutPath,
		Cwd:                want.Cwd,
		ParentAgentID:      want.ParentAgentID,
		AgentType:          want.AgentType,
		AgentMemoryScope:   want.AgentMemoryScope,
		Archived:           want.Archived,
		CreatedAt:          want.CreatedAt,
		UpdatedAt:          want.UpdatedAt,
		SessionUUID:        want.SessionUUID,
		CodexHome:          want.CodexHome,
		CodexInstanceKey:   want.CodexInstanceKey,
		CodexModelProvider: want.CodexModelProvider,
	}
	if !reflect.DeepEqual(*got, wantRecord) {
		t.Fatalf("binding record = %#v, want %#v", *got, wantRecord)
	}
}

func assertThreadBindingUpsertParams(t *testing.T, got bindingstore.UpsertParams) {
	t.Helper()
	want := bindingstore.UpsertParams{
		AgentID:            "agent-next",
		Provider:           "codex",
		ProviderThreadID:   "provider-next",
		CodexThreadID:      "thread-next",
		RolloutPath:        "/tmp/next.jsonl",
		SessionUUID:        "019e2c35-42ef-75b3-9f73-31cf7cc4cf2f",
		Cwd:                "/repo/next",
		ParentAgentID:      "parent-next",
		AgentType:          "worker",
		AgentMemoryScope:   "project",
		CreatedAt:          303,
		UpdatedAt:          404,
		CodexHome:          "/Users/dev/.codex-next",
		CodexInstanceKey:   "next",
		CodexModelProvider: "openai-compatible",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("binding upsert params = %#v, want %#v", got, want)
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

func TestBindingRegistrationCanonicalizesExistingAliasCodexHome(t *testing.T) {
	t.Parallel()

	canonicalHome, aliasHome := createCleanCodexHomeAlias(t)
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:            "agent-canonical",
		Provider:           "codex",
		ProviderThreadID:   "provider-thread-canonical",
		CodexThreadID:      "thread-canonical",
		Cwd:                "/repo",
		CodexHome:          aliasHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:     "thread-canonical",
		ProviderThreadID:   "provider-thread-canonical",
		AgentID:            "agent-canonical",
		Provider:           "codex",
		CWD:                "/repo",
		CodexHome:          canonicalHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}, true)
	if err != nil {
		t.Fatalf("persistThreadState() error = %v", err)
	}
	if len(bindings.upserts) != 1 {
		t.Fatalf("binding upserts = %d, want 1 canonical repair", len(bindings.upserts))
	}
	if bindings.upsert.CodexHome != canonicalHome {
		t.Fatalf("binding codex home = %q, want canonical %q", bindings.upsert.CodexHome, canonicalHome)
	}
	if bindings.binding.CodexHome != canonicalHome {
		t.Fatalf("persisted binding codex home = %q, want canonical %q", bindings.binding.CodexHome, canonicalHome)
	}
}

func TestBindingRegistrationRejectsCodexIdentityTupleConflict(t *testing.T) {
	t.Parallel()

	canonicalHome, _ := createCleanCodexHomeAlias(t)
	for _, tc := range []struct {
		name          string
		instanceKey   string
		modelProvider string
		wantMessage   string
	}{
		{
			name:          "instance key",
			instanceKey:   "other",
			modelProvider: "openai",
			wantMessage:   "codex instance key is immutable",
		},
		{
			name:          "model provider",
			instanceKey:   "default",
			modelProvider: "other-provider",
			wantMessage:   "codex model provider is immutable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			threads := &stubThreadStore{}
			bindings := &stubBindingStore{binding: &bindingstore.Binding{
				AgentID:            "agent-conflict-" + strings.ReplaceAll(tc.name, " ", "-"),
				Provider:           "codex",
				ProviderThreadID:   "provider-thread-conflict-" + strings.ReplaceAll(tc.name, " ", "-"),
				CodexThreadID:      "thread-conflict-" + strings.ReplaceAll(tc.name, " ", "-"),
				Cwd:                "/repo",
				CodexHome:          canonicalHome,
				CodexInstanceKey:   "default",
				CodexModelProvider: "openai",
				CreatedAt:          123,
			}}
			svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

			err := svc.persistThreadState(context.Background(), threadState{
				PublicThreadID:     bindings.binding.CodexThreadID,
				ProviderThreadID:   bindings.binding.ProviderThreadID,
				AgentID:            bindings.binding.AgentID,
				Provider:           "codex",
				CWD:                "/repo",
				CodexHome:          canonicalHome,
				CodexInstanceKey:   tc.instanceKey,
				CodexModelProvider: tc.modelProvider,
				CreatedAt:          123,
			}, true)
			if err == nil || !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("persistThreadState() error = %v, want %q", err, tc.wantMessage)
			}
			if len(bindings.upserts) != 0 {
				t.Fatalf("binding upserts = %d, want none on tuple conflict", len(bindings.upserts))
			}
			if bindings.binding.CodexInstanceKey != "default" || bindings.binding.CodexModelProvider != "openai" {
				t.Fatalf("binding identity changed to %q/%q, want original default/openai",
					bindings.binding.CodexInstanceKey,
					bindings.binding.CodexModelProvider)
			}
		})
	}
}

func TestBindingRegistrationRejectsNonAliasCodexHomeRepair(t *testing.T) {
	t.Parallel()

	canonicalHome, _ := createCleanCodexHomeAlias(t)
	otherHome, _ := createCleanCodexHomeAlias(t)
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:            "agent-home-conflict",
		Provider:           "codex",
		ProviderThreadID:   "provider-thread-home-conflict",
		CodexThreadID:      "thread-home-conflict",
		Cwd:                "/repo",
		CodexHome:          canonicalHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:     "thread-home-conflict",
		ProviderThreadID:   "provider-thread-home-conflict",
		AgentID:            "agent-home-conflict",
		Provider:           "codex",
		CWD:                "/repo",
		CodexHome:          otherHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}, true)
	if err == nil || !strings.Contains(err.Error(), "codex home is immutable") {
		t.Fatalf("persistThreadState() error = %v, want codex home immutable rejection", err)
	}
	if len(bindings.upserts) != 0 {
		t.Fatalf("binding upserts = %d, want none on non-alias home repair", len(bindings.upserts))
	}
	if bindings.binding.CodexHome != canonicalHome {
		t.Fatalf("binding codex home = %q, want original %q", bindings.binding.CodexHome, canonicalHome)
	}
}

func TestBindingRegistrationHistoryInputUsesCanonicalCodexHome(t *testing.T) {
	t.Parallel()

	canonicalHome, aliasHome := createCleanCodexHomeAlias(t)
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:            "agent-history-canonical",
		Provider:           "codex",
		ProviderThreadID:   "provider-thread-history-canonical",
		CodexThreadID:      "thread-history-canonical",
		Cwd:                "/repo",
		CodexHome:          aliasHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:     "thread-history-canonical",
		ProviderThreadID:   "provider-thread-history-canonical",
		AgentID:            "agent-history-canonical",
		Provider:           "codex",
		CWD:                "/repo",
		CodexHome:          canonicalHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}, true)
	if err != nil {
		t.Fatalf("persistThreadState() error = %v", err)
	}
	historyReq := readMessagesHistoryRequestForSession("thread-history-canonical", bindings.binding, nil)
	if historyReq.CodexHome != canonicalHome {
		t.Fatalf("history request codex home = %q, want canonical %q", historyReq.CodexHome, canonicalHome)
	}
}

func TestPersistThreadStateUpdatesExistingBindingSessionUUID(t *testing.T) {
	t.Parallel()

	const sessionUUID = "019e2c35-42ef-75b3-9f73-31cf7cc4cf2e"
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:            "agent-session",
		Provider:           "codex",
		ProviderThreadID:   "",
		CodexThreadID:      "agent-session",
		Cwd:                "/repo",
		CodexHome:          "/Users/mac/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}}
	svc := NewService(silentLogger(), threads, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.persistThreadState(context.Background(), threadState{
		PublicThreadID:     "agent-session",
		AgentID:            "agent-session",
		Provider:           "codex",
		CWD:                "/repo",
		SessionUUID:        sessionUUID,
		CodexHome:          "/Users/mac/.codex",
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
		CreatedAt:          123,
	}, true)
	if err != nil {
		t.Fatalf("persistThreadState() error = %v", err)
	}
	if len(bindings.upserts) != 1 {
		t.Fatalf("binding upserts = %d, want 1", len(bindings.upserts))
	}
	if bindings.upsert.SessionUUID != sessionUUID {
		t.Fatalf("binding session_uuid = %q, want %s", bindings.upsert.SessionUUID, sessionUUID)
	}
	if bindings.upsert.ProviderThreadID != "" {
		t.Fatalf("binding provider_thread_id = %q, want empty until history is recoverable", bindings.upsert.ProviderThreadID)
	}
}
