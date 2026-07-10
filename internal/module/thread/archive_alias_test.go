package thread

import (
	"context"
	"testing"
)

func TestArchiveResolvesBindingWhenPendingLaunchProbeMissesAlias(t *testing.T) {
	t.Parallel()

	calls := []string{}
	bindingStore := &stubThreadBindingStore{
		binding: &BindingRecord{
			AgentID:       "agent-1",
			CodexThreadID: "thread-1",
		},
		calls: &calls,
	}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		calls: &calls,
	}
	svc := &service{
		bindingStore: bindingStore,
		threadStore:  threadStore,
		sessions: &stubThreadSessions{
			agentID: "agent-1",
			session: &stubThreadSession{threadID: "thread-1", calls: &calls},
			calls:   &calls,
		},
		turns:         &stubTurnService{calls: &calls},
		orchestration: &stubThreadOrchestration{calls: &calls},
	}

	if err := svc.Archive(context.Background(), "agent-1"); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after alias resolution")
	assertStopCallPresent(t, calls, "thread_status:thread-1:archived", "archive status for public thread after alias resolution")
}
