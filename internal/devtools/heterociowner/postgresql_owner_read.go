package heterociowner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type postgresqlPlanProjection struct {
	Manifest heteroci.Manifest
	Units    []heteroci.ExecutionUnit
	Decision heteroci.RoutingDecision
	Result   heteroci.TopologyReservedPlan
}

type postgresqlReservationRow struct {
	Key      durableReservationKey
	Capacity heteroci.ResourceCapacity
	State    string
	Revision int64
}

// LocateRun 在一个 scope-locked SERIALIZABLE 事务中读取同一 run identity 的 durable 投影。
func (owner *PostgreSQLOwner) LocateRun(ctx context.Context, runID heteroci.TopologyRunID) ([]DurableRunState, error) {
	states, _, err := owner.readPostgreSQLRunStates(ctx, runID)
	if err != nil {
		return nil, err
	}
	return states, nil
}

// ReadRun 读取当前 owner epoch 的 durable 投影；历史 epoch 必须显式恢复。
func (owner *PostgreSQLOwner) ReadRun(ctx context.Context, runID heteroci.TopologyRunID) (DurableRunState, error) {
	states, current, err := owner.readPostgreSQLRunStates(ctx, runID)
	if err != nil {
		return DurableRunState{}, err
	}
	if len(states) == 0 {
		return DurableRunState{}, redactDurableReadError(errors.New("PostgreSQL routing run is missing"))
	}
	if len(states) != 1 {
		return DurableRunState{}, &RunIdentityAmbiguousError{}
	}
	state := states[0]
	if state.Fence.OwnerID != current.OwnerID || state.Fence.OwnerEpoch != current.OwnerEpoch {
		return DurableRunState{}, &RunRecoveryRequiredError{}
	}
	return state, nil
}

func (owner *PostgreSQLOwner) readPostgreSQLRunStates(ctx context.Context, runID heteroci.TopologyRunID) (states []DurableRunState, current heteroci.OwnerFence, readErr error) {
	nowUnixMilli, err := owner.preparePostgreSQLRead(runID)
	if err != nil {
		return nil, heteroci.OwnerFence{}, redactDurableReadError(err)
	}
	return owner.readPostgreSQLRunStatesTx(ctx, runID, time.UnixMilli(nowUnixMilli))
}

func (owner *PostgreSQLOwner) readPostgreSQLRunStatesTx(ctx context.Context, runID heteroci.TopologyRunID, now time.Time) (states []DurableRunState, current heteroci.OwnerFence, readErr error) {
	tx, err := owner.beginPostgreSQLOwnerTx(ctx)
	if err != nil {
		return nil, heteroci.OwnerFence{}, redactDurableReadError(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			readErr = errors.Join(readErr, redactDurableReadError(rollbackErr))
		}
	}()
	current, found, err := readPostgreSQLOwnerHead(ctx, tx, owner.scope, false)
	if err != nil {
		return nil, heteroci.OwnerFence{}, redactDurableReadError(err)
	}
	if !found {
		return nil, heteroci.OwnerFence{}, redactDurableReadError(errors.New("PostgreSQL owner head is missing"))
	}
	states, err = readPostgreSQLRunStateRows(ctx, tx, owner.scope, runID, now)
	if err != nil {
		return nil, heteroci.OwnerFence{}, redactDurableReadError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, heteroci.OwnerFence{}, redactDurableReadError(err)
	}
	// 保留 named return，以便 defer 合并 rollback 根因。
	return states, current, nil
}

// readPostgreSQLRunStateRows 读取单一 run identity 的全部 epoch 投影并拒绝歧义。
func readPostgreSQLRunStateRows(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, runID heteroci.TopologyRunID, now time.Time) ([]DurableRunState, error) {
	rows, err := readPostgreSQLRunRows(ctx, tx, scope, string(runID))
	if err != nil {
		return nil, err
	}
	if len(rows) > 1 {
		return nil, &RunIdentityAmbiguousError{}
	}
	states := make([]DurableRunState, 0, len(rows))
	for _, row := range rows {
		state, err := buildPostgreSQLRunState(ctx, tx, scope, row, now)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (owner *PostgreSQLOwner) preparePostgreSQLRead(runID heteroci.TopologyRunID) (int64, error) {
	if owner == nil || owner.db == nil {
		return 0, errors.New("heteroci PostgreSQL owner is nil")
	}
	if err := owner.scope.Validate(); err != nil {
		return 0, err
	}
	if err := runID.Validate(); err != nil {
		return 0, err
	}
	return owner.nowUnixMilli()
}

func buildPostgreSQLRunState(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, row postgresqlRunRow, now time.Time) (DurableRunState, error) {
	epoch, err := heteroci.NewRoutingOwnerEpoch(row.OwnerEpoch)
	if err != nil {
		return DurableRunState{}, err
	}
	projection, err := loadPostgreSQLRun(ctx, tx, scope, epoch, heteroci.TopologyRunID(row.RunID), now)
	if err != nil {
		return DurableRunState{}, err
	}
	released, err := validatePostgreSQLRunRelease(ctx, tx, projection.Fence, projection.RunID, projection.Leases, projection.PlanResult, projection.LastRevision)
	if err != nil {
		return DurableRunState{}, err
	}
	return DurableRunState{Fence: projection.Fence, RunID: projection.RunID, Manifest: projection.Manifest, Units: append([]heteroci.ExecutionUnit(nil), projection.Units...), Decision: projection.Decision, Leases: append([]heteroci.RoutingLease(nil), projection.Leases...), HeartbeatTTL: projection.HeartbeatTTL, Released: released, LastRevision: projection.LastRevision}, nil
}

func postgreSQLAllTerminal(leases []heteroci.RoutingLease) bool {
	for _, lease := range leases {
		if !validPostgreSQLTerminalState(lease.State) {
			return false
		}
	}
	return true
}

// readPostgreSQLReservationRows 读取 run 的全部 reservation 字段，禁止只用数据库 count 作为预期。

func readPostgreSQLReservationRows(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, fence heteroci.OwnerFence, runID string) ([]postgresqlReservationRow, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency, state, last_revision FROM heteroci_owner.routing_reservations WHERE scope = $1 AND owner_epoch = $2 AND run_id = $3 ORDER BY node_id, probe_identity, probe_boot_id", fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL routing reservation projection: %w", err)
	}
	defer rows.Close()
	result := make([]postgresqlReservationRow, 0)
	for rows.Next() {
		var row postgresqlReservationRow
		if err := rows.Scan(&row.Key.nodeID, &row.Key.probeIdentity, &row.Key.probeBootID, &row.Capacity.CPUMilli, &row.Capacity.MemoryBytes, &row.Capacity.MaxConcurrency, &row.State, &row.Revision); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL routing reservation projection: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL routing reservation projection: %w", err)
	}
	return result, nil
}

func readPostgreSQLRunRows(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, scope heteroci.RoutingOwnerScope, runID string) ([]postgresqlRunRow, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT owner_epoch, run_id, repository_id, source_tree_sha, plan_operation_id, heartbeat_ttl_milli, last_revision FROM heteroci_owner.routing_runs WHERE scope = $1 AND run_id = $2 ORDER BY owner_epoch", scope, runID)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL routing run rows: %w", err)
	}
	defer rows.Close()
	var result []postgresqlRunRow
	for rows.Next() {
		var row postgresqlRunRow
		if err := rows.Scan(&row.OwnerEpoch, &row.RunID, &row.RepositoryID, &row.SourceTreeSHA, &row.PlanOperationID, &row.HeartbeatTTLMs, &row.LastRevision); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL routing run row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL routing run rows: %w", err)
	}
	return result, nil
}

func validatePostgreSQLRunRelease(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, fence heteroci.OwnerFence, runID string, leases []heteroci.RoutingLease, plan heteroci.TopologyReservedPlan, lastRevision heteroci.RoutingOwnerRevision) (bool, error) {
	if len(leases) == 0 {
		return false, fmt.Errorf("PostgreSQL routing run has no leases")
	}
	expected, err := buildDurableReservations(plan.Plan, plan.EffectiveSnapshot)
	if err != nil {
		return false, fmt.Errorf("derive PostgreSQL reservation expectation from persisted plan: %w", err)
	}
	actual, err := readPostgreSQLReservationRows(ctx, queryer, fence, runID)
	if err != nil {
		return false, err
	}
	if err := validatePostgreSQLReservationProjection(actual, expected, leases, lastRevision); err != nil {
		return false, err
	}
	return postgreSQLAllTerminal(leases), nil
}

// validatePostgreSQLReservationProjection checks persisted reservation rows.
func validatePostgreSQLReservationProjection(actual []postgresqlReservationRow, expected []durableReservation, leases []heteroci.RoutingLease, lastRevision heteroci.RoutingOwnerRevision) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("PostgreSQL routing reservation projection count does not match persisted plan")
	}
	want := make(map[durableReservationKey]durableReservation, len(expected))
	for _, reservation := range expected {
		if _, exists := want[reservation.key]; exists {
			return fmt.Errorf("PostgreSQL persisted plan has duplicate reservation identity")
		}
		want[reservation.key] = reservation
	}
	expectedState := postgreSQLExpectedReservationState(leases)
	for _, row := range actual {
		reservation, exists := want[row.Key]
		if !exists {
			return fmt.Errorf("PostgreSQL routing reservation identity is not present in persisted plan")
		}
		if row.Capacity != reservation.capacity {
			return fmt.Errorf("PostgreSQL routing reservation capacity projection drifted")
		}
		if row.State != expectedState {
			return fmt.Errorf("PostgreSQL routing reservation state projection drifted")
		}
		if !validPostgreSQLReservationRevision(row.Revision, lastRevision) {
			return fmt.Errorf("PostgreSQL routing reservation revision projection drifted")
		}
		delete(want, row.Key)
	}
	if len(want) != 0 {
		return fmt.Errorf("PostgreSQL routing reservation projection is missing")
	}
	return nil
}

func validPostgreSQLReservationRevision(revision int64, lastRevision heteroci.RoutingOwnerRevision) bool {
	return revision > 0 && revision <= int64(lastRevision)
}

func projectPostgreSQLLease(leases []heteroci.RoutingLease, unitID string, nextLease heteroci.RoutingLease) ([]heteroci.RoutingLease, error) {
	projected := append([]heteroci.RoutingLease(nil), leases...)
	for index := range projected {
		if projected[index].UnitID == unitID {
			projected[index] = nextLease
			return projected, nil
		}
	}
	return nil, fmt.Errorf("PostgreSQL terminal lease projection is missing")
}

func validatePostgreSQLTerminalReplayKind(kind string, target heteroci.RoutingLeaseState) error {
	switch kind {
	case postgresqlFinishOperationKind:
		if target == heteroci.LeaseStateSucceeded || target == heteroci.LeaseStateFailed {
			return nil
		}
	case postgresqlCancelOperationKind:
		if target == heteroci.LeaseStateCancelled {
			return nil
		}
	}
	return ErrOperationConflict
}

func syncPostgreSQLStartedReservations(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, revision heteroci.RoutingOwnerRevision, plan heteroci.TopologyReservedPlan, leases []heteroci.RoutingLease, unitID string, startedLease heteroci.RoutingLease) error {
	expected, err := buildDurableReservations(plan.Plan, plan.EffectiveSnapshot)
	if err != nil {
		return fmt.Errorf("derive PostgreSQL reservation expectation before start: %w", err)
	}
	projected, err := projectPostgreSQLLease(leases, unitID, startedLease)
	if err != nil {
		return err
	}
	switch postgreSQLExpectedReservationState(leases) {
	case string(heteroci.LeaseStatePlanned):
		result, err := tx.ExecContext(ctx, "UPDATE heteroci_owner.routing_reservations SET state = 'started', last_revision = $1 WHERE scope = $2 AND owner_epoch = $3 AND run_id = $4 AND state = 'planned'", revision, fence.Scope, fence.OwnerEpoch, runID)
		if err != nil {
			return fmt.Errorf("update PostgreSQL started reservations: %w", err)
		}
		if err := requirePostgreSQLRows(result, len(expected), "update PostgreSQL started reservations"); err != nil {
			return err
		}
	case string(heteroci.LeaseStateStarted):
	default:
		return fmt.Errorf("PostgreSQL reservation state is invalid before start")
	}
	actual, err := readPostgreSQLReservationRows(ctx, tx, fence, runID)
	if err != nil {
		return err
	}
	if err := validatePostgreSQLReservationProjection(actual, expected, projected, revision); err != nil {
		return fmt.Errorf("validate PostgreSQL reservations after start: %w", err)
	}
	return nil
}

func postgreSQLExpectedReservationState(leases []heteroci.RoutingLease) string {
	allTerminal, hasStarted := true, false
	for _, lease := range leases {
		if !validPostgreSQLTerminalState(lease.State) {
			allTerminal = false
		}
		if postgreSQLLeaseConsumesStartedReservation(lease.State) {
			hasStarted = true
		}
	}
	if allTerminal {
		return "finished"
	}
	if hasStarted {
		return string(heteroci.LeaseStateStarted)
	}
	return string(heteroci.LeaseStatePlanned)
}

func postgreSQLLeaseConsumesStartedReservation(state heteroci.RoutingLeaseState) bool {
	switch state {
	case heteroci.LeaseStateStarted, heteroci.LeaseStateSucceeded, heteroci.LeaseStateFailed:
		return true
	default:
		return false
	}
}

func loadPostgreSQLRun(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch, runID heteroci.TopologyRunID, now time.Time) (postgresqlRunProjection, error) {
	row, err := readPostgreSQLRunRow(ctx, queryer, scope, epoch, runID)
	if err != nil {
		return postgresqlRunProjection{}, err
	}
	ownerID, err := loadPostgreSQLEpochOwner(ctx, queryer, scope, epoch)
	if err != nil {
		return postgresqlRunProjection{}, err
	}
	lastRevision, err := heteroci.NewRoutingOwnerRevision(row.LastRevision)
	if err != nil {
		return postgresqlRunProjection{}, err
	}
	fence, err := heteroci.NewOwnerFence(scope, ownerID, epoch, lastRevision)
	if err != nil {
		return postgresqlRunProjection{}, err
	}
	operationID, err := heteroci.ParseOperationID(row.PlanOperationID)
	if err != nil {
		return postgresqlRunProjection{}, fmt.Errorf("PostgreSQL routing plan operation is invalid")
	}
	plan, err := loadPostgreSQLPlanProjection(ctx, queryer, scope, epoch, row, ownerID, operationID)
	if err != nil {
		return postgresqlRunProjection{}, err
	}
	leases, err := loadPostgreSQLRunLeases(ctx, queryer, fence, string(runID), plan.Decision, now)
	if err != nil {
		return postgresqlRunProjection{}, err
	}
	return postgresqlRunProjection{Fence: fence, RunID: row.RunID, RepositoryID: row.RepositoryID, SourceTreeSHA: row.SourceTreeSHA, PlanOperation: operationID, Manifest: plan.Manifest, Units: plan.Units, Decision: plan.Decision, Leases: leases, HeartbeatTTL: time.Duration(row.HeartbeatTTLMs) * time.Millisecond, LastRevision: lastRevision, PlanResult: plan.Result}, nil
}

func readPostgreSQLRunRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch, runID heteroci.TopologyRunID) (postgresqlRunRow, error) {
	var row postgresqlRunRow
	err := queryer.QueryRowContext(ctx, "SELECT owner_epoch, run_id, repository_id, source_tree_sha, plan_operation_id, heartbeat_ttl_milli, last_revision FROM heteroci_owner.routing_runs WHERE scope = $1 AND owner_epoch = $2 AND run_id = $3", scope, epoch, runID).Scan(&row.OwnerEpoch, &row.RunID, &row.RepositoryID, &row.SourceTreeSHA, &row.PlanOperationID, &row.HeartbeatTTLMs, &row.LastRevision)
	if err != nil {
		return postgresqlRunRow{}, fmt.Errorf("read PostgreSQL routing run: %w", err)
	}
	if row.OwnerEpoch != int64(epoch) || row.RunID != string(runID) || row.HeartbeatTTLMs <= 0 {
		return postgresqlRunRow{}, fmt.Errorf("PostgreSQL routing run identity is invalid")
	}
	return row, nil
}

func loadPostgreSQLPlanProjection(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch, row postgresqlRunRow, ownerID heteroci.RoutingOwnerID, operationID heteroci.OperationID) (postgresqlPlanProjection, error) {
	record, found, err := readPostgreSQLOperation(ctx, queryer, scope, operationID)
	if err != nil {
		return postgresqlPlanProjection{}, err
	}
	if !found {
		return postgresqlPlanProjection{}, fmt.Errorf("PostgreSQL routing plan operation is missing")
	}
	var envelope postgresqlPlanEnvelope
	if err := json.Unmarshal(record.RequestBytes, &envelope); err != nil {
		return postgresqlPlanProjection{}, fmt.Errorf("decode PostgreSQL routing plan request: %w", err)
	}
	if err := validatePostgreSQLPlanBinding(envelope, scope, epoch, row, ownerID, operationID); err != nil {
		return postgresqlPlanProjection{}, err
	}
	result, err := replayPostgreSQLPlan(record, envelope.Session, envelope.Request, record.RequestBytes)
	if err != nil {
		return postgresqlPlanProjection{}, err
	}
	return postgresqlPlanProjection{Manifest: envelope.Request.Manifest, Units: append([]heteroci.ExecutionUnit(nil), envelope.Request.Units...), Decision: result.Decision, Result: result}, nil
}

func validatePostgreSQLPlanBinding(envelope postgresqlPlanEnvelope, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch, row postgresqlRunRow, ownerID heteroci.RoutingOwnerID, operationID heteroci.OperationID) error {
	if envelope.Session.Scope != scope || envelope.Session.OwnerEpoch != epoch || envelope.Session.OwnerID != ownerID || envelope.Request.OperationID != operationID || string(envelope.Request.RunIdentity.DurableRunID) != row.RunID || envelope.Request.RunIdentity.RepositoryID != row.RepositoryID || envelope.Request.RunIdentity.SourceTreeSHA != row.SourceTreeSHA {
		return fmt.Errorf("PostgreSQL routing plan identity drifted")
	}
	if err := envelope.Request.Validate(envelope.Session); err != nil {
		return fmt.Errorf("validate PostgreSQL routing plan request: %w", err)
	}
	return nil
}

func loadPostgreSQLEpochOwner(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch) (heteroci.RoutingOwnerID, error) {
	var ownerID string
	var startedAt int64
	if err := queryer.QueryRowContext(ctx, "SELECT owner_id, started_at_unix_milli FROM heteroci_owner.owner_epochs WHERE scope = $1 AND owner_epoch = $2", scope, epoch).Scan(&ownerID, &startedAt); err != nil {
		return "", fmt.Errorf("read PostgreSQL owner epoch: %w", err)
	}
	parsed, err := heteroci.ParseRoutingOwnerID(ownerID)
	if err != nil || startedAt <= 0 {
		return "", fmt.Errorf("PostgreSQL owner epoch identity is invalid")
	}
	return parsed, nil
}

func loadPostgreSQLLease(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, fence heteroci.OwnerFence, runID, unitID string, decision heteroci.RoutingDecision, now time.Time) (postgresqlLeaseProjection, error) {
	row, err := readPostgreSQLLeaseRow(ctx, queryer, fence, runID, unitID)
	if err != nil {
		return postgresqlLeaseProjection{}, err
	}
	base, err := decodePostgreSQLLeaseBase(row.LeaseBytes, runID, unitID, decision, now)
	if err != nil {
		return postgresqlLeaseProjection{}, err
	}
	lastRevision, err := heteroci.NewRoutingOwnerRevision(row.LastRevision)
	if err != nil {
		return postgresqlLeaseProjection{}, err
	}
	projection := postgresqlLeaseProjection{Base: base, State: row.State, LastRevision: lastRevision}
	if err := validatePostgreSQLLeaseProjection(&projection, row, decision, now); err != nil {
		return postgresqlLeaseProjection{}, err
	}
	return projection, nil
}

func readPostgreSQLLeaseRow(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, fence heteroci.OwnerFence, runID, unitID string) (postgresqlLeaseRow, error) {
	var row postgresqlLeaseRow
	if err := queryer.QueryRowContext(ctx, "SELECT state, lease_bytes, started_bytes, terminal_bytes, last_revision, started_at_unix_milli, finished_at_unix_milli FROM heteroci_owner.routing_leases WHERE scope = $1 AND owner_epoch = $2 AND run_id = $3 AND unit_id = $4", fence.Scope, fence.OwnerEpoch, runID, unitID).Scan(&row.State, &row.LeaseBytes, &row.StartedBytes, &row.TerminalBytes, &row.LastRevision, &row.StartedAt, &row.FinishedAt); err != nil {
		return postgresqlLeaseRow{}, fmt.Errorf("read PostgreSQL routing lease: %w", err)
	}
	return row, nil
}

func decodePostgreSQLLeaseBase(data []byte, runID, unitID string, decision heteroci.RoutingDecision, now time.Time) (heteroci.RoutingLease, error) {
	var base heteroci.RoutingLease
	if err := json.Unmarshal(data, &base); err != nil {
		return heteroci.RoutingLease{}, fmt.Errorf("decode PostgreSQL routing lease: %w", err)
	}
	if base.RunID != runID || base.UnitID != unitID || base.State != heteroci.LeaseStatePlanned {
		return heteroci.RoutingLease{}, fmt.Errorf("PostgreSQL routing lease base identity drifted")
	}
	if err := base.ValidateAgainst(decision, now); err != nil {
		return heteroci.RoutingLease{}, fmt.Errorf("validate PostgreSQL routing lease base: %w", err)
	}
	return base, nil
}

func validatePostgreSQLLeaseProjection(projection *postgresqlLeaseProjection, row postgresqlLeaseRow, decision heteroci.RoutingDecision, now time.Time) error {
	switch row.State {
	case string(heteroci.LeaseStatePlanned):
		return validatePostgreSQLPlannedLeaseProjection(row)
	case string(heteroci.LeaseStateStarted):
		return validatePostgreSQLStartedLeaseProjection(projection, row, decision, now)
	case "finished":
		return validatePostgreSQLFinishedLeaseProjection(projection, row, decision, now)
	default:
		return fmt.Errorf("PostgreSQL routing lease state is invalid")
	}
}

func validatePostgreSQLPlannedLeaseProjection(row postgresqlLeaseRow) error {
	if len(row.StartedBytes) != 0 || len(row.TerminalBytes) != 0 || row.StartedAt.Valid || row.FinishedAt.Valid {
		return fmt.Errorf("PostgreSQL planned lease projection drifted")
	}
	return nil
}

func validatePostgreSQLStartedLeaseProjection(projection *postgresqlLeaseProjection, row postgresqlLeaseRow, decision heteroci.RoutingDecision, now time.Time) error {
	if err := json.Unmarshal(row.StartedBytes, &projection.Started); err != nil {
		return fmt.Errorf("decode PostgreSQL started lease: %w", err)
	}
	if projection.Started.Lease.State != heteroci.LeaseStateStarted || !postgresqlSameLeaseIdentity(projection.Base, projection.Started.Lease) || !row.StartedAt.Valid || row.StartedAt.Int64 != projection.Started.Lease.StartedAtUnixMilli || row.FinishedAt.Valid {
		return fmt.Errorf("PostgreSQL started lease projection drifted")
	}
	if err := projection.Started.ValidateAgainst(decision, now); err != nil {
		return fmt.Errorf("validate PostgreSQL started lease: %w", err)
	}
	return nil
}

func validatePostgreSQLFinishedLeaseProjection(projection *postgresqlLeaseProjection, row postgresqlLeaseRow, decision heteroci.RoutingDecision, now time.Time) error {
	if err := json.Unmarshal(row.TerminalBytes, &projection.Terminal); err != nil {
		return fmt.Errorf("decode PostgreSQL terminal lease: %w", err)
	}
	if !validPostgreSQLTerminalState(projection.Terminal.State) || !postgresqlSameLeaseIdentity(projection.Base, projection.Terminal) || !row.FinishedAt.Valid || row.FinishedAt.Int64 != projection.Terminal.FinishedAtUnixMilli {
		return fmt.Errorf("PostgreSQL terminal lease projection drifted")
	}
	if projection.Terminal.State != heteroci.LeaseStateCancelled && !row.StartedAt.Valid {
		return fmt.Errorf("PostgreSQL terminal lease start timestamp is missing")
	}
	if err := projection.Terminal.ValidateAgainst(decision, now); err != nil {
		return fmt.Errorf("validate PostgreSQL terminal lease: %w", err)
	}
	return nil
}

func loadPostgreSQLRunLeases(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, fence heteroci.OwnerFence, runID string, decision heteroci.RoutingDecision, now time.Time) ([]heteroci.RoutingLease, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT unit_id FROM heteroci_owner.routing_leases WHERE scope = $1 AND owner_epoch = $2 AND run_id = $3 ORDER BY unit_id", fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL routing lease ids: %w", err)
	}
	defer rows.Close()
	var unitIDs []string
	for rows.Next() {
		var unitID string
		if err := rows.Scan(&unitID); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL routing lease id: %w", err)
		}
		unitIDs = append(unitIDs, unitID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL routing lease ids: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close PostgreSQL routing lease ids: %w", err)
	}
	var leases []heteroci.RoutingLease
	for _, unitID := range unitIDs {
		projection, err := loadPostgreSQLLease(ctx, queryer, fence, runID, unitID, decision, now)
		if err != nil {
			return nil, err
		}
		lease := postgresqlLeaseFromProjection(projection)
		leases = append(leases, lease)
	}
	sort.Slice(leases, func(left, right int) bool { return leases[left].UnitID < leases[right].UnitID })
	if len(leases) != len(decision.Assignments) {
		return nil, fmt.Errorf("PostgreSQL routing lease coverage is incomplete")
	}
	return leases, nil
}

func validPostgreSQLTerminalState(state heteroci.RoutingLeaseState) bool {
	return state == heteroci.LeaseStateSucceeded || state == heteroci.LeaseStateFailed || state == heteroci.LeaseStateCancelled
}

func postgresqlLeaseFromProjection(projection postgresqlLeaseProjection) heteroci.RoutingLease {
	switch projection.State {
	case string(heteroci.LeaseStateStarted):
		return projection.Started.Lease
	case "finished":
		return projection.Terminal
	default:
		return projection.Base
	}
}

type postgresqlLeaseIdentity struct {
	SchemaVersion  string
	Authority      string
	RunID          string
	DecisionDigest string
	UnitID         string
	Executor       heteroci.ExecutorKind
	NodeID         string
	Endpoint       string
	ProbeIdentity  string
	ProbeBootID    string
	ReservedAt     int64
}

func postgresqlSameLeaseIdentity(left, right heteroci.RoutingLease) bool {
	return postgresqlLeaseIdentityOf(left) == postgresqlLeaseIdentityOf(right)
}

func postgresqlLeaseIdentityOf(lease heteroci.RoutingLease) postgresqlLeaseIdentity {
	return postgresqlLeaseIdentity{SchemaVersion: lease.SchemaVersion, Authority: lease.Authority, RunID: lease.RunID, DecisionDigest: lease.DecisionDigest, UnitID: lease.UnitID, Executor: lease.Executor, NodeID: lease.NodeID, Endpoint: lease.Endpoint, ProbeIdentity: lease.ProbeIdentity, ProbeBootID: lease.ProbeBootID, ReservedAt: lease.ReservedAtUnixMilli}
}
