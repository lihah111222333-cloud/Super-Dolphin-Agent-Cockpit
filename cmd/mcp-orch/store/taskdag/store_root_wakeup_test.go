package taskdag

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
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

	wakeup := lookupPendingWakeup(t, db, "root-assigned")
	assertRootWakeupRoute(t, wakeup, runID)
	assertRootWakeupPayload(t, wakeup)

	rows, err = any(store).(RunStore).ScheduleRootWakeups(context.Background(), "dag-1", runID)
	requireNoScheduleRootError(t, err, "ScheduleRootWakeups replay")
	assertScheduleRootReplay(t, db, rows)
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
