package heterociowner

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// RunIdentityAmbiguousError 表示同一 scope 的 run_id 横跨多个 owner epoch。
type RunIdentityAmbiguousError struct{}

// Error 返回不包含运行载荷或节点身份的稳定错误。
func (RunIdentityAmbiguousError) Error() string {
	return "heteroci routing run identity is ambiguous"
}

// RunRecoveryRequiredError 表示运行属于非当前 owner epoch，必须走显式恢复流程。
type RunRecoveryRequiredError struct{}

// Error 返回不包含运行载荷或节点身份的稳定错误。
func (RunRecoveryRequiredError) Error() string {
	return "heteroci routing run recovery is required"
}

// DurableRunState 是 durable owner 中一个运行及其租约的只读投影。
type DurableRunState struct {
	Fence        heteroci.OwnerFence           `json:"fence"`
	RunID        string                        `json:"run_id"`
	Manifest     heteroci.Manifest             `json:"manifest"`
	Units        []heteroci.ExecutionUnit      `json:"units"`
	Decision     heteroci.RoutingDecision      `json:"decision"`
	Leases       []heteroci.RoutingLease       `json:"leases"`
	HeartbeatTTL time.Duration                 `json:"heartbeat_ttl"`
	Released     bool                          `json:"released"`
	LastRevision heteroci.RoutingOwnerRevision `json:"last_revision"`
}

// DurableOperationState 是 owner journal 中一个 operation 的只读投影。
type DurableOperationState struct {
	Fence         heteroci.OwnerFence           `json:"fence"`
	OperationID   heteroci.OperationID          `json:"operation_id"`
	Kind          string                        `json:"kind"`
	RequestDigest string                        `json:"request_digest"`
	ResultDigest  string                        `json:"result_digest"`
	ResultBytes   []byte                        `json:"result_bytes"`
	Revision      heteroci.RoutingOwnerRevision `json:"revision"`
}

type durableReadStateError struct {
	cause error
}

// Error 返回不泄露 durable payload、端点或探针身份的稳定读取错误。
func (err durableReadStateError) Error() string {
	return "heteroci owner durable state read failed"
}

// Unwrap 保留内部根因供 errors.Is/As 判断，但不把根因文本暴露给调用方。
func (err durableReadStateError) Unwrap() error {
	return err.cause
}

type durableRunReadRow struct {
	scope            heteroci.RoutingOwnerScope
	ownerEpoch       int64
	runID            string
	manifestBytes    []byte
	manifestDigest   string
	unitsBytes       []byte
	unitsDigest      string
	ttlMillis        int64
	decisionBytes    []byte
	decisionDigest   string
	released         int64
	reservedRevision int64
	lastRevision     int64
	epochOwnerID     string
	epochStartedAt   int64
	epochRetiredAt   sql.NullInt64
	headOwnerID      string
	headEpoch        int64
	headRevision     int64
	headEpochOwnerID string
	headEpochStarted int64
	headEpochRetired sql.NullInt64
}

type durableOwnerEpochRead struct {
	ownerID   heteroci.RoutingOwnerID
	epoch     heteroci.RoutingOwnerEpoch
	startedAt int64
	retiredAt sql.NullInt64
}

const maxDurableRunTTLMillis = int64((1<<63 - 1) / int64(time.Millisecond))

// LocateRun 按 scope 和 run_id 查找唯一 durable 运行，允许返回唯一历史 epoch 供审计。
func (owner *Owner) LocateRun(ctx context.Context, runID string) ([]DurableRunState, error) {
	if err := validateReadStateArguments(owner, runID); err != nil {
		return nil, redactDurableReadError(err)
	}
	now, err := owner.readStateNow()
	if err != nil {
		return nil, redactDurableReadError(err)
	}
	tx, err := beginDurableReadTx(ctx, owner)
	if err != nil {
		return nil, redactDurableReadError(err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := readDurableRunRows(ctx, tx, owner.scope, runID)
	if err != nil {
		return nil, redactDurableReadError(err)
	}
	if len(rows) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, redactDurableReadError(err)
		}
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, &RunIdentityAmbiguousError{}
	}
	state, err := buildDurableRunState(ctx, tx, rows[0], now)
	if err != nil {
		return nil, redactDurableReadError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, redactDurableReadError(err)
	}
	return []DurableRunState{state}, nil
}

// ReadRun 读取当前 owner epoch 中唯一的 durable 运行；历史运行必须显式恢复。
func (owner *Owner) ReadRun(ctx context.Context, runID string) (DurableRunState, error) {
	if err := validateReadStateArguments(owner, runID); err != nil {
		return DurableRunState{}, redactDurableReadError(err)
	}
	now, err := owner.readStateNow()
	if err != nil {
		return DurableRunState{}, redactDurableReadError(err)
	}
	tx, err := beginDurableReadTx(ctx, owner)
	if err != nil {
		return DurableRunState{}, redactDurableReadError(err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := readDurableRunRows(ctx, tx, owner.scope, runID)
	if err != nil {
		return DurableRunState{}, redactDurableReadError(err)
	}
	if len(rows) == 0 {
		return DurableRunState{}, redactDurableReadError(errors.New("durable routing run is missing"))
	}
	if len(rows) != 1 {
		return DurableRunState{}, &RunIdentityAmbiguousError{}
	}
	state, err := buildDurableRunState(ctx, tx, rows[0], now)
	if err != nil {
		return DurableRunState{}, redactDurableReadError(err)
	}
	if !durableRunIsCurrent(rows[0]) {
		return DurableRunState{}, &RunRecoveryRequiredError{}
	}
	if err := tx.Commit(); err != nil {
		return DurableRunState{}, redactDurableReadError(err)
	}
	return state, nil
}

// ReadOperation 读取指定 owner epoch 的不可变 journal operation，不产生任何 mutation。
func (owner *Owner) ReadOperation(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID) (DurableOperationState, bool, error) {
	if err := validateReadOperationArguments(owner, fence, operationID); err != nil {
		return DurableOperationState{}, false, redactDurableReadError(err)
	}
	tx, err := beginDurableReadTx(ctx, owner)
	if err != nil {
		return DurableOperationState{}, false, redactDurableReadError(err)
	}
	defer func() { _ = tx.Rollback() }()
	record, found, epoch, head, err := readDurableOperationRecord(ctx, tx, owner.scope, fence, operationID)
	if err != nil {
		return DurableOperationState{}, false, redactDurableReadError(err)
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return DurableOperationState{}, false, redactDurableReadError(err)
		}
		return DurableOperationState{}, false, nil
	}
	result, err := buildDurableOperationState(record, owner.scope, epoch, head, operationID)
	if err != nil {
		return DurableOperationState{}, false, redactDurableReadError(err)
	}
	if err := tx.Commit(); err != nil {
		return DurableOperationState{}, false, redactDurableReadError(err)
	}
	return result, true, nil
}

// readDurableOperationRecord 校验 epoch/head 身份并读取不可变 operation journal。
func readDurableOperationRecord(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, fence heteroci.OwnerFence, operationID heteroci.OperationID) (OperationRecord, bool, durableOwnerEpochRead, heteroci.OwnerFence, error) {
	epoch, head, err := readDurableOwnerEpoch(ctx, tx, scope, fence.OwnerEpoch)
	if err != nil {
		return OperationRecord{}, false, durableOwnerEpochRead{}, heteroci.OwnerFence{}, err
	}
	if err := validateDurableOperationAuthority(epoch, head, fence); err != nil {
		return OperationRecord{}, false, durableOwnerEpochRead{}, heteroci.OwnerFence{}, err
	}
	record, found, err := readOperationByID(ctx, tx, scope, fence.OwnerEpoch, operationID)
	if err != nil {
		return OperationRecord{}, false, durableOwnerEpochRead{}, heteroci.OwnerFence{}, err
	}
	return record, found, epoch, head, nil
}

// validateDurableOperationAuthority 校验 operation 所属 epoch 与当前 head 的顺序。
func validateDurableOperationAuthority(epoch durableOwnerEpochRead, head heteroci.OwnerFence, fence heteroci.OwnerFence) error {
	if epoch.ownerID != fence.OwnerID || head.OwnerEpoch < epoch.epoch {
		return errors.New("durable operation owner identity is invalid")
	}
	if head.OwnerEpoch == epoch.epoch && head.OwnerID != epoch.ownerID {
		return errors.New("durable operation current owner identity is invalid")
	}
	if head.OwnerEpoch > epoch.epoch && !epoch.retiredAt.Valid {
		return errors.New("durable operation historical epoch is not retired")
	}
	if head.OwnerEpoch == epoch.epoch && epoch.retiredAt.Valid {
		return errors.New("durable operation current epoch is retired")
	}
	return nil
}

// buildDurableOperationState 构造深拷贝的 operation 只读投影并校验 revision。
func buildDurableOperationState(record OperationRecord, scope heteroci.RoutingOwnerScope, epoch durableOwnerEpochRead, head heteroci.OwnerFence, operationID heteroci.OperationID) (DurableOperationState, error) {
	if record.Scope != scope || record.OwnerEpoch != epoch.epoch || record.OperationID != operationID {
		return DurableOperationState{}, errors.New("durable operation identity drifted")
	}
	if record.OwnerEpoch == head.OwnerEpoch && record.Revision > head.Revision {
		return DurableOperationState{}, errors.New("durable operation revision is ahead of owner head")
	}
	operationFence, err := heteroci.NewOwnerFence(scope, epoch.ownerID, epoch.epoch, record.Revision)
	if err != nil {
		return DurableOperationState{}, err
	}
	return DurableOperationState{
		Fence:         operationFence,
		OperationID:   record.OperationID,
		Kind:          record.Kind,
		RequestDigest: record.RequestDigest,
		ResultDigest:  record.ResultDigest,
		ResultBytes:   append([]byte(nil), record.ResultBytes...),
		Revision:      record.Revision,
	}, nil
}

// validateReadStateArguments 校验 owner、scope 与 run identity 的输入边界。
func validateReadStateArguments(owner *Owner, runID string) error {
	if owner == nil || owner.db == nil {
		return errors.New("heteroci owner is nil")
	}
	if err := owner.scope.Validate(); err != nil {
		return err
	}
	if !validOwnerRunID(runID) {
		return errors.New("heteroci owner run id is invalid")
	}
	return nil
}

// validateReadOperationArguments 校验 owner、fence 与 operation identity 的输入边界。
func validateReadOperationArguments(owner *Owner, fence heteroci.OwnerFence, operationID heteroci.OperationID) error {
	if owner == nil || owner.db == nil {
		return errors.New("heteroci owner is nil")
	}
	if err := fence.Validate(); err != nil {
		return err
	}
	if fence.Scope != owner.scope {
		return errors.New("heteroci owner fence scope is not bound to owner")
	}
	if _, err := heteroci.ParseOperationID(string(operationID)); err != nil {
		return err
	}
	return nil
}

func (owner *Owner) readStateNow() (time.Time, error) {
	nowUnixMilli, err := owner.nowUnixMilli()
	if err != nil {
		return time.Time{}, err
	}
	return time.UnixMilli(nowUnixMilli), nil
}

func beginDurableReadTx(ctx context.Context, owner *Owner) (*sql.Tx, error) {
	return owner.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
}

// readDurableRunRows 在单个只读事务中读取运行投影及 owner epoch 映射。
func readDurableRunRows(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, runID string) ([]durableRunReadRow, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT r.owner_epoch, r.run_id, r.manifest_json, r.manifest_digest,
       r.units_json, r.units_digest, r.heartbeat_ttl_millis,
       r.decision_json, r.decision_digest, r.released,
       r.reserved_revision, r.last_revision,
       e.owner_id, e.started_at_unix_milli, e.retired_at_unix_milli,
       h.owner_id, h.owner_epoch, h.revision,
       he.owner_id, he.started_at_unix_milli, he.retired_at_unix_milli
FROM heteroci_routing_runs AS r
JOIN heteroci_owner_epochs AS e
  ON e.scope = r.scope AND e.owner_epoch = r.owner_epoch
JOIN heteroci_owner_heads AS h
  ON h.scope = r.scope
JOIN heteroci_owner_epochs AS he
  ON he.scope = h.scope AND he.owner_epoch = h.owner_epoch
WHERE r.scope = ? AND r.run_id = ?
ORDER BY r.owner_epoch`, scope, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []durableRunReadRow
	for rows.Next() {
		var row durableRunReadRow
		row.scope = scope
		if err := rows.Scan(
			&row.ownerEpoch, &row.runID, &row.manifestBytes, &row.manifestDigest,
			&row.unitsBytes, &row.unitsDigest, &row.ttlMillis,
			&row.decisionBytes, &row.decisionDigest, &row.released,
			&row.reservedRevision, &row.lastRevision,
			&row.epochOwnerID, &row.epochStartedAt, &row.epochRetiredAt,
			&row.headOwnerID, &row.headEpoch, &row.headRevision,
			&row.headEpochOwnerID, &row.headEpochStarted, &row.headEpochRetired,
		); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// buildDurableRunState 校验运行、租约和预留后构造深拷贝只读投影。
func buildDurableRunState(ctx context.Context, tx *sql.Tx, row durableRunReadRow, now time.Time) (DurableRunState, error) {
	fence, run, err := validateDurableRunReadRow(row, now)
	if err != nil {
		return DurableRunState{}, err
	}
	reservedRevision, err := heteroci.NewRoutingOwnerRevision(row.reservedRevision)
	if err != nil {
		return DurableRunState{}, err
	}
	leases, err := readDurableRunLeases(ctx, tx, fence, row.runID, run.decision, run.lastRevision, reservedRevision, now)
	if err != nil {
		return DurableRunState{}, err
	}
	if err := validateDurableRunLeaseSet(leases, run.decision, run.released, reservedRevision, run.lastRevision); err != nil {
		return DurableRunState{}, err
	}
	if err := validateDurableRunReservations(ctx, tx, fence, row.runID, run.released, run.lastRevision); err != nil {
		return DurableRunState{}, err
	}
	return DurableRunState{
		Fence:        fence,
		RunID:        row.runID,
		Manifest:     run.manifest,
		Units:        append([]heteroci.ExecutionUnit(nil), run.units...),
		Decision:     run.decision,
		Leases:       leases,
		HeartbeatTTL: run.heartbeatTTL,
		Released:     run.released,
		LastRevision: run.lastRevision,
	}, nil
}

// validateDurableRunReadRow 校验运行行、owner 映射、revision 和 JSON projection。
func validateDurableRunReadRow(row durableRunReadRow, _ time.Time) (heteroci.OwnerFence, durableRunRecord, error) {
	if err := validateDurableRunRowShape(row); err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	epochState, err := parseDurableRunEpochState(row)
	if err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	if err := validateDurableRunEpochRelation(row, epochState); err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	_, lastRevision, err := parseDurableRunRevisions(row)
	if err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	if epochState.headEpoch == epochState.epoch && epochState.headRevision < lastRevision {
		return heteroci.OwnerFence{}, durableRunRecord{}, errors.New("durable routing run revision is ahead of owner head")
	}
	fence, err := heteroci.NewOwnerFence(row.scope, epochState.epochOwnerID, epochState.epoch, lastRevision)
	if err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	run, err := decodeDurableRunProjection(row, lastRevision)
	if err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	if err := validateDurableReadDecision(run.decision); err != nil {
		return heteroci.OwnerFence{}, durableRunRecord{}, err
	}
	return fence, run, nil
}

// validateDurableRunRowShape 校验运行行的稳定标识、TTL 和 released 枚举。
func validateDurableRunRowShape(row durableRunReadRow) error {
	if !validOwnerRunID(row.runID) || row.ttlMillis <= 0 || row.ttlMillis > maxDurableRunTTLMillis {
		return errors.New("durable routing run identity or ttl is invalid")
	}
	if row.released != 0 && row.released != 1 {
		return errors.New("durable routing run released state is invalid")
	}
	return nil
}

// parseDurableRunEpochState 解析运行 epoch 与当前 owner head 的严格身份映射。
func parseDurableRunEpochState(row durableRunReadRow) (durableRunEpochState, error) {
	epoch, err := parseDurableRunEpochIdentity(row)
	if err != nil {
		return durableRunEpochState{}, err
	}
	head, err := parseDurableRunHeadIdentity(row)
	if err != nil {
		return durableRunEpochState{}, err
	}
	return durableRunEpochState{epoch: epoch.epoch, epochOwnerID: epoch.ownerID, headEpoch: head.epoch, headOwnerID: head.ownerID, headRevision: head.revision}, nil
}

type durableRunEpochState struct {
	epoch        heteroci.RoutingOwnerEpoch
	epochOwnerID heteroci.RoutingOwnerID
	headEpoch    heteroci.RoutingOwnerEpoch
	headOwnerID  heteroci.RoutingOwnerID
	headRevision heteroci.RoutingOwnerRevision
}

type durableRunHeadState struct {
	epoch    heteroci.RoutingOwnerEpoch
	ownerID  heteroci.RoutingOwnerID
	revision heteroci.RoutingOwnerRevision
}

type durableRunEpochIdentity struct {
	epoch   heteroci.RoutingOwnerEpoch
	ownerID heteroci.RoutingOwnerID
}

// parseDurableRunEpochIdentity 解析运行所属 epoch 和 owner identity。
func parseDurableRunEpochIdentity(row durableRunReadRow) (durableRunEpochIdentity, error) {
	epoch, err := heteroci.NewRoutingOwnerEpoch(row.ownerEpoch)
	if err != nil {
		return durableRunEpochIdentity{}, err
	}
	ownerID, err := heteroci.ParseRoutingOwnerID(row.epochOwnerID)
	if err != nil || row.epochStartedAt <= 0 {
		return durableRunEpochIdentity{}, errors.New("durable routing owner epoch is invalid")
	}
	if err := validateDurableRetirement(row.epochStartedAt, row.epochRetiredAt); err != nil {
		return durableRunEpochIdentity{}, err
	}
	return durableRunEpochIdentity{epoch: epoch, ownerID: ownerID}, nil
}

// parseDurableRunHeadIdentity 解析当前 owner head 与其 epoch 映射。
func parseDurableRunHeadIdentity(row durableRunReadRow) (durableRunHeadState, error) {
	epoch, err := heteroci.NewRoutingOwnerEpoch(row.headEpoch)
	if err != nil {
		return durableRunHeadState{}, err
	}
	revision, err := heteroci.NewRoutingOwnerRevision(row.headRevision)
	if err != nil {
		return durableRunHeadState{}, err
	}
	ownerID, err := heteroci.ParseRoutingOwnerID(row.headOwnerID)
	if err != nil || row.headEpochStarted <= 0 {
		return durableRunHeadState{}, errors.New("durable current owner epoch is invalid")
	}
	if _, err := heteroci.ParseRoutingOwnerID(row.headEpochOwnerID); err != nil {
		return durableRunHeadState{}, errors.New("durable current owner epoch is invalid")
	}
	if err := validateDurableRetirement(row.headEpochStarted, row.headEpochRetired); err != nil {
		return durableRunHeadState{}, err
	}
	if row.headEpochOwnerID != row.headOwnerID || row.headEpochRetired.Valid {
		return durableRunHeadState{}, errors.New("durable current owner epoch mapping drifted")
	}
	return durableRunHeadState{epoch: epoch, ownerID: ownerID, revision: revision}, nil
}

// validateDurableRunEpochRelation 校验当前与历史 owner epoch 的退休关系。
func validateDurableRunEpochRelation(row durableRunReadRow, state durableRunEpochState) error {
	if state.headEpoch < state.epoch || (state.headEpoch == state.epoch && state.headOwnerID != state.epochOwnerID) {
		return errors.New("durable owner epoch ordering drifted")
	}
	if state.headEpoch == state.epoch && row.epochRetiredAt.Valid {
		return errors.New("current durable owner epoch is retired")
	}
	if state.headEpoch > state.epoch && !row.epochRetiredAt.Valid {
		return errors.New("historical durable owner epoch is not retired")
	}
	return nil
}

// parseDurableRunRevisions 解析 reserved_revision 与 last_revision 的单调关系。
func parseDurableRunRevisions(row durableRunReadRow) (heteroci.RoutingOwnerRevision, heteroci.RoutingOwnerRevision, error) {
	reservedRevision, err := heteroci.NewRoutingOwnerRevision(row.reservedRevision)
	if err != nil {
		return 0, 0, err
	}
	lastRevision, err := heteroci.NewRoutingOwnerRevision(row.lastRevision)
	if err != nil || lastRevision < reservedRevision {
		return 0, 0, errors.New("durable routing run revision is invalid")
	}
	return reservedRevision, lastRevision, nil
}

// decodeDurableRunProjection 严格校验运行 JSON、摘要、TTL 和 released 投影。
func decodeDurableRunProjection(row durableRunReadRow, lastRevision heteroci.RoutingOwnerRevision) (durableRunRecord, error) {
	columns := durableRunColumns{
		manifestBytes:  append([]byte(nil), row.manifestBytes...),
		manifestDigest: row.manifestDigest,
		unitsBytes:     append([]byte(nil), row.unitsBytes...),
		unitsDigest:    row.unitsDigest,
		ttlMillis:      row.ttlMillis,
		decisionBytes:  append([]byte(nil), row.decisionBytes...),
		decisionDigest: row.decisionDigest,
		released:       int(row.released),
		lastRevision:   int64(lastRevision),
	}
	if err := validateDurableRunColumns(columns); err != nil {
		return durableRunRecord{}, err
	}
	run, err := decodeAndValidateDurableRun(columns)
	if err != nil {
		return durableRunRecord{}, err
	}
	if run.lastRevision != lastRevision || run.released != (row.released == 1) {
		return durableRunRecord{}, errors.New("durable routing run projection drifted")
	}
	return run, nil
}

func validateDurableRetirement(startedAt int64, retiredAt sql.NullInt64) error {
	if !retiredAt.Valid {
		return nil
	}
	if retiredAt.Int64 <= 0 || retiredAt.Int64 < startedAt {
		return errors.New("durable owner epoch retirement is invalid")
	}
	return nil
}

func validateDurableReadDecision(decision heteroci.RoutingDecision) error {
	if decision.SchemaVersion != heteroci.RoutingDecisionSchemaVersion || decision.Authority != heteroci.AuthorityLocalNonAuthoritative || decision.Assignments == nil || !validOwnerDigest(decision.PackingPlanDigest) {
		return errors.New("durable routing decision identity is invalid")
	}
	return validateDurableDecisionDigest(decision, decision.DecisionDigest)
}

// readDurableRunLeases 读取并校验运行内全部 lease 的 JSON、状态和 revision。
func readDurableRunLeases(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, decision heteroci.RoutingDecision, lastRevision, reservedRevision heteroci.RoutingOwnerRevision, now time.Time) ([]heteroci.RoutingLease, error) {
	rows, err := tx.QueryContext(ctx, "SELECT unit_id FROM heteroci_routing_leases WHERE scope = ? AND owner_epoch = ? AND run_id = ? ORDER BY unit_id", fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var leases []heteroci.RoutingLease
	for rows.Next() {
		var unitID string
		if err := rows.Scan(&unitID); err != nil {
			return nil, err
		}
		lease, revision, err := loadDurableLease(ctx, tx, fence, runID, unitID)
		if err != nil {
			return nil, err
		}
		if revision < reservedRevision || revision > lastRevision {
			return nil, errors.New("durable routing lease revision is outside run revision")
		}
		if err := lease.ValidateAgainst(decision, now); err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return leases, nil
}

// validateDurableRunLeaseSet 校验 lease 覆盖、生命周期 terminal 状态和 run released 投影。
func validateDurableRunLeaseSet(leases []heteroci.RoutingLease, decision heteroci.RoutingDecision, released bool, reservedRevision, lastRevision heteroci.RoutingOwnerRevision) error {
	if len(leases) == 0 || len(leases) != len(decision.Assignments) {
		return errors.New("durable routing run lease set is incomplete")
	}
	seen, allTerminal, err := inspectDurableRunLeaseStates(leases, reservedRevision, lastRevision)
	if err != nil {
		return err
	}
	if err := validateDurableRunLeaseCoverage(seen, decision); err != nil {
		return err
	}
	if released != allTerminal {
		return errors.New("durable routing run released state disagrees with lease states")
	}
	return nil
}

// inspectDurableRunLeaseStates 检查 lease identity、revision 并汇总 terminal 状态。
func inspectDurableRunLeaseStates(leases []heteroci.RoutingLease, reservedRevision, lastRevision heteroci.RoutingOwnerRevision) (map[string]struct{}, bool, error) {
	seen := make(map[string]struct{}, len(leases))
	allTerminal := true
	for _, lease := range leases {
		if _, exists := seen[lease.UnitID]; exists {
			return nil, false, errors.New("durable routing run lease set contains duplicate unit")
		}
		seen[lease.UnitID] = struct{}{}
		if lease.ReservedAtUnixMilli <= 0 || reservedRevision <= 0 || lastRevision < reservedRevision {
			return nil, false, errors.New("durable routing lease reservation identity is invalid")
		}
		allTerminal = allTerminal && durableLeaseStateTerminal(lease.State)
	}
	return seen, allTerminal, nil
}

// durableLeaseStateTerminal 判断 lease 是否已经不可再次启动。
func durableLeaseStateTerminal(state heteroci.RoutingLeaseState) bool {
	switch state {
	case heteroci.LeaseStateSucceeded, heteroci.LeaseStateFailed, heteroci.LeaseStateCancelled:
		return true
	default:
		return false
	}
}

// validateDurableRunLeaseCoverage 校验 decision assignment 与 lease 的双向覆盖。
func validateDurableRunLeaseCoverage(seen map[string]struct{}, decision heteroci.RoutingDecision) error {
	for _, assignment := range decision.Assignments {
		if _, exists := seen[assignment.UnitID]; !exists {
			return errors.New("durable routing decision lease coverage is incomplete")
		}
	}
	return nil
}

// validateDurableRunReservations 校验 reservation released 与 run 生命周期的一致性。
func validateDurableRunReservations(ctx context.Context, tx *sql.Tx, fence heteroci.OwnerFence, runID string, released bool, lastRevision heteroci.RoutingOwnerRevision) error {
	rows, err := tx.QueryContext(ctx, "SELECT released, last_revision FROM heteroci_routing_reservations WHERE scope = ? AND owner_epoch = ? AND run_id = ?", fence.Scope, fence.OwnerEpoch, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var reservationReleased, revision int64
		if err := rows.Scan(&reservationReleased, &revision); err != nil {
			return err
		}
		parsedRevision, err := heteroci.NewRoutingOwnerRevision(revision)
		if err != nil || (reservationReleased != 0 && reservationReleased != 1) || parsedRevision > lastRevision || (reservationReleased == 1) != released {
			return errors.New("durable routing reservation projection is inconsistent")
		}
	}
	return rows.Err()
}

// readDurableOwnerEpoch 读取指定 epoch 映射及当前 owner head，保持只读语义。
func readDurableOwnerEpoch(ctx context.Context, tx *sql.Tx, scope heteroci.RoutingOwnerScope, epoch heteroci.RoutingOwnerEpoch) (durableOwnerEpochRead, heteroci.OwnerFence, error) {
	var ownerID string
	var startedAt int64
	var retiredAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT owner_id, started_at_unix_milli, retired_at_unix_milli FROM heteroci_owner_epochs WHERE scope = ? AND owner_epoch = ?", scope, epoch).Scan(&ownerID, &startedAt, &retiredAt); err != nil {
		return durableOwnerEpochRead{}, heteroci.OwnerFence{}, err
	}
	parsedOwnerID, err := heteroci.ParseRoutingOwnerID(ownerID)
	if err != nil || startedAt <= 0 {
		return durableOwnerEpochRead{}, heteroci.OwnerFence{}, errors.New("durable owner epoch is invalid")
	}
	if err := validateDurableRetirement(startedAt, retiredAt); err != nil {
		return durableOwnerEpochRead{}, heteroci.OwnerFence{}, err
	}
	head, found, err := readOwnerHead(ctx, tx, scope)
	if err != nil {
		return durableOwnerEpochRead{}, heteroci.OwnerFence{}, err
	}
	if !found {
		return durableOwnerEpochRead{}, heteroci.OwnerFence{}, errors.New("heteroci owner head is missing")
	}
	return durableOwnerEpochRead{ownerID: parsedOwnerID, epoch: epoch, startedAt: startedAt, retiredAt: retiredAt}, head, nil
}

func durableRunIsCurrent(row durableRunReadRow) bool {
	return !row.epochRetiredAt.Valid && row.headEpoch == row.ownerEpoch && row.headOwnerID == row.epochOwnerID
}

func redactDurableReadError(cause error) error {
	if cause == nil {
		return nil
	}
	return durableReadStateError{cause: cause}
}
