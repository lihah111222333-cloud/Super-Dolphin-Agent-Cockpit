package thread

import (
	"context"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestStopContinuesWhenLocalSessionAndManagedAgentAlreadyGone(t *testing.T) {
	t.Parallel()

	calls := []string{}
	sessions := &stubThreadSessions{
		agentID:    "agent-1",
		getErr:     contract.ErrSessionNotFound,
		generation: 11,
		calls:      &calls,
	}
	orch := &stubThreadOrchestration{calls: &calls, stopErr: contract.ErrAgentNotFound}
	threadStore := &recordingThreadStore{
		stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID: "thread-1",
			AgentID:  "agent-1",
			Status:   statusCreated,
		}},
		calls: &calls,
	}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &BindingRecord{
			AgentID:       "agent-1",
			CodexThreadID: "thread-1",
		}},
		threadStore:   threadStore,
		sessions:      sessions,
		turns:         &stubTurnService{calls: &calls},
		orchestration: orch,
	}

	if err := svc.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	assertStopCallPresent(t, calls, "agent_stop:agent-1", "managed agent stop after stale local session")
	assertStopCallPresent(t, calls, "thread_status:thread-1:stopped", "stopped status after stale managed agent")
	if !reflect.DeepEqual(sessions.removedGenerations, []uint64{11}) {
		t.Fatalf("removed generations = %#v, want [11]", sessions.removedGenerations)
	}
}
