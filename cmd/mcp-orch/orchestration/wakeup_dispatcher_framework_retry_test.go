package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

func TestWakeupDispatcher_RouterFrameworkErrorWithRecordedSpawnDoesNotFailDAGAtMaxAttempts(t *testing.T) {
	spawningThreadID := "thread-already-launched"
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:           59,
			DagKey:       "dag-1",
			NodeKey:      "n1",
			RunID:        int64Ptr(7004),
			ClaimedBy:    "worker-a",
			AttemptCount: 1,
		}},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-1",
			Metadata: testRawConfig(t, `{"schedule":{"default_retry":0},"fail_fast":true}`),
		},
		nodesReply: []taskdag.Node{{
			DagKey:           "dag-1",
			NodeKey:          "n1",
			NodeType:         "agent",
			Status:           string(nodeexec.NodeStatusReady),
			Config:           testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
			SpawningThreadID: &spawningThreadID,
		}},
		runningErr: errors.New("running status write failed"),
	}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	launcher := &stubAgentLauncher{threadID: "duplicate-child"}
	agentExec := newTestAgentExecutor(launcher)
	d.WithNodeRouter(NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(launcher.calls) != 0 {
		t.Fatalf("launcher calls = %d, want 0 when spawning_thread_id is already recorded", len(launcher.calls))
	}
	if len(store.retryCalls) != 1 {
		t.Fatalf("RetryWakeup calls = %d, want 1 for framework writeback retry", len(store.retryCalls))
	}
	if len(store.failCalls) != 0 || len(store.failNodeCalls) != 0 {
		t.Fatalf("failCalls=%d failNodeCalls=%d, want no DAG failure for framework writeback retry", len(store.failCalls), len(store.failNodeCalls))
	}
}

func TestWakeupDispatcher_SpawnWritebackFailureDoesNotRetryUnrecordedLaunch(t *testing.T) {
	store := &dispatcherStubStore{
		claimReply: []taskdag.Wakeup{{
			ID:           60,
			DagKey:       "dag-1",
			NodeKey:      "n1",
			RunID:        int64Ptr(7005),
			ClaimedBy:    "worker-a",
			AttemptCount: 1,
		}},
		dagReply: &taskdag.DAG{
			DagKey:   "dag-1",
			Metadata: testRawConfig(t, `{"schedule":{"default_retry":0},"fail_fast":true}`),
		},
		nodesReply: []taskdag.Node{{
			DagKey:   "dag-1",
			NodeKey:  "n1",
			NodeType: "agent",
			Status:   string(nodeexec.NodeStatusReady),
			Config:   testRawConfig(t, `{"exec":{"agent_key":"alpha","cwd":"/tmp/node-cwd"},"first_turn":"hi"}`),
		}},
		runningErr: errors.New("ready->running write must not be reached"),
	}
	d, err := NewWakeupDispatcher(store, &dispatcherStubLauncher{}, nil, WakeupDispatcherConfig{ClaimedBy: "worker-a"})
	if err != nil {
		t.Fatalf("NewWakeupDispatcher err = %v", err)
	}
	launcher := &stubAgentLauncher{threadID: "thread-launched-unrecorded"}
	agentExec := newTestAgentExecutor(launcher, nodeexec.WithRecorder(failingNodeSpawnRecorder{}))
	d.WithNodeRouter(NewNodeExecutorRouter(store, agentExec, nil, nil, nil, nil))

	n, err := d.ProcessBatch(context.Background())
	if err != nil {
		t.Fatalf("ProcessBatch err = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessBatch handled = %d, want 1", n)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("launcher calls = %d, want exactly one launch before fail-fast", len(launcher.calls))
	}
	if len(store.runningCalls) != 0 {
		t.Fatalf("running calls = %d, want 0 without durable spawning_thread_id", len(store.runningCalls))
	}
	if len(store.retryCalls) != 0 {
		t.Fatalf("RetryWakeup calls = %d, want 0 after unrecorded launch", len(store.retryCalls))
	}
	if len(store.failCalls) != 1 || len(store.failNodeCalls) != 1 {
		t.Fatalf("failCalls=%d failNodeCalls=%d, want wakeup and DAG node failed", len(store.failCalls), len(store.failNodeCalls))
	}
}
