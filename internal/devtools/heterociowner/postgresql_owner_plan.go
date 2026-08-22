package heterociowner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const postgresqlPlanAndReserveOperationKind = "topology_plan_and_reserve/v1"

var errPostgreSQLOwnerCommitNotRecorded = errors.New("heteroci PostgreSQL owner commit was not recorded")

type postgresqlPlanEnvelope struct {
	Session heteroci.TopologySession
	Request heteroci.TopologyPlanRequest
}

type postgresqlOperationRecord struct {
	OperationRecord
	OwnerID heteroci.RoutingOwnerID
}

// PlanAndReserve 在同一 scope-locked SERIALIZABLE transaction 内重算 domain plan 并持久化 journal/projection。
func (owner *PostgreSQLOwner) PlanAndReserve(ctx context.Context, session heteroci.TopologySession, request heteroci.TopologyPlanRequest) (heteroci.TopologyReservedPlan, error) {
	requestBytes, nowUnixMilli, err := owner.preparePostgreSQLPlan(session, request)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	for range postgresqlOwnerMutationAttempts {
		result, err := owner.planAndReserveOnce(ctx, session, request, requestBytes, nowUnixMilli)
		if err == nil {
			return result, nil
		}
		if !isRetryablePostgreSQLError(err) && !errors.Is(err, errPostgreSQLOwnerCommitNotRecorded) {
			return heteroci.TopologyReservedPlan{}, err
		}
	}
	return heteroci.TopologyReservedPlan{}, fmt.Errorf("plan and reserve PostgreSQL topology after retries")
}

func (owner *PostgreSQLOwner) preparePostgreSQLPlan(session heteroci.TopologySession, request heteroci.TopologyPlanRequest) ([]byte, int64, error) {
	if owner == nil || owner.db == nil || session.Scope != owner.scope {
		return nil, 0, fmt.Errorf("heteroci PostgreSQL owner session scope is invalid")
	}
	if err := request.Validate(session); err != nil {
		return nil, 0, err
	}
	requestBytes, err := json.Marshal(postgresqlPlanEnvelope{Session: session, Request: request})
	if err != nil {
		return nil, 0, fmt.Errorf("encode PostgreSQL topology plan request: %w", err)
	}
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return nil, 0, err
	}
	return requestBytes, nowUnixMilli, nil
}

func (owner *PostgreSQLOwner) planAndReserveOnce(ctx context.Context, session heteroci.TopologySession, request heteroci.TopologyPlanRequest, requestBytes []byte, nowUnixMilli int64) (heteroci.TopologyReservedPlan, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	result, err := executePostgreSQLPlanTransaction(ctx, tx, session, request, requestBytes, nowUnixMilli)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, rollbackPostgreSQLOwnerTx(tx, err)
	}
	return owner.commitPostgreSQLPlan(ctx, tx, session, request, requestBytes, result)
}

func executePostgreSQLPlanTransaction(ctx context.Context, tx *sql.Tx, session heteroci.TopologySession, request heteroci.TopologyPlanRequest, requestBytes []byte, nowUnixMilli int64) (heteroci.TopologyReservedPlan, error) {
	if record, found, err := readPostgreSQLOperation(ctx, tx, session.Scope, request.OperationID); err != nil {
		return heteroci.TopologyReservedPlan{}, err
	} else if found {
		return replayPostgreSQLPlan(record, session, request, requestBytes)
	}
	current, found, err := readPostgreSQLOwnerHead(ctx, tx, session.Scope, true)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("heteroci PostgreSQL owner has not been acquired")
		}
		return heteroci.TopologyReservedPlan{}, err
	}
	if err := session.ValidateMutationFence(current); err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	result, reservations, err := planPostgreSQLTopology(ctx, tx, current, request, time.UnixMilli(nowUnixMilli))
	if err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, fmt.Errorf("encode PostgreSQL topology plan result: %w", err)
	}
	if err := persistPostgreSQLPlan(ctx, tx, current, request, requestBytes, result, resultBytes, reservations, nowUnixMilli); err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	return result, nil
}

func (owner *PostgreSQLOwner) commitPostgreSQLPlan(ctx context.Context, tx *sql.Tx, session heteroci.TopologySession, request heteroci.TopologyPlanRequest, requestBytes []byte, result heteroci.TopologyReservedPlan) (heteroci.TopologyReservedPlan, error) {
	if err := tx.Commit(); err == nil {
		return result, nil
	} else if isRetryablePostgreSQLError(err) {
		return heteroci.TopologyReservedPlan{}, err
	} else {
		replayed, found, replayErr := owner.reconcilePostgreSQLPlan(ctx, session, request, requestBytes)
		if replayErr != nil {
			return heteroci.TopologyReservedPlan{}, errors.Join(ErrPostgreSQLOwnerCommitUncertain, err, replayErr)
		}
		if found {
			return replayed, nil
		}
		return heteroci.TopologyReservedPlan{}, errPostgreSQLOwnerCommitNotRecorded
	}
}

func planPostgreSQLTopology(ctx context.Context, tx *sql.Tx, current heteroci.OwnerFence, request heteroci.TopologyPlanRequest, now time.Time) (heteroci.TopologyReservedPlan, []durableReservation, error) {
	if err := request.SourceSnapshot.ValidateAgainst(request.Manifest, now, request.HeartbeatTTL); err != nil {
		return heteroci.TopologyReservedPlan{}, nil, fmt.Errorf("validate PostgreSQL topology source snapshot: %w", err)
	}
	reserved, err := readPostgreSQLReservations(ctx, tx, current)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, nil, err
	}
	planned, reservations, err := planFromReservations(current, string(request.RunIdentity.DurableRunID), request.Manifest, request.SourceSnapshot, request.Units, request.HeartbeatTTL, now, reserved)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, nil, err
	}
	revision, err := nextOwnerRevision(current)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, nil, err
	}
	fence, err := heteroci.NewOwnerFence(current.Scope, current.OwnerID, current.OwnerEpoch, revision)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, nil, err
	}
	result := heteroci.TopologyReservedPlan{Fence: fence, OperationID: request.OperationID, SourceSnapshot: planned.SourceSnapshot, EffectiveSnapshot: planned.EffectiveSnapshot, Plan: planned.Plan, Decision: planned.Decision, Leases: planned.Leases}
	return result, reservations, nil
}

func readPostgreSQLReservations(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence) (map[durableReservationKey]heteroci.ResourceCapacity, error) {
	rows, err := tx.QueryContext(ctx, "SELECT node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency FROM heteroci_owner.routing_reservations WHERE scope = $1 AND owner_epoch = $2 AND state IN ('planned', 'started')", fence.Scope, fence.OwnerEpoch)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL topology reservations: %w", err)
	}
	defer rows.Close()
	result := make(map[durableReservationKey]heteroci.ResourceCapacity)
	for rows.Next() {
		var key durableReservationKey
		var capacity heteroci.ResourceCapacity
		if err := rows.Scan(&key.nodeID, &key.probeIdentity, &key.probeBootID, &capacity.CPUMilli, &capacity.MemoryBytes, &capacity.MaxConcurrency); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL topology reservation: %w", err)
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate PostgreSQL topology reservation")
		}
		result[key] = capacity
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL topology reservations: %w", err)
	}
	return result, nil
}

func persistPostgreSQLPlan(ctx context.Context, tx *sql.Tx, current heteroci.OwnerFence, request heteroci.TopologyPlanRequest, requestBytes []byte, result heteroci.TopologyReservedPlan, resultBytes []byte, reservations []durableReservation, nowUnixMilli int64) error {
	previousJournal, err := readPostgreSQLJournalHead(ctx, tx, current.Scope)
	if err != nil {
		return err
	}
	requestDigest, resultDigest := digestBytes(requestBytes), digestBytes(resultBytes)
	operation := OperationRequest{Fence: current, OperationID: request.OperationID, Kind: postgresqlPlanAndReserveOperationKind, RequestBytes: requestBytes, ResultBytes: resultBytes, ProjectionDigest: resultDigest, CommittedAtUnixMilli: nowUnixMilli}
	journalDigest := journalChainDigest(current, operation, previousJournal, requestDigest, resultDigest, result.Fence.Revision)
	_, err = tx.ExecContext(ctx, "INSERT INTO heteroci_owner.operation_journal (scope, operation_id, owner_id, owner_epoch, revision, kind, request_bytes, request_digest, result_bytes, result_digest, previous_journal_digest, journal_digest, projection_digest, committed_at_unix_milli) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)", current.Scope, request.OperationID, current.OwnerID, current.OwnerEpoch, result.Fence.Revision, operation.Kind, requestBytes, requestDigest, resultBytes, resultDigest, previousJournal, journalDigest, resultDigest, nowUnixMilli)
	if err != nil {
		return fmt.Errorf("insert PostgreSQL topology operation: %w", err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO heteroci_owner.routing_runs (scope, owner_epoch, run_id, repository_id, source_tree_sha, plan_operation_id, heartbeat_ttl_milli, last_revision, created_at_unix_milli) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)", current.Scope, current.OwnerEpoch, request.RunIdentity.DurableRunID, request.RunIdentity.RepositoryID, request.RunIdentity.SourceTreeSHA, request.OperationID, request.HeartbeatTTL.Milliseconds(), result.Fence.Revision, nowUnixMilli)
	if err != nil {
		return fmt.Errorf("insert PostgreSQL topology run: %w", err)
	}
	for _, reservation := range reservations {
		if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner.routing_reservations (scope, owner_epoch, run_id, node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency, state, last_revision) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)", current.Scope, current.OwnerEpoch, request.RunIdentity.DurableRunID, reservation.key.nodeID, reservation.key.probeIdentity, reservation.key.probeBootID, reservation.capacity.CPUMilli, reservation.capacity.MemoryBytes, reservation.capacity.MaxConcurrency, heteroci.LeaseStatePlanned, result.Fence.Revision); err != nil {
			return fmt.Errorf("insert PostgreSQL topology reservation: %w", err)
		}
	}
	for _, lease := range result.Leases {
		leaseBytes, err := json.Marshal(lease)
		if err != nil {
			return fmt.Errorf("encode PostgreSQL topology lease: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO heteroci_owner.routing_leases (scope, owner_epoch, run_id, unit_id, state, lease_bytes, last_revision) VALUES ($1, $2, $3, $4, $5, $6, $7)", current.Scope, current.OwnerEpoch, request.RunIdentity.DurableRunID, lease.UnitID, heteroci.LeaseStatePlanned, leaseBytes, result.Fence.Revision); err != nil {
			return fmt.Errorf("insert PostgreSQL topology lease: %w", err)
		}
	}
	updated, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.owner_heads SET revision = $1, journal_digest = $2, projection_digest = $3, updated_at_unix_milli = $4 WHERE scope = $5 AND owner_id = $6 AND owner_epoch = $7 AND revision = $8", result.Fence.Revision, journalDigest, resultDigest, nowUnixMilli, current.Scope, current.OwnerID, current.OwnerEpoch, current.Revision)
	if err != nil {
		return fmt.Errorf("advance PostgreSQL topology owner head: %w", err)
	}
	return requireOnePostgreSQLRow(updated, "advance PostgreSQL topology owner head")
}

func readPostgreSQLJournalHead(ctx context.Context, queryer postgresqlOwnerQueryRower, scope heteroci.RoutingOwnerScope) (string, error) {
	var digest string
	if err := queryer.QueryRowContext(ctx, "SELECT journal_digest FROM heteroci_owner.owner_heads WHERE scope = $1", scope).Scan(&digest); err != nil {
		return "", fmt.Errorf("read PostgreSQL topology journal head: %w", err)
	}
	if !validOwnerDigest(digest) {
		return "", fmt.Errorf("PostgreSQL topology journal head is invalid")
	}
	return digest, nil
}

func readPostgreSQLOperation(ctx context.Context, queryer postgresqlOwnerQueryRower, scope heteroci.RoutingOwnerScope, operationID heteroci.OperationID) (postgresqlOperationRecord, bool, error) {
	var row postgresqlOperationRecord
	row.Scope, row.OperationID = scope, operationID
	err := queryer.QueryRowContext(ctx, "SELECT owner_id, owner_epoch, revision, kind, request_bytes, request_digest, result_bytes, result_digest, previous_journal_digest, journal_digest, projection_digest, committed_at_unix_milli FROM heteroci_owner.operation_journal WHERE scope = $1 AND operation_id = $2", scope, operationID).Scan(&row.OwnerID, &row.OwnerEpoch, &row.Revision, &row.Kind, &row.RequestBytes, &row.RequestDigest, &row.ResultBytes, &row.ResultDigest, &row.PreviousJournal, &row.JournalDigest, &row.ProjectionDigest, &row.CommittedAtUnixMilli)
	if errors.Is(err, sql.ErrNoRows) {
		return postgresqlOperationRecord{}, false, nil
	}
	if err != nil {
		return postgresqlOperationRecord{}, false, fmt.Errorf("read PostgreSQL topology operation: %w", err)
	}
	if err := validateOperationRecordIntegrity(row.OperationRecord, row.RequestDigest, row.ResultDigest); err != nil {
		return postgresqlOperationRecord{}, false, err
	}
	return row, true, nil
}

func replayPostgreSQLPlan(record postgresqlOperationRecord, session heteroci.TopologySession, request heteroci.TopologyPlanRequest, requestBytes []byte) (heteroci.TopologyReservedPlan, error) {
	if record.Kind != postgresqlPlanAndReserveOperationKind || !bytes.Equal(record.RequestBytes, requestBytes) {
		return heteroci.TopologyReservedPlan{}, ErrOperationConflict
	}
	if record.OwnerID != session.OwnerID || record.OwnerEpoch != session.OwnerEpoch || record.Revision <= 1 {
		return heteroci.TopologyReservedPlan{}, fmt.Errorf("PostgreSQL topology operation owner identity drifted: owner_match=%t epoch_match=%t revision_valid=%t", record.OwnerID == session.OwnerID, record.OwnerEpoch == session.OwnerEpoch, record.Revision > 1)
	}
	previousFence, err := heteroci.NewOwnerFence(session.Scope, session.OwnerID, session.OwnerEpoch, record.Revision-1)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	operation := OperationRequest{Fence: previousFence, OperationID: request.OperationID, Kind: record.Kind, RequestBytes: record.RequestBytes, ResultBytes: record.ResultBytes, ProjectionDigest: record.ProjectionDigest, CommittedAtUnixMilli: record.CommittedAtUnixMilli}
	if journalChainDigest(previousFence, operation, record.PreviousJournal, record.RequestDigest, record.ResultDigest, record.Revision) != record.JournalDigest {
		return heteroci.TopologyReservedPlan{}, fmt.Errorf("PostgreSQL topology operation journal digest mismatch")
	}
	var result heteroci.TopologyReservedPlan
	if err := json.Unmarshal(record.ResultBytes, &result); err != nil {
		return heteroci.TopologyReservedPlan{}, fmt.Errorf("decode PostgreSQL topology plan result: %w", err)
	}
	durable := DurableReservedRoutingPlan{Fence: result.Fence, OperationID: result.OperationID, SourceSnapshot: result.SourceSnapshot, EffectiveSnapshot: result.EffectiveSnapshot, Plan: result.Plan, Decision: result.Decision, Leases: result.Leases}
	if err := validateDurablePlanResult(durable, previousFence, request.OperationID, request.Manifest, request.Units, request.HeartbeatTTL, time.UnixMilli(record.CommittedAtUnixMilli)); err != nil {
		return heteroci.TopologyReservedPlan{}, err
	}
	return result, nil
}

func (owner *PostgreSQLOwner) reconcilePostgreSQLPlan(ctx context.Context, session heteroci.TopologySession, request heteroci.TopologyPlanRequest, requestBytes []byte) (heteroci.TopologyReservedPlan, bool, error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, false, err
	}
	record, found, err := readPostgreSQLOperation(ctx, tx, owner.scope, request.OperationID)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, false, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return heteroci.TopologyReservedPlan{}, false, err
		}
		return heteroci.TopologyReservedPlan{}, false, nil
	}
	result, err := replayPostgreSQLPlan(record, session, request, requestBytes)
	if err != nil {
		return heteroci.TopologyReservedPlan{}, false, rollbackPostgreSQLOwnerTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return heteroci.TopologyReservedPlan{}, false, err
	}
	return result, true, nil
}
