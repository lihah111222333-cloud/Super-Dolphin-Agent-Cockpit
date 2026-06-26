package thread

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
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

func TestForkPersistsInheritedPromptSnapshotBeforeReturning(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("snapshot store down")
	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := forkParentThreadStore()
	threads.savePromptSnapshotError = storeErr
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		t.Fatal("ResumeSession should not be called when fork snapshot persistence fails")
		return nil, nil
	}}
	orch := &forkOrchestrationStub{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Fork(context.Background(), "thread-parent")
	if !errors.Is(err, storeErr) {
		t.Fatalf("Fork() error = %v, want snapshot store error", err)
	}
	if len(threads.savePromptSnapshotIDs) != 1 || threads.savePromptSnapshotIDs[0] != "thread-fork" {
		t.Fatalf("SavePromptSnapshot IDs = %#v, want [thread-fork]", threads.savePromptSnapshotIDs)
	}
	if orch.launch.AgentID != "" {
		t.Fatalf("LaunchAgent = %#v, want no fork launch after snapshot failure", orch.launch)
	}
	if len(orch.bindAgentIDs) != 0 {
		t.Fatalf("BindSessionGeneration calls = %#v, want none", orch.bindAgentIDs)
	}
	if threads.upsertCount != 0 {
		t.Fatalf("thread upsert count = %d, want 0", threads.upsertCount)
	}
	if len(bindings.upserts) != 0 {
		t.Fatalf("binding upserts = %#v, want none", bindings.upserts)
	}
}
