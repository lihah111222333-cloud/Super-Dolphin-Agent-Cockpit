package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/store/taskdag"
)

func TestRecoverReplaysStoreBackedActiveTurnAheadOfQueuedWork(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil)
	svc.recoveryStore = stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"replay me","selectedSkills":["debug"]}`),
			},
		},
	}

	agent := svc.newAgentLocked("agent-1")
	agent.command = []string{"sh", "-c", "sleep 60"}
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.queue.Enqueue(TurnSubmission{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "queued work"}},
	})
	svc.agents[agent.id] = agent
	t.Cleanup(func() {
		if agent.cmd != nil {
			_ = stopProcess(agent.cmd)
		}
	})

	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if agent.state != agentdto.StateTurnQueued {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnQueued)
	}
	if got := agent.queue.Len(); got != 2 {
		t.Fatalf("queue.Len() = %d, want 2", got)
	}

	first, ok := agent.queue.Dequeue()
	if !ok {
		t.Fatal("first Dequeue() ok = false, want true")
	}
	if first.ExpectedTurnID != "turn-active" {
		t.Fatalf("first.ExpectedTurnID = %q, want turn-active", first.ExpectedTurnID)
	}
	if first.ThreadID != "thread-1" {
		t.Fatalf("first.ThreadID = %q, want thread-1", first.ThreadID)
	}
	if len(first.Inputs) != 1 || first.Inputs[0].Content != "replay me" {
		t.Fatalf("first.Inputs = %#v, want replayed prompt", first.Inputs)
	}

	second, ok := agent.queue.Dequeue()
	if !ok {
		t.Fatal("second Dequeue() ok = false, want true")
	}
	if len(second.Inputs) != 1 || second.Inputs[0].Content != "queued work" {
		t.Fatalf("second.Inputs = %#v, want queued work", second.Inputs)
	}
}

func TestFireOrForceLockedIncludesContextForIllegalTransition(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle

	err := svc.fireOrForceLocked(context.Background(), agent, agentdto.TriggerTurnAccepted)
	if err == nil {
		t.Fatal("fireOrForceLocked() error = nil, want non-nil")
	}
	for _, want := range []string{
		`state=idle`,
		`trigger=turn_accepted`,
		`turn_enqueued`,
		`recover_requested`,
		`stop_requested`,
		`process_exited`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fireOrForceLocked() error = %q, want substring %q", err, want)
		}
	}
}

func TestFireOrForceLockedIncludesContextWhenStateMachineMissing(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil)
	agent := &agentRuntime{id: "agent-1", state: agentdto.StateIdle}

	err := svc.fireOrForceLocked(context.Background(), agent, agentdto.TriggerTurnAccepted)
	if err == nil {
		t.Fatal("fireOrForceLocked() error = nil, want non-nil")
	}
	for _, want := range []string{
		`state=idle`,
		`trigger=turn_accepted`,
		`turn_enqueued`,
		`state machine is not initialized`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fireOrForceLocked() error = %q, want substring %q", err, want)
		}
	}
}

type stubRecoveryTurnStore struct {
	nodes   []taskdag.Node
	wakeups map[int64]taskdag.Wakeup
}

func (s stubRecoveryTurnStore) ListRunningNodesByAssignee(_ context.Context, assignee string) ([]taskdag.Node, error) {
	filtered := make([]taskdag.Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		if node.AssignedTo == assignee {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (s stubRecoveryTurnStore) GetWakeup(_ context.Context, id int64) (*taskdag.Wakeup, error) {
	wakeup, ok := s.wakeups[id]
	if !ok {
		return nil, errors.New("wakeup not found")
	}
	return &wakeup, nil
}

func testStringPtr(value string) *string { return &value }

func testInt64Ptr(value int64) *int64 { return &value }
