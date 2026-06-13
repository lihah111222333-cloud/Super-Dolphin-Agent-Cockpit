//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"strings"
	"testing"
)

func TestEnqueueWakeupRejectsMissingRunID(t *testing.T) {
	t.Parallel()

	store := &store{}
	_, err := store.EnqueueWakeup(context.Background(), EnqueueWakeupInput{
		DagKey:         "dag-1",
		NodeKey:        "n1",
		WakeupKind:     "node_ready",
		TargetAgentID:  "agent-a",
		PromptPayload:  []byte(`{}`),
		IdempotencyKey: "dag-1:n1",
	})
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("EnqueueWakeup err = %v, want run_id required", err)
	}
}

func TestClaimDueWakeupsSkipsFinalizedRunWakeup(t *testing.T) {
	t.Parallel()

	store, db, now := newTaskDAGTestStore()
	run := db.runs["run-1"]
	run.Status = "failed"
	db.runs["run-1"] = run
	db.wakeups[7] = newPendingWakeup(now, 7)

	claimed, err := store.ClaimDueWakeups(context.Background(), ClaimDueWakeupsInput{
		ClaimedBy:     "worker-a",
		LeaseInterval: testLeaseInterval,
		Limit:         1,
	})
	if err != nil {
		t.Fatalf("ClaimDueWakeups err = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed wakeups = %+v, want none for finalized run", claimed)
	}
}
