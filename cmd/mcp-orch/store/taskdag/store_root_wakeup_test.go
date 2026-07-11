//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestScheduleRootWakeups_EnqueuesOnlyAssignedReadyRoots(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedRunID(db, "dag-1", "run-a")
	seedRuntimeNode(t, db, now, runID, "root-assigned", nil, "ready", "agent-root")
	seedRuntimeNode(t, db, now, runID, "root-unassigned", nil, "ready", "")
	seedRuntimeNode(t, db, now, runID, "root-pending", nil, "pending", "agent-pending")
	seedRuntimeNode(t, db, now, runID, "child", []string{"root-assigned"}, "pending", "agent-child")

	rows, err := any(store).(RunStore).ScheduleRootWakeups(context.Background(), "dag-1", runID)
	requireNoScheduleRootError(t, err, "ScheduleRootWakeups")
	assertScheduleRootRows(t, rows, 1, "ScheduleRootWakeups")
	assertPendingWakeupCount(t, db, "root-assigned", 1)
	assertNoPendingWakeups(t, db, []string{"root-unassigned", "root-pending", "child"})
	assertRunHasDispatchBlockedEvent(t, db, "run-a", "root-unassigned", "assigned_to")

	wakeup := lookupPendingWakeup(t, db, "root-assigned")
	assertRootWakeupRoute(t, wakeup, runID)
	assertRootWakeupPayload(t, wakeup)

	rows, err = any(store).(RunStore).ScheduleRootWakeups(context.Background(), "dag-1", runID)
	requireNoScheduleRootError(t, err, "ScheduleRootWakeups replay")
	assertScheduleRootReplay(t, db, rows)
}

func TestScheduleRootWakeups_BlocksAssignedRootMissingCWD(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedRunID(db, "dag-1", "run-missing-cwd")
	seedRuntimeNode(t, db, now, runID, "root", nil, "ready", "agent-root")
	setRuntimeNodeConfig(t, db, runID, "root", json.RawMessage(`{"exec":{"agent_key":"alpha"}}`))

	rows, err := any(store).(RunStore).ScheduleRootWakeups(context.Background(), "dag-1", runID)
	requireNoScheduleRootError(t, err, "ScheduleRootWakeups")
	assertScheduleRootRows(t, rows, 0, "ScheduleRootWakeups missing cwd")
	assertPendingWakeupCount(t, db, "root", 0)
	assertRunHasDispatchBlockedEvent(t, db, "run-missing-cwd", "root", "exec.cwd")
}

func TestScheduleRootWakeups_BlocksAssignedRootMissingAgentIdentity(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	runID := seedRunID(db, "dag-1", "run-missing-identity")
	seedRuntimeNode(t, db, now, runID, "root", nil, "ready", "agent-root")
	setRuntimeNodeConfig(t, db, runID, "root", json.RawMessage(`{"exec":{"cwd":"/tmp/node-cwd"}}`))

	rows, err := any(store).(RunStore).ScheduleRootWakeups(context.Background(), "dag-1", runID)
	requireNoScheduleRootError(t, err, "ScheduleRootWakeups")
	assertScheduleRootRows(t, rows, 0, "ScheduleRootWakeups missing identity")
	assertPendingWakeupCount(t, db, "root", 0)
	assertRunHasDispatchBlockedEvent(t, db, "run-missing-identity", "root", "agent_key")
}

func requireNoScheduleRootError(t *testing.T, err error, label string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
}

func assertScheduleRootRows(t *testing.T, got, want int64, label string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s rows = %d, want %d", label, got, want)
	}
}

func assertPendingWakeupCount(t *testing.T, db *fakeTaskDAGDB, nodeKey string, want int) {
	t.Helper()
	if got := pendingForNode(db, nodeKey); got != want {
		t.Fatalf("%s wakeups = %d, want %d", nodeKey, got, want)
	}
}

func assertNoPendingWakeups(t *testing.T, db *fakeTaskDAGDB, nodeKeys []string) {
	t.Helper()
	for _, nodeKey := range nodeKeys {
		assertPendingWakeupCount(t, db, nodeKey, 0)
	}
}

func assertRootWakeupRoute(t *testing.T, wakeup sqlc.TaskDagWakeup, runID int64) {
	t.Helper()
	if wakeup.RunID.Int64 != runID || wakeup.WakeupKind != downstreamWakeupKind || wakeup.TargetAgentID != "agent-root" {
		t.Fatalf("root wakeup = %+v, want run_id=%d node_start agent-root", wakeup, runID)
	}
	if want := ManualDispatchIdempotencyKey("dag-1", "root-assigned", runID, "agent-root"); wakeup.IdempotencyKey != want {
		t.Fatalf("root wakeup idempotency_key = %q, want %q", wakeup.IdempotencyKey, want)
	}
}

func assertRootWakeupPayload(t *testing.T, wakeup sqlc.TaskDagWakeup) {
	t.Helper()
	var payload DownstreamWakeupPayload
	if err := json.Unmarshal(wakeup.PromptPayload, &payload); err != nil {
		t.Fatalf("root wakeup payload unmarshal error = %v", err)
	}
	if payload.AgentID != "agent-root" || len(payload.UpstreamOutputs) != 0 {
		t.Fatalf("root wakeup payload = %+v, want agent only", payload)
	}
}

func assertScheduleRootReplay(t *testing.T, db *fakeTaskDAGDB, rows int64) {
	t.Helper()
	assertScheduleRootRows(t, rows, 0, "ScheduleRootWakeups replay")
	assertPendingWakeupCount(t, db, "root-assigned", 1)
}

func setRuntimeNodeConfig(t *testing.T, db *fakeTaskDAGDB, runID int64, nodeKey string, config json.RawMessage) {
	t.Helper()
	key := dagRunNodeKey("dag-1", nodeKey, runID)
	row, ok := db.nodes[key]
	if !ok {
		t.Fatalf("node %s not found for run %d", nodeKey, runID)
	}
	row.Config = append(json.RawMessage(nil), config...)
	db.nodes[key] = row
}

func assertRunHasDispatchBlockedEvent(t *testing.T, db *fakeTaskDAGDB, runKey, nodeKey, reasonFragment string) {
	t.Helper()
	run, ok := db.runs[runKey]
	if !ok {
		t.Fatalf("run %s not found", runKey)
	}
	var events []struct {
		Kind    string `json:"kind"`
		NodeKey string `json:"node_key"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(run.Events, &events); err != nil {
		t.Fatalf("decode run events %q: %v", string(run.Events), err)
	}
	for _, ev := range events {
		if ev.Kind == "node_dispatch_blocked" &&
			ev.NodeKey == nodeKey &&
			strings.Contains(ev.Reason, reasonFragment) {
			return
		}
	}
	t.Fatalf("run %s events = %+v, want node_dispatch_blocked for %s containing %q", runKey, events, nodeKey, reasonFragment)
}
