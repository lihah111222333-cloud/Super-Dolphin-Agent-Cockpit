package heterociowner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func TestOwnerFinishReleasesCompletedRunAndReplays(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	plan, err := owner.PlanAndReserve(context.Background(), fence, operationID("a"), "run-finish", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	startAt := now.Add(time.Second)
	owner.now = func() time.Time { return startAt }
	started, err := owner.Start(context.Background(), plan.Fence, operationID("b"), "run-finish", "unit-a", durableRoutingSnapshot(manifest, fence.OwnerEpoch, startAt))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	finishAt := now.Add(2 * time.Second)
	owner.now = func() time.Time { return finishAt }
	finishID := operationID("c")
	finished, err := owner.Finish(context.Background(), started.Fence, finishID, "run-finish", "unit-a", heteroci.LeaseStateSucceeded)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if finished.Lease.State != heteroci.LeaseStateSucceeded || finished.Lease.FinishedAtUnixMilli != finishAt.UnixMilli() {
		t.Fatalf("finished lease = %#v", finished.Lease)
	}
	replay, err := owner.Finish(context.Background(), started.Fence, finishID, "run-finish", "unit-a", heteroci.LeaseStateSucceeded)
	if err != nil {
		t.Fatalf("Finish replay: %v", err)
	}
	if replay != finished {
		t.Fatalf("Finish replay drifted: got %#v want %#v", replay, finished)
	}
	assertOwnerRunReleased(t, owner, fence, "run-finish", true)
	assertOwnerReservationsReleased(t, owner, fence, "run-finish", true)
	if _, err := owner.Start(context.Background(), started.Fence, operationID("d"), "run-finish", "unit-a", durableRoutingSnapshot(manifest, fence.OwnerEpoch, finishAt)); err == nil {
		t.Fatal("Start accepted released run")
	}
}

func TestOwnerFinishRejectsPlannedLeaseWithoutMutation(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	plan, err := owner.PlanAndReserve(context.Background(), fence, operationID("e"), "run-invalid-finish", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	owner.now = func() time.Time { return now.Add(time.Second) }
	if _, err := owner.Finish(context.Background(), plan.Fence, operationID("f"), "run-invalid-finish", "unit-a", heteroci.LeaseStateFailed); err == nil {
		t.Fatal("Finish accepted planned lease")
	}
	assertOwnerLeaseState(t, owner, fence, "run-invalid-finish", "unit-a", heteroci.LeaseStatePlanned, plan.Fence.Revision)
	assertOwnerRunReleased(t, owner, fence, "run-invalid-finish", false)
}

func TestOwnerCancelPlannedReleasesReservation(t *testing.T) {
	owner, fence, manifest, snapshot, units, now := durableRoutingFixture(t)
	plan, err := owner.PlanAndReserve(context.Background(), fence, operationID("1"), "run-cancel", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	owner.now = func() time.Time { return now.Add(time.Second) }
	cancelID := operationID("2")
	cancelled, err := owner.CancelPlanned(context.Background(), plan.Fence, cancelID, "run-cancel", "unit-a")
	if err != nil {
		t.Fatalf("CancelPlanned: %v", err)
	}
	if cancelled.Lease.State != heteroci.LeaseStateCancelled || cancelled.Lease.StartedAtUnixMilli != 0 {
		t.Fatalf("cancelled lease = %#v", cancelled.Lease)
	}
	replay, err := owner.CancelPlanned(context.Background(), plan.Fence, cancelID, "run-cancel", "unit-a")
	if err != nil || replay != cancelled {
		t.Fatalf("CancelPlanned replay = %#v, %v", replay, err)
	}
	assertOwnerRunReleased(t, owner, fence, "run-cancel", true)
	assertOwnerReservationsReleased(t, owner, fence, "run-cancel", true)
}

func operationID(character string) heteroci.OperationID {
	return heteroci.OperationID(strings.Repeat(character, heteroci.OperationIDHexLength))
}

func assertOwnerRunReleased(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID string, want bool) {
	t.Helper()
	var released int
	err := owner.db.QueryRowContext(context.Background(), "SELECT released FROM heteroci_routing_runs WHERE scope = ? AND owner_epoch = ? AND run_id = ?", fence.Scope, fence.OwnerEpoch, runID).Scan(&released)
	if err != nil {
		t.Fatalf("read durable run release: %v", err)
	}
	if (released == 1) != want {
		t.Fatalf("run released = %d, want %t", released, want)
	}
}

func assertOwnerReservationsReleased(t *testing.T, owner *Owner, fence heteroci.OwnerFence, runID string, want bool) {
	t.Helper()
	var released int
	err := owner.db.QueryRowContext(context.Background(), "SELECT released FROM heteroci_routing_reservations WHERE scope = ? AND owner_epoch = ? AND run_id = ?", fence.Scope, fence.OwnerEpoch, runID).Scan(&released)
	if err != nil {
		t.Fatalf("read durable reservation release: %v", err)
	}
	if (released == 1) != want {
		t.Fatalf("reservation released = %d, want %t", released, want)
	}
}

var _ = errors.Is
