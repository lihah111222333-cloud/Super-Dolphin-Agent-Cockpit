package thread

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestServiceStartRequiresOrchestration(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, nil, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-no-orchestration",
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Prompt:   "start",
	})
	if err == nil || !strings.Contains(err.Error(), "orchestration service is not configured") {
		t.Fatalf("Start() error = %v, want missing orchestration error", err)
	}
	if threads.upsertCount != 0 {
		t.Fatalf("thread upsert count = %d, want 0 before failed launch is persisted", threads.upsertCount)
	}
}

func TestUpsertPublicThreadRequiresThreadStore(t *testing.T) {
	t.Parallel()

	err := (&service{}).upsertPublicThread(context.Background(), threadState{PublicThreadID: "thread-1"}, bindingWriteOutcome{})
	if err == nil || !strings.Contains(err.Error(), "thread store is not configured") {
		t.Fatalf("upsertPublicThread() error = %v, want missing thread store error", err)
	}
}

func TestSavePromptSnapshotRequiresThreadStore(t *testing.T) {
	t.Parallel()

	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{DisplayName: "assembled"}, "codex")
	err := (&service{}).savePromptSnapshot(context.Background(), "thread-1", assembly)
	if err == nil || !strings.Contains(err.Error(), "thread store is not configured") {
		t.Fatalf("savePromptSnapshot() error = %v, want missing thread store error", err)
	}
}

func TestSavePromptSnapshotRejectsEmptySnapshot(t *testing.T) {
	t.Parallel()

	err := (&service{threadStore: &stubThreadStore{}}).savePromptSnapshot(
		context.Background(),
		"thread-1",
		contract.StartAssembly{},
	)
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot is empty") {
		t.Fatalf("savePromptSnapshot() error = %v, want empty snapshot error", err)
	}
}

func TestResolveStablePromptSnapshotPropagatesStoreError(t *testing.T) {
	t.Parallel()

	cause := errors.New("snapshot store unavailable")
	svc := &service{threadStore: &stubThreadStore{promptSnapshotError: cause}}

	_, err := svc.resolveStablePromptSnapshot(
		context.Background(),
		"thread-1",
		"codex",
		contract.PromptAssemblySnapshot{DisplayName: "fallback"},
	)
	if !errors.Is(err, cause) {
		t.Fatalf("resolveStablePromptSnapshot() error = %v, want %v", err, cause)
	}
}

func TestUnarchivePendingLaunchDoesNotRequireBinding(t *testing.T) {
	t.Parallel()

	bindingStore := &stubThreadBindingStore{}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:      "thread-pending",
			Status:        statusArchived,
			PendingLaunch: true,
		}},
	}
	var stopped threaddto.Stopped
	svc := &service{
		bindingStore: bindingStore,
		threadStore:  threadStore,
		emitStopped: func(evt threaddto.Stopped) {
			stopped = evt
		},
	}

	if err := svc.Unarchive(context.Background(), "thread-pending"); err != nil {
		t.Fatalf("Unarchive() error = %v", err)
	}
	if threadStore.status.ThreadID != "thread-pending" || threadStore.status.Status != statusCreated {
		t.Fatalf("thread status = %#v, want pending thread recreated", threadStore.status)
	}
	if len(bindingStore.archived) != 0 {
		t.Fatalf("binding archive calls = %#v, want none for pending_launch", bindingStore.archived)
	}
	if stopped.ThreadID != "thread-pending" || stopped.Status != statusCreated || stopped.Reason != "unarchived_pending_launch" {
		t.Fatalf("stopped event = %#v, want pending thread created/unarchived", stopped)
	}
}

func TestForkSavesInheritedPromptSnapshotAfterNewThreadRowExists(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := &snapshotRequiresThreadRowStore{stubThreadStore: forkParentThreadStore()}
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Fork(context.Background(), "thread-parent"); err != nil {
		t.Fatalf("Fork() error = %v, want snapshot saved after durable fork row exists", err)
	}
	if len(threads.savePromptSnapshotIDs) != 1 || threads.savePromptSnapshotIDs[0] != "thread-fork" {
		t.Fatalf("SavePromptSnapshot IDs = %#v, want [thread-fork]", threads.savePromptSnapshotIDs)
	}
	if threads.upsertCount == 0 {
		t.Fatal("thread upsert count = 0, want fork row persisted before snapshot save")
	}
}

type snapshotRequiresThreadRowStore struct {
	*stubThreadStore
}

func (s *snapshotRequiresThreadRowStore) SavePromptSnapshot(ctx context.Context, threadID string, snapshot threadstore.PromptSnapshot) error {
	if s.thread == nil || s.thread.ThreadID != threadID {
		return fmt.Errorf("new thread row %q does not exist before snapshot save", threadID)
	}
	return s.stubThreadStore.SavePromptSnapshot(ctx, threadID, snapshot)
}

func TestPostSnapshotResumeRejectsMissingPromptSnapshot(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &threadstore.Thread{
		ThreadID:  "thread-resume",
		AgentID:   "agent-resume",
		Prompt:    "resume name",
		Model:     "gpt-5.5",
		Cwd:       "/repo",
		CreatedAt: 123,
		Status:    statusCreated,
	}}
	const providerThreadID = "11111111-2222-3333-4444-555555555581"
	bindings := &stubBindingStore{binding: &bindingstore.Binding{
		AgentID:          "agent-resume",
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		CodexThreadID:    "thread-resume",
		RolloutPath:      writeExistingProviderHistoryFile(t),
		Cwd:              "/repo",
	}}
	sessions := &stubSessionProvider{}
	resumeCalled := false
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		resumeCalled = true
		sessions.session = &stubSession{threadID: providerThreadID}
		return sessions.session, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewServiceWithPromptAssembly(
		silentLogger(),
		threads,
		bindings,
		sessions,
		starter,
		nil,
		orch,
		nil,
		&resumeMetadataPromptAssembly{},
		nil,
		nil,
	).(*service)

	_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-resume"})
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("Resume() error = %v, want missing prompt snapshot error", err)
	}
	if resumeCalled {
		t.Fatal("ResumeSession was called despite missing stored prompt snapshot")
	}
	if orch.launch.AgentID != "" {
		t.Fatalf("orchestration launch = %#v, want none before snapshot preflight passes", orch.launch)
	}
}

func TestPostSnapshotForkRejectsInvalidPromptSnapshot(t *testing.T) {
	t.Parallel()

	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	snapshot := validThreadPromptSnapshotForTest("Forked Thread")
	snapshot.SectionSnapshot["identity"] = "tampered after hash"
	threads := forkParentThreadStore()
	threads.promptSnapshot = &snapshot
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called with an invalid stored prompt snapshot")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "thread-parent")
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("Fork() error = %v, want invalid prompt snapshot error", err)
	}
	if originalSession.forkRequest.ThreadID != "" {
		t.Fatalf("ForkThread request = %#v, want no provider fork before snapshot preflight", originalSession.forkRequest)
	}
	if orch.launch.AgentID != "" {
		t.Fatalf("orchestration launch = %#v, want none before snapshot preflight passes", orch.launch)
	}
}

func TestLegacyThreadUsesExplicitSnapshotMigrationGate(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, config []byte, wantErr bool) {
		t.Helper()
		threads := &stubThreadStore{thread: &threadstore.Thread{
			ThreadID:         "thread-legacy",
			AgentID:          "agent-legacy",
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "local",
			Prompt:           "legacy name",
			Model:            "gpt-5.5",
			Cwd:              "/repo",
			ConfigOverride:   config,
			CreatedAt:        123,
			Status:           statusCreated,
		}}
		const providerThreadID = "11111111-2222-3333-4444-555555555582"
		bindings := &stubBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-legacy",
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "local",
			Provider:         "codex",
			ProviderThreadID: providerThreadID,
			CodexThreadID:    "thread-legacy",
			RolloutPath:      writeExistingProviderHistoryFile(t),
			Cwd:              "/repo",
		}}
		sessions := &stubSessionProvider{}
		resumeCalled := false
		starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			resumeCalled = true
			sessions.session = &stubSession{threadID: providerThreadID}
			return sessions.session, nil
		}}
		svc := NewServiceWithPromptAssembly(
			silentLogger(),
			threads,
			bindings,
			sessions,
			starter,
			nil,
			&forkOrchestrationStub{},
			nil,
			&resumeMetadataPromptAssembly{},
			nil,
			nil,
		).(*service)

		_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-legacy"})
		if wantErr {
			if err == nil || !strings.Contains(err.Error(), "legacy") {
				t.Fatalf("Resume() error = %v, want legacy migration gate error", err)
			}
			if resumeCalled {
				t.Fatal("ResumeSession was called without explicit legacy migration gate")
			}
			return
		}
		if err != nil {
			t.Fatalf("Resume() error = %v, want nil with explicit legacy migration gate", err)
		}
		if !resumeCalled {
			t.Fatal("ResumeSession was not called with explicit legacy migration gate")
		}
	}

	t.Run("without_gate_rejects", func(t *testing.T) {
		run(t, nil, true)
	})
	t.Run("with_gate_allows_migration", func(t *testing.T) {
		config := legacyPromptSnapshotMigrationConfig(t)
		run(t, config, false)
	})
}

func TestStartDoesNotPublishStartedBeforeSnapshotSaved(t *testing.T) {
	t.Parallel()

	cause := errors.New("snapshot write failed")
	threads := &stubThreadStore{savePromptSnapshotError: cause}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)
	startedCount := 0
	svc.emitStarted = func(threaddto.Started) {
		startedCount++
	}

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-start",
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Prompt:   "start",
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Start() error = %v, want %v", err, cause)
	}
	if startedCount != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 before prompt snapshot is saved", startedCount)
	}
}

func TestForkDoesNotPublishStartedBeforeSnapshotSaved(t *testing.T) {
	t.Parallel()

	cause := errors.New("fork snapshot write failed")
	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := forkParentThreadStore()
	threads.savePromptSnapshotError = cause
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)
	startedCount := 0
	svc.emitStarted = func(threaddto.Started) {
		startedCount++
	}

	_, err := svc.Fork(context.Background(), "thread-parent")
	if !errors.Is(err, cause) {
		t.Fatalf("Fork() error = %v, want %v", err, cause)
	}
	if startedCount != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 before fork prompt snapshot is saved", startedCount)
	}
}

func TestPromptSnapshotHashCoversSectionSnapshot(t *testing.T) {
	t.Parallel()

	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           "snapshot name",
		BaseInstructions:      "snapshot base",
		DeveloperInstructions: "snapshot dev",
		ResolvedSections: []contract.ResolvedPromptSection{
			{Name: "identity", Content: "You are Codex."},
			{Name: "language", Content: "Use Chinese."},
		},
		Snapshot: contract.PromptAssemblySnapshot{Generation: 9},
	}, "codex")
	snapshot := assembly.Snapshot
	if !storedPromptSnapshotValid(snapshot, "codex") {
		t.Fatalf("storedPromptSnapshotValid() = false for freshly generated snapshot: %#v", snapshot)
	}

	tampered := snapshot
	tampered.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	tampered.SectionSnapshot["language"] = "Use French."
	if storedPromptSnapshotValid(tampered, "codex") {
		t.Fatal("storedPromptSnapshotValid() = true after section snapshot tamper, want false")
	}
}

func validThreadPromptSnapshotForTest(displayName string) threadstore.PromptSnapshot {
	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		ResolvedSections: []contract.ResolvedPromptSection{
			{Name: "identity", Content: "stable identity"},
		},
		Snapshot: contract.PromptAssemblySnapshot{Generation: 7},
	}, "codex")
	return promptSnapshotRecordToStore(toStoredPromptSnapshot(assembly.Snapshot))
}

func legacyPromptSnapshotMigrationConfig(t *testing.T) []byte {
	t.Helper()
	config, err := encodeStoredThreadConfig(storedThreadConfig{
		Runtime: map[string]any{
			"legacyPromptSnapshotMigration": true,
		},
	})
	if err != nil {
		t.Fatalf("encodeStoredThreadConfig() error = %v", err)
	}
	return config
}
