package thread

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
