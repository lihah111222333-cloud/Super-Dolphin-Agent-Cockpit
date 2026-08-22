package heterociowner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const startOperationKind = "start"

type startOperationRequest struct {
	Fence           heteroci.OwnerFence    `json:"fence"`
	RunID           heteroci.TopologyRunID `json:"run_id"`
	UnitID          string                 `json:"unit_id"`
	CurrentSnapshot heteroci.SnapshotSet   `json:"current_snapshot"`
}

type durableRunRecord struct {
	manifest     heteroci.Manifest
	units        []heteroci.ExecutionUnit
	decision     heteroci.RoutingDecision
	heartbeatTTL time.Duration
	lastRevision heteroci.RoutingOwnerRevision
	released     bool
}

type durableRunColumns struct {
	manifestBytes  []byte
	manifestDigest string
	unitsBytes     []byte
	unitsDigest    string
	decisionBytes  []byte
	decisionDigest string
	ttlMillis      int64
	lastRevision   int64
	released       int
}

// Start 在同一 immediate transaction 内校验当前快照并把 planned lease CAS 为 started。
// 返回值包含 launcher 必须传给 daemon 的 owner_epoch/run_id/unit_id/decision_digest occupancy。
func (owner *Owner) Start(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, currentSnapshot heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error) {
	if err := validateStartArguments(owner, fence, operationID, runID, unitID, currentSnapshot); err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	runIDText := string(runID)
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	requestBytes, err := json.Marshal(startOperationRequest{Fence: fence, RunID: runID, UnitID: unitID, CurrentSnapshot: currentSnapshot})
	if err != nil {
		return heteroci.StartedRoutingLease{}, fmt.Errorf("encode durable start request: %w", err)
	}
	var started heteroci.StartedRoutingLease
	err = WithImmediateTx(ctx, owner.db, func(tx *sql.Tx) error {
		return owner.startTx(ctx, tx, fence, operationID, runIDText, unitID, currentSnapshot, time.UnixMilli(nowUnixMilli), nowUnixMilli, requestBytes, &started)
	})
	if err != nil {
		return heteroci.StartedRoutingLease{}, err
	}
	return started, nil
}

// startTx 执行 immutable replay 或一次新的 lease state/journal CAS。
func (owner *Owner) startTx(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, currentSnapshot heteroci.SnapshotSet, now time.Time, nowUnixMilli int64, requestBytes []byte, started *heteroci.StartedRoutingLease) error {
	existing, found, err := readOperationByID(ctx, tx, fence.Scope, fence.OwnerEpoch, operationID)
	if err != nil {
		return err
	}
	run, err := loadDurableRun(ctx, tx, fence, runID)
	if err != nil {
		return err
	}
	if found {
		return replayStartOperation(existing, requestBytes, fence, run, now, started)
	}
	return owner.startNewTransition(ctx, tx, fence, operationID, runID, unitID, currentSnapshot, run, now, nowUnixMilli, requestBytes, started)
}

// startNewTransition 校验当前 fence 并持久化新的 started projection。
func (owner *Owner) startNewTransition(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID, unitID string, currentSnapshot heteroci.SnapshotSet, run durableRunRecord, now time.Time, nowUnixMilli int64, requestBytes []byte, started *heteroci.StartedRoutingLease) error {
	if run.released {
		return fmt.Errorf("durable routing run is released")
	}
	if err := owner.requireCurrentFence(ctx, tx, fence); err != nil {
		return err
	}
	lease, leaseRevision, nextStarted, err := loadAndBuildStarted(ctx, tx, fence, runID, unitID, currentSnapshot, run, now)
	if err != nil {
		return err
	}
	resultBytes, err := json.Marshal(nextStarted)
	if err != nil {
		return fmt.Errorf("encode durable start result: %w", err)
	}
	if err := persistStartedLease(ctx, tx, fence, runID, unitID, leaseRevision, run.lastRevision, nextStarted.Fence.Revision, lease); err != nil {
		return err
	}
	_, err = commitOperationTx(ctx, tx, OperationRequest{Fence: fence, OperationID: operationID, Kind: startOperationKind, RequestBytes: requestBytes, ResultBytes: resultBytes, ProjectionDigest: digestBytes(resultBytes), CommittedAtUnixMilli: nowUnixMilli})
	if err != nil {
		return err
	}
	*started = nextStarted
	return nil
}

// requireCurrentFence 拒绝任何不是 SQLite current head 的 mutation fence。
func (owner *Owner) requireCurrentFence(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence) error {
	current, headFound, err := readOwnerHead(ctx, tx, owner.scope)
	if err != nil {
		return err
	}
	if !headFound || current != fence {
		return fmt.Errorf("heteroci owner fence is stale")
	}
	return nil
}

// loadAndBuildStarted 读取 planned lease、校验绑定并构造 next started 值。
func loadAndBuildStarted(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, unitID string, currentSnapshot heteroci.SnapshotSet, run durableRunRecord, now time.Time) (heteroci.RoutingLease, heteroci.RoutingOwnerRevision, heteroci.StartedRoutingLease, error) {
	lease, leaseRevision, err := loadDurableLease(ctx, tx, fence, runID, unitID)
	if err != nil {
		return heteroci.RoutingLease{}, 0, heteroci.StartedRoutingLease{}, err
	}
	if err := validateDurableStartBinding(run, lease, currentSnapshot, unitID, fence, now); err != nil {
		return heteroci.RoutingLease{}, 0, heteroci.StartedRoutingLease{}, err
	}
	nextFence, nextLease, err := buildStartedTransition(fence, lease, run.decision, now)
	if err != nil {
		return heteroci.RoutingLease{}, 0, heteroci.StartedRoutingLease{}, err
	}
	nextStarted, err := heteroci.NewStartedRoutingLease(nextFence, nextLease, run.decision, now)
	if err != nil {
		return heteroci.RoutingLease{}, 0, heteroci.StartedRoutingLease{}, err
	}
	return nextLease, leaseRevision, nextStarted, nil
}

// validateStartArguments 校验 durable Start 的 owner/fence/operation/run/unit/snapshot 身份。
func validateStartArguments(owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet) error {
	if err := runID.Validate(); err != nil {
		return err
	}
	if err := validatePlanOwnerBinding(owner, fence, operationID, string(runID)); err != nil {
		return err
	}
	if !validOwnerRunID(unitID) {
		return fmt.Errorf("heteroci owner unit id is invalid")
	}
	if snapshot.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("heteroci current snapshot owner epoch does not match fence")
	}
	return nil
}

// replayStartOperation 只对 exact request 返回 journal 中不可变的 started projection。
func replayStartOperation(existing OperationRecord, requestBytes []byte, fence heteroci.OwnerFence, run durableRunRecord, now time.Time, started *heteroci.StartedRoutingLease) error {
	if existing.Kind != startOperationKind || !bytes.Equal(existing.RequestBytes, requestBytes) {
		return ErrOperationConflict
	}
	if err := json.Unmarshal(existing.ResultBytes, started); err != nil {
		return fmt.Errorf("decode durable start replay: %w", err)
	}
	wantFence, err := heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, existing.Revision)
	if err != nil || started.Fence != wantFence {
		return fmt.Errorf("durable start replay fence drifted")
	}
	if err := started.ValidateAgainst(run.decision, now); err != nil {
		return fmt.Errorf("validate durable start replay: %w", err)
	}
	return nil
}

// loadDurableRun 严格读取并校验当前 epoch 的 manifest、units 和 decision projection。
func loadDurableRun(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string) (durableRunRecord, error) {
	var columns durableRunColumns
	row := tx.QueryRowContext(ctx, "SELECT manifest_json, manifest_digest, units_json, units_digest, heartbeat_ttl_millis, decision_json, decision_digest, released, last_revision FROM heteroci_routing_runs WHERE scope = ? AND owner_epoch = ? AND run_id = ?", fence.Scope, fence.OwnerEpoch, runID)
	if err := row.Scan(&columns.manifestBytes, &columns.manifestDigest, &columns.unitsBytes, &columns.unitsDigest, &columns.ttlMillis, &columns.decisionBytes, &columns.decisionDigest, &columns.released, &columns.lastRevision); err != nil {
		return durableRunRecord{}, fmt.Errorf("read durable routing run: %w", err)
	}
	return decodeAndValidateDurableRun(columns)
}

// decodeAndValidateDurableRun 校验列摘要、revision、JSON 和 decision digest。
func decodeAndValidateDurableRun(columns durableRunColumns) (durableRunRecord, error) {
	if err := validateDurableRunColumns(columns); err != nil {
		return durableRunRecord{}, err
	}
	revision, err := heteroci.NewRoutingOwnerRevision(columns.lastRevision)
	if err != nil {
		return durableRunRecord{}, err
	}
	record := durableRunRecord{heartbeatTTL: time.Duration(columns.ttlMillis) * time.Millisecond, lastRevision: revision, released: columns.released == 1}
	if err := decodeDurableRunJSON(columns.manifestBytes, columns.unitsBytes, columns.decisionBytes, &record); err != nil {
		return durableRunRecord{}, err
	}
	if err := validateDurableDecisionDigest(record.decision, columns.decisionDigest); err != nil {
		return durableRunRecord{}, err
	}
	return record, nil
}

// validateDurableRunColumns 校验持久化 payload 摘要、TTL 与 released 枚举。
func validateDurableRunColumns(columns durableRunColumns) error {
	if columns.manifestDigest != digestBytes(columns.manifestBytes) || columns.unitsDigest != digestBytes(columns.unitsBytes) {
		return fmt.Errorf("durable routing run payload digest drifted")
	}
	if columns.ttlMillis <= 0 {
		return fmt.Errorf("durable routing run heartbeat TTL is invalid")
	}
	if columns.released != 0 && columns.released != 1 {
		return fmt.Errorf("durable routing run released state is invalid")
	}
	return nil
}

// validateDurableDecisionDigest 校验 routing decision 的 canonical 摘要。
func validateDurableDecisionDigest(decision heteroci.RoutingDecision, stored string) error {
	canonicalDecision, err := decision.CanonicalDigest()
	if err != nil || canonicalDecision != stored || canonicalDecision != decision.DecisionDigest {
		return fmt.Errorf("durable routing decision digest drifted")
	}
	return nil
}

// decodeDurableRunJSON 解码并校验数据库中的 manifest、units 和 decision JSON。
func decodeDurableRunJSON(manifestBytes, unitsBytes, decisionBytes []byte, record *durableRunRecord) error {
	if err := json.Unmarshal(manifestBytes, &record.manifest); err != nil {
		return fmt.Errorf("decode durable manifest: %w", err)
	}
	if err := json.Unmarshal(unitsBytes, &record.units); err != nil {
		return fmt.Errorf("decode durable execution units: %w", err)
	}
	if err := json.Unmarshal(decisionBytes, &record.decision); err != nil {
		return fmt.Errorf("decode durable decision: %w", err)
	}
	if err := record.manifest.Validate(); err != nil {
		return err
	}
	return heteroci.ValidateExecutionUnits(record.units)
}

// loadDurableLease 读取 lease JSON 与列状态并拒绝 projection 漂移。
func loadDurableLease(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, unitID string) (heteroci.RoutingLease, heteroci.RoutingOwnerRevision, error) {
	var leaseBytes []byte
	var state string
	var reserved, started, finished, lastRevision int64
	row := tx.QueryRowContext(ctx, "SELECT lease_json, state, reserved_at_unix_milli, started_at_unix_milli, finished_at_unix_milli, last_revision FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ?", fence.Scope, fence.OwnerEpoch, runID, unitID)
	if err := row.Scan(&leaseBytes, &state, &reserved, &started, &finished, &lastRevision); err != nil {
		return heteroci.RoutingLease{}, 0, fmt.Errorf("read durable routing lease: %w", err)
	}
	var lease heteroci.RoutingLease
	if err := json.Unmarshal(leaseBytes, &lease); err != nil {
		return heteroci.RoutingLease{}, 0, fmt.Errorf("decode durable routing lease: %w", err)
	}
	if lease.RunID != runID || lease.UnitID != unitID {
		return heteroci.RoutingLease{}, 0, fmt.Errorf("durable routing lease identity drifted")
	}
	if string(lease.State) != state || lease.ReservedAtUnixMilli != reserved || lease.StartedAtUnixMilli != started || lease.FinishedAtUnixMilli != finished {
		return heteroci.RoutingLease{}, 0, fmt.Errorf("durable routing lease projection drifted")
	}
	revision, err := heteroci.NewRoutingOwnerRevision(lastRevision)
	if err != nil {
		return heteroci.RoutingLease{}, 0, err
	}
	return lease, revision, nil
}

// validateDurableStartBinding 校验已在规划阶段验证的 snapshot identity、assignment、lease 和工作单元。
func validateDurableStartBinding(run durableRunRecord, lease heteroci.RoutingLease, snapshot heteroci.SnapshotSet, unitID string, fence heteroci.OwnerFence, now time.Time) error {
	if snapshot.OwnerEpoch != fence.OwnerEpoch {
		return fmt.Errorf("durable current snapshot owner epoch drifted")
	}
	if snapshot.CapturedAtUnixMilli < run.decision.SnapshotCapturedAtUnixMilli {
		return fmt.Errorf("durable current snapshot predates routing decision")
	}
	assignment, err := durableAssignment(run.decision.Assignments, unitID)
	if err != nil {
		return err
	}
	unit, err := durableExecutionUnit(run.units, unitID)
	if err != nil {
		return err
	}
	if err := lease.ValidateAgainst(run.decision, now); err != nil {
		return fmt.Errorf("validate durable planned lease: %w", err)
	}
	if lease.State != heteroci.LeaseStatePlanned {
		return fmt.Errorf("durable routing lease is not planned")
	}
	return validateDurableExecutorBinding(assignment, unit, snapshot)
}

// durableAssignment 返回 decision 中唯一的目标工作单元 assignment。
func durableAssignment(assignments []heteroci.RouteAssignment, unitID string) (heteroci.RouteAssignment, error) {
	var found *heteroci.RouteAssignment
	for index := range assignments {
		if assignments[index].UnitID != unitID {
			continue
		}
		if found != nil {
			return heteroci.RouteAssignment{}, fmt.Errorf("durable decision repeats unit assignment")
		}
		copy := assignments[index]
		found = &copy
	}
	if found == nil {
		return heteroci.RouteAssignment{}, fmt.Errorf("durable decision unit assignment is missing")
	}
	return *found, nil
}

// durableExecutionUnit 返回持久化 units 中唯一的目标工作单元。
func durableExecutionUnit(units []heteroci.ExecutionUnit, unitID string) (heteroci.ExecutionUnit, error) {
	var found *heteroci.ExecutionUnit
	for index := range units {
		if units[index].UnitID != unitID {
			continue
		}
		if found != nil {
			return heteroci.ExecutionUnit{}, fmt.Errorf("durable routing run repeats execution unit")
		}
		copy := units[index]
		found = &copy
	}
	if found == nil {
		return heteroci.ExecutionUnit{}, fmt.Errorf("durable routing execution unit is missing")
	}
	return *found, nil
}

// validateDurableExecutorBinding 锁定本地节点身份。
func validateDurableExecutorBinding(assignment heteroci.RouteAssignment, unit heteroci.ExecutionUnit, snapshot heteroci.SnapshotSet) error {
	switch assignment.Executor {
	case heteroci.ExecutorKindLocalNode:
		return validateDurableLocalBinding(assignment, unit, snapshot)
	default:
		return fmt.Errorf("durable routing executor is unknown")
	}
}

// validateDurableLocalBinding 校验节点四身份和当前 active runtime。
func validateDurableLocalBinding(assignment heteroci.RouteAssignment, unit heteroci.ExecutionUnit, snapshot heteroci.SnapshotSet) error {
	for _, node := range snapshot.Nodes {
		if node.NodeID != assignment.NodeID {
			continue
		}
		if node.Endpoint != assignment.Endpoint || node.ProbeIdentity != assignment.ProbeIdentity || node.ProbeBootID != assignment.ProbeBootID {
			return fmt.Errorf("durable local assignment identity drifted")
		}
		if !durableNodeSupportsUnit(node, unit) {
			return fmt.Errorf("durable local node cannot run execution unit")
		}
		return nil
	}
	return fmt.Errorf("durable local assignment node is missing")
}

// durableNodeSupportsUnit 检查 live node 的 platform/runtime 矩阵。
func durableNodeSupportsUnit(node heteroci.NodeSnapshot, unit heteroci.ExecutionUnit) bool {
	if node.Status != heteroci.NodeStatusLive {
		return false
	}
	active := false
	for _, capability := range node.ActiveCapabilities {
		active = active || capability.Runtime == unit.RequiredRuntime
	}
	if !active {
		return false
	}
	switch unit.RequiredRuntime {
	case heteroci.RuntimeUbuntuContainer, heteroci.RuntimeDarlingCompatRuntime, heteroci.RuntimeDockerOSXExperimental:
		return node.Platform == heteroci.PlatformLinuxAMD64
	case heteroci.RuntimeWindowsNative:
		return node.Platform == heteroci.PlatformWindowsAMD64
	case heteroci.RuntimeDarwinNative:
		return node.Platform == unit.TargetPlatform
	default:
		return false
	}
}

// buildStartedTransition 构造 next fence 和 started lease 并校验时间顺序。
func buildStartedTransition(fence heteroci.OwnerFence, lease heteroci.RoutingLease, decision heteroci.RoutingDecision, now time.Time) (heteroci.OwnerFence, heteroci.RoutingLease, error) {
	revision, err := nextOwnerRevision(fence)
	if err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, err
	}
	nextFence, err := heteroci.NewOwnerFence(fence.Scope, fence.OwnerID, fence.OwnerEpoch, revision)
	if err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, err
	}
	next := lease
	next.State = heteroci.LeaseStateStarted
	next.StartedAtUnixMilli = now.UnixMilli()
	next.FinishedAtUnixMilli = 0
	if err := next.ValidateAgainst(decision, now); err != nil {
		return heteroci.OwnerFence{}, heteroci.RoutingLease{}, fmt.Errorf("validate durable started lease: %w", err)
	}
	return nextFence, next, nil
}

// persistStartedLease 以旧状态和旧 revision 为 CAS 条件更新 lease/run projection。
func persistStartedLease(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID, unitID string, leaseRevision, runRevision, nextRevision heteroci.RoutingOwnerRevision, next heteroci.RoutingLease) error {
	leaseBytes, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode durable started lease: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE heteroci_routing_leases SET lease_json = ?, state = ?, started_at_unix_milli = ?, finished_at_unix_milli = ?, last_revision = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND unit_id = ? AND state = ? AND last_revision = ?", leaseBytes, next.State, next.StartedAtUnixMilli, next.FinishedAtUnixMilli, nextRevision, fence.Scope, fence.OwnerEpoch, runID, unitID, heteroci.LeaseStatePlanned, leaseRevision)
	if err != nil {
		return fmt.Errorf("update durable started lease: %w", err)
	}
	if err := requireSingleRow(result, "durable started lease CAS"); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, "UPDATE heteroci_routing_runs SET last_revision = ? WHERE scope = ? AND owner_epoch = ? AND run_id = ? AND released = 0 AND last_revision = ?", nextRevision, fence.Scope, fence.OwnerEpoch, runID, runRevision)
	if err != nil {
		return fmt.Errorf("update durable started run: %w", err)
	}
	return requireSingleRow(result, "durable started run CAS")
}

// requireSingleRow 检查 SQLite CAS 恰好影响一行。
func requireSingleRow(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s affected %d rows", operation, rows)
	}
	return nil
}
