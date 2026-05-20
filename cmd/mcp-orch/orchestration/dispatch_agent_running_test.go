package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/jackc/pgx/v5"
)

// TestDispatchAgent_WritesRunningOnSuccess locks in the ADR-017 v1.2 §2.4
// happy path: agent.Execute returns done, dispatchAgent must call
// UpdateRunningNodeStatus(running) and the metric counter advances by 1.
func TestDispatchAgent_WritesRunningOnSuccess(t *testing.T) {
	before := DispatchAgentRunningCounters()

	launcher := &stubAgentLauncher{threadID: "thr-1"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   "ready",
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 42, DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	if len(store.runningStatusCalls) != 1 {
		t.Fatalf("runningStatusCalls = %d, want 1", len(store.runningStatusCalls))
	}
	got := store.runningStatusCalls[0]
	if got.DagKey != "dag-1" || got.NodeKey != "n1" || got.RunID != 7 || got.Status != "running" || got.WakeupID != 42 {
		t.Fatalf("got = %+v, want dag-1/n1/running/wakeup=42", got)
	}
	after := DispatchAgentRunningCounters()
	if after.Written-before.Written != 1 {
		t.Fatalf("Written delta = %d, want 1", after.Written-before.Written)
	}
}

// TestDispatchAgent_RaceWindowD_NoRowsIsSilent covers ADR-017 v1.2 §2.6
// Window D: subscriber already pushed done/failed before dispatchAgent
// finishes its UpdateRunningNodeStatus call. pgx.ErrNoRows must be swallowed
// silently — dispatchAgent still returns the executor outcome, the metric
// counter records SkippedAlreadyTerminal +1 (not WriteFailed).
func TestDispatchAgent_RaceWindowD_NoRowsIsSilent(t *testing.T) {
	before := DispatchAgentRunningCounters()

	launcher := &stubAgentLauncher{threadID: "thr-2"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   "done", // subscriber already pushed terminal
		}},
		runningStatusErr: pgx.ErrNoRows,
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 7, DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v (race D should be silent)", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done (executor outcome preserved)", outcome.Status)
	}
	if len(store.runningStatusCalls) != 1 {
		t.Fatalf("runningStatusCalls = %d, want 1", len(store.runningStatusCalls))
	}
	after := DispatchAgentRunningCounters()
	if after.SkippedAlreadyTerminal-before.SkippedAlreadyTerminal != 1 {
		t.Fatalf("SkippedAlreadyTerminal delta = %d, want 1", after.SkippedAlreadyTerminal-before.SkippedAlreadyTerminal)
	}
	if after.WriteFailed != before.WriteFailed {
		t.Fatalf("WriteFailed must NOT advance on race-D, got delta %d", after.WriteFailed-before.WriteFailed)
	}
}

// TestDispatchAgent_DBErrorIsPropagated verifies that a generic DB error from
// UpdateRunningNodeStatus is not hidden after the child launch succeeds.
// The framework error must propagate so dispatcher can retry/fail visibly.
func TestDispatchAgent_DBErrorIsPropagated(t *testing.T) {
	before := DispatchAgentRunningCounters()

	launcher := &stubAgentLauncher{threadID: "thr-3"}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   "ready",
		}},
		runningStatusErr: errors.New("simulated DB connection drop"),
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 99, DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err == nil || !strings.Contains(err.Error(), "ready->running write failed") {
		t.Fatalf("RouteByWakeup err = %v, want ready->running write failure", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done (executor outcome preserved)", outcome.Status)
	}
	after := DispatchAgentRunningCounters()
	if after.WriteFailed-before.WriteFailed != 1 {
		t.Fatalf("WriteFailed delta = %d, want 1", after.WriteFailed-before.WriteFailed)
	}
}

func TestDispatchAgent_RetryWithRecordedSpawnDoesNotLaunchDuplicateChild(t *testing.T) {
	launcher := &stubAgentLauncher{threadID: "duplicate-child"}
	agentExec := newTestAgentExecutor(launcher, nil)
	spawningThreadID := "thread-already-launched"
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:           "dag-1",
			NodeKey:          "n1",
			RunID:            routerTestRunID(7),
			NodeType:         "agent",
			Title:            "n1",
			Config:           json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:           "ready",
			SpawningThreadID: &spawningThreadID,
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 77, DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusDone {
		t.Fatalf("outcome.Status = %v, want done", outcome.Status)
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 when spawning_thread_id is already recorded", len(launcher.calls))
	}
	if len(store.runningStatusCalls) != 1 {
		t.Fatalf("runningStatusCalls = %d, want 1", len(store.runningStatusCalls))
	}
}

// TestDispatchAgent_LaunchFailedDoesNotWriteRunning ensures that when the
// executor itself surfaces a launch error (or NodeStatusFailed validation),
// dispatchAgent does NOT attempt the ready→running write — letting dispatcher
// pick up the failure cleanly (retry / mark failed).
func TestDispatchAgent_LaunchFailedDoesNotWriteRunning(t *testing.T) {
	launcher := &stubAgentLauncher{err: errors.New("launch refused: bad agent_key")}
	agentExec := newTestAgentExecutor(launcher, nil)
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"bad"},"first_turn":"hi"}`),
			Status:   "ready",
		}},
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, _ := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 1, DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if outcome.Status != nodeexec.NodeStatusFailed {
		t.Fatalf("outcome.Status = %v, want failed (launch refused)", outcome.Status)
	}
	if len(store.runningStatusCalls) != 0 {
		t.Fatalf("runningStatusCalls = %d, want 0 (must NOT write running when launch fails)", len(store.runningStatusCalls))
	}
}

func TestDispatchAgent_SpawnWritebackFailureDoesNotWriteRunning(t *testing.T) {
	launcher := &stubAgentLauncher{threadID: "thread-launched-unrecorded"}
	agentExec := newTestAgentExecutor(launcher, nodeexec.WithRecorder(failingNodeSpawnRecorder{}))
	store := &stubRouterStore{
		nodes: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			RunID:    routerTestRunID(7),
			NodeType: "agent",
			Title:    "n1",
			Config:   json.RawMessage(`{"exec":{"agent_key":"alpha"},"first_turn":"hi"}`),
			Status:   "ready",
		}},
		runningStatusErr: errors.New("must not be reached"),
	}
	router := NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil)

	outcome, err := router.RouteByWakeup(context.Background(), &taskdag.Wakeup{
		ID: 12, DagKey: "dag-1", NodeKey: "n1", RunID: routerTestRunID(7),
	})
	if err != nil {
		t.Fatalf("RouteByWakeup err = %v", err)
	}
	if outcome.Status != nodeexec.NodeStatusFailed || outcome.FailureClass != nodeexec.FailureClassHard {
		t.Fatalf("outcome = (%q,%q), want hard failed", outcome.Status, outcome.FailureClass)
	}
	if len(store.runningStatusCalls) != 0 {
		t.Fatalf("runningStatusCalls = %d, want 0 after spawn writeback failure", len(store.runningStatusCalls))
	}
}

type failingNodeSpawnRecorder struct{}

func (failingNodeSpawnRecorder) RecordNodeSpawn(context.Context, string, string, int64, string) error {
	return errors.New("record spawn failed")
}
