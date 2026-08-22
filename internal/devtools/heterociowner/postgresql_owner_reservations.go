package heterociowner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

func persistPostgreSQLTerminal(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, unitID string, leaseRevision, runRevision, nextRevision heteroci.RoutingOwnerRevision, lease heteroci.RoutingLease, complete bool, plan heteroci.TopologyReservedPlan, leases []heteroci.RoutingLease) error {
	terminalBytes, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("encode PostgreSQL terminal lease: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_leases SET state = 'finished', terminal_bytes = $1, last_revision = $2, finished_at_unix_milli = $3 WHERE scope = $4 AND owner_epoch = $5 AND run_id = $6 AND unit_id = $7 AND state = $8 AND last_revision = $9", terminalBytes, nextRevision, lease.FinishedAtUnixMilli, fence.Scope, fence.OwnerEpoch, runID, unitID, string(leaseStateSource(lease)), leaseRevision)
	if err != nil {
		return fmt.Errorf("update PostgreSQL terminal lease: %w", err)
	}
	if err := requireOnePostgreSQLRow(result, "update PostgreSQL terminal lease"); err != nil {
		return err
	}
	if err := updatePostgreSQLRunRevision(ctx, tx, fence, runID, runRevision, nextRevision); err != nil {
		return err
	}
	if complete {
		if err := releasePostgreSQLReservations(ctx, tx, fence, runID, nextRevision, runRevision, plan, leases, unitID, lease); err != nil {
			return err
		}
	}
	return nil
}

func releasePostgreSQLReservations(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, revision, previousRevision heteroci.RoutingOwnerRevision, plan heteroci.TopologyReservedPlan, leases []heteroci.RoutingLease, unitID string, nextLease heteroci.RoutingLease) error {
	expected, err := buildDurableReservations(plan.Plan, plan.EffectiveSnapshot)
	if err != nil {
		return fmt.Errorf("derive PostgreSQL reservation expectation before release: %w", err)
	}
	actual, err := readPostgreSQLReservationRows(ctx, tx, fence, runID)
	if err != nil {
		return err
	}
	if err := validatePostgreSQLReservationProjection(actual, expected, leases, previousRevision); err != nil {
		return fmt.Errorf("validate PostgreSQL reservations before release: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_reservations SET state = 'finished', last_revision = $1 WHERE scope = $2 AND owner_epoch = $3 AND run_id = $4 AND state <> 'finished'", revision, fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return fmt.Errorf("finish PostgreSQL reservations: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read PostgreSQL reservation release rows: %w", err)
	}
	if rows != int64(len(expected)) {
		return fmt.Errorf("PostgreSQL reservation release affected %d rows, expected %d", rows, len(expected))
	}
	projectedLeases, err := projectPostgreSQLLease(leases, unitID, nextLease)
	if err != nil {
		return err
	}
	actual, err = readPostgreSQLReservationRows(ctx, tx, fence, runID)
	if err != nil {
		return err
	}
	if err := validatePostgreSQLReservationProjection(actual, expected, projectedLeases, revision); err != nil {
		return fmt.Errorf("validate PostgreSQL reservations after release: %w", err)
	}
	return nil
}

func leaseStateSource(lease heteroci.RoutingLease) heteroci.RoutingLeaseState {
	if lease.State == heteroci.LeaseStateSucceeded || lease.State == heteroci.LeaseStateFailed {
		return heteroci.LeaseStateStarted
	}
	return heteroci.LeaseStatePlanned
}
