package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

// ensureCoordinatorSchemas 按依赖顺序补齐所有 coordinator schema。
func ensureCoordinatorSchemas(ctx context.Context, db coordinatorSchemaDB) error {
	for _, ensure := range []func(context.Context, coordinatorSchemaDB) error{
		ensureCoordinatorSubmissionAuthoritySchema,
		ensureCoordinatorReceiptSchema,
		ensureCoordinatorActionGrantSchema,
		ensureCoordinatorRecoverySchema,
		ensureCoordinatorContainerEvidenceSchema,
		ensureCoordinatorContainerExitedAtSchema,
		ensureCoordinatorShardSchema,
		ensureCoordinatorGateLogSchema,
	} {
		if err := ensure(ctx, db); err != nil {
			return err
		}
	}
	return nil
}

// ensureCoordinatorContainerExitedAtSchema 为单容器持久记录迁移独立观测到的退出时刻。
func ensureCoordinatorContainerExitedAtSchema(ctx context.Context, db coordinatorSchemaDB) error {
	columns, err := coordinatorJobColumns(ctx, db)
	if err != nil {
		return err
	}
	if columns["container_exited_at"] {
		return nil
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE coordinator_jobs ADD COLUMN container_exited_at TEXT"); err != nil {
		return fmt.Errorf("add coordinator container_exited_at: %w", err)
	}
	return nil
}

// ensureCoordinatorSubmissionAuthoritySchema refuses an old job table before
// any read can silently misclassify historic work as a manual submission.
// Authority cannot be backfilled safely, so operators must rebuild that local
// durable store rather than receiving a partial ALTER TABLE migration.
func ensureCoordinatorSubmissionAuthoritySchema(ctx context.Context, db coordinatorSchemaDB) error {
	columns, err := coordinatorJobColumns(ctx, db)
	if err != nil {
		return err
	}
	missing := make([]string, 0, 3)
	for _, column := range []string{"entrypoint", "authority_owner", "authority_attestation"} {
		if !columns[column] {
			missing = append(missing, column)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("legacy coordinator_jobs schema requires rebuild; missing authority columns: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ensureCoordinatorContainerEvidenceSchema 增加 typed witness 列且不伪造旧执行证据。
func ensureCoordinatorContainerEvidenceSchema(ctx context.Context, db coordinatorSchemaDB) error {
	columns, err := coordinatorJobColumns(ctx, db)
	if err != nil {
		return err
	}
	if err := addCoordinatorContainerEvidenceColumns(ctx, db, columns); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `UPDATE coordinator_jobs SET container_resource_witness_verified = 0
WHERE container_resource_witness_verified IS NULL AND container_phase IN ('', 'prepared', 'created')`)
	if err != nil {
		return fmt.Errorf("mark coordinator rows without verified container evidence: %w", err)
	}
	return nil
}

func coordinatorJobColumns(ctx context.Context, db coordinatorSchemaDB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_jobs)")
	if err != nil {
		return nil, fmt.Errorf("inspect coordinator container evidence schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, errors.Join(fmt.Errorf("scan coordinator container evidence schema: %w", err), rows.Close())
		}
		columns[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("read coordinator container evidence schema: %w", err)
	}
	return columns, nil
}

func addCoordinatorContainerEvidenceColumns(ctx context.Context, db coordinatorSchemaDB, columns map[string]bool) error {
	for _, migration := range []struct {
		column string
		query  string
	}{
		{column: "container_host_config_digest", query: "ALTER TABLE coordinator_jobs ADD COLUMN container_host_config_digest TEXT NOT NULL DEFAULT ''"},
		{column: "container_resource_witness_json", query: "ALTER TABLE coordinator_jobs ADD COLUMN container_resource_witness_json BLOB"},
		{column: "container_resource_witness_digest", query: "ALTER TABLE coordinator_jobs ADD COLUMN container_resource_witness_digest TEXT NOT NULL DEFAULT ''"},
		{column: "container_resource_witness_verified", query: "ALTER TABLE coordinator_jobs ADD COLUMN container_resource_witness_verified INTEGER"},
	} {
		if columns[migration.column] {
			continue
		}
		if _, err := db.ExecContext(ctx, migration.query); err != nil {
			return fmt.Errorf("add coordinator %s: %w", migration.column, err)
		}
	}
	return nil
}

func validateCoordinatorDigest(name, value string) error {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return fmt.Errorf("%w: %s is not a SHA-256 digest", errCoordinatorState, name)
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:")); err != nil {
		return fmt.Errorf("%w: %s is not a SHA-256 digest", errCoordinatorState, name)
	}
	return nil
}

// validateLifecycleResourceWitness 校验事件证据完整性及同一容器内的不可变性。
func validateLifecycleResourceWitness(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) error {
	hasWitness := lifecycleEventHasResourceWitness(event)
	if hasWitness {
		if err := validateCoordinatorResourceWitnessPayload(
			event.HostConfigDigest, &event.ResourceWitness, event.ResourceWitnessDigest, "lifecycle"); err != nil {
			return err
		}
	}
	if lifecyclePhaseRequiresResourceWitness(event.Phase) && !hasWitness {
		return fmt.Errorf("%w: lifecycle verified container resource witness is required", errCoordinatorState)
	}
	return validateLifecycleResourceWitnessTransition(record, event, hasWitness)
}

func lifecycleEventHasResourceWitness(event localci.FreshContainerLifecycleEvent) bool {
	return event.HostConfigDigest != "" || event.ResourceWitnessDigest != "" ||
		event.ResourceWitness != (gatecontract.ContainerResourceWitness{})
}

func lifecyclePhaseRequiresResourceWitness(phase localci.FreshContainerLifecyclePhase) bool {
	return slices.Contains([]localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseStarting,
		localci.FreshContainerPhaseStarted,
		localci.FreshContainerPhaseExited,
		localci.FreshContainerPhaseRemovalPending,
	}, phase)
}

// validateLifecycleResourceWitnessTransition 拒绝同一容器的 witness 被清空或替换。
func validateLifecycleResourceWitnessTransition(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
	hasWitness bool,
) error {
	if event.Phase == localci.FreshContainerPhasePrepared && record.ContainerPhase == localci.FreshContainerPhaseRemoved {
		return nil
	}
	if record.ContainerResourceWitnessVerified && !hasWitness {
		return fmt.Errorf("%w: lifecycle container resource witness verification state drifted", errCoordinatorState)
	}
	if record.ContainerResourceWitness == nil {
		return nil
	}
	if event.HostConfigDigest != record.ContainerHostConfigDigest ||
		event.ResourceWitnessDigest != record.ContainerResourceWitnessDigest ||
		event.ResourceWitness != *record.ContainerResourceWitness {
		return fmt.Errorf("%w: lifecycle container resource witness drifted", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorResourceWitness 校验数据库中的 witness、摘要和验证状态一致。
func validateCoordinatorResourceWitness(record coordinatorJobRecord) error {
	hasWitness := record.ContainerHostConfigDigest != "" || record.ContainerResourceWitness != nil ||
		record.ContainerResourceWitnessDigest != ""
	if hasWitness != record.ContainerResourceWitnessVerified {
		return fmt.Errorf("%w: persisted container resource witness verification state mismatched", errCoordinatorState)
	}
	if hasWitness {
		if err := validateCoordinatorResourceWitnessPayload(
			record.ContainerHostConfigDigest, record.ContainerResourceWitness,
			record.ContainerResourceWitnessDigest, "persisted"); err != nil {
			return err
		}
	}
	if lifecyclePhaseRequiresResourceWitness(record.ContainerPhase) && !hasWitness {
		return fmt.Errorf("%w: persisted verified container resource witness is required", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorResourceWitnessPayload 校验 typed 内容、摘要及固定资源合同。
func validateCoordinatorResourceWitnessPayload(
	hostDigest string,
	witness *gatecontract.ContainerResourceWitness,
	witnessDigest string,
	scope string,
) error {
	if hostDigest == "" || witness == nil || witnessDigest == "" {
		return fmt.Errorf("%w: %s container resource witness is incomplete", errCoordinatorState, scope)
	}
	if err := validateCoordinatorDigest(scope+" container host config digest", hostDigest); err != nil {
		return err
	}
	digest, err := witness.Digest()
	if err != nil {
		return fmt.Errorf("%w: %s container resource witness is invalid: %v", errCoordinatorState, scope, err)
	}
	if digest != witnessDigest {
		return fmt.Errorf("%w: %s container resource witness digest mismatched", errCoordinatorState, scope)
	}
	if *witness != localci.ExpectedFreshContainerResourceWitness() {
		return fmt.Errorf("%w: %s container resource witness drifted from the production resource contract", errCoordinatorState, scope)
	}
	return nil
}

var coordinatorRecoveryColumns = map[string]string{
	"deadline_at":                 "TEXT",
	"scheduler_subsequence":       "INTEGER NOT NULL DEFAULT 0",
	"scheduler_dependencies_json": "BLOB NOT NULL DEFAULT '[]'",
	"active_gate_id":              "TEXT NOT NULL DEFAULT ''",
	"container_phase":             "TEXT NOT NULL DEFAULT ''",
	"container_id":                "TEXT NOT NULL DEFAULT ''",
	"container_labels_json":       "BLOB",
	"container_image_reference":   "TEXT NOT NULL DEFAULT ''",
	"container_config_digest":     "TEXT NOT NULL DEFAULT ''",
	"source_snapshot_dir":         "TEXT NOT NULL DEFAULT ''",
	"removal_proof_digest":        "TEXT NOT NULL DEFAULT ''",
}

// ensureCoordinatorRecoverySchema 为旧数据库逐列补齐恢复字段，任何迁移错误都阻断 owner。
func ensureCoordinatorRecoverySchema(ctx context.Context, db coordinatorSchemaDB) (retErr error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_jobs)")
	if err != nil {
		return fmt.Errorf("inspect coordinator recovery schema: %w", err)
	}
	columns := make(map[string]struct{})
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("scan coordinator recovery schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate coordinator recovery schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close coordinator recovery schema rows: %w", err)
	}
	for name, definition := range coordinatorRecoveryColumns {
		if _, exists := columns[name]; exists {
			continue
		}
		statement := "ALTER TABLE coordinator_jobs ADD COLUMN " + name + " " + definition
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add coordinator recovery column %q: %w", name, err)
		}
	}
	return nil
}

// jobs 按持久 enqueue 顺序读取 owner 启动时需要判定的全部记录。
func (store *coordinatorStore) jobs(ctx context.Context) (records []coordinatorJobRecord, retErr error) {
	rows, err := store.listCoordinatorJobRows(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()
	records, err = scanCoordinatorJobRows(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close coordinator jobs: %w", err)
	}
	if err := store.hydrateRecoveredCoordinatorJobs(ctx, records); err != nil {
		return nil, err
	}
	return records, nil
}

// listCoordinatorJobRows 查询恢复所需的持久 job 行，不在 rows 打开期间加载分片子表。
func (store *coordinatorStore) listCoordinatorJobRows(ctx context.Context) (*sql.Rows, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, container_exited_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, container_host_config_digest,
container_resource_witness_json, container_resource_witness_digest, container_resource_witness_verified, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs ORDER BY enqueue_sequence`)
	if err != nil {
		return nil, fmt.Errorf("list coordinator jobs: %w", err)
	}
	return rows, nil
}

// scanCoordinatorJobRows 在保持查询顺序的同时还原 coordinator job 行。
func scanCoordinatorJobRows(rows *sql.Rows) ([]coordinatorJobRecord, error) {
	var records []coordinatorJobRecord
	for rows.Next() {
		record, err := scanCoordinatorJob(rows, "recovery list")
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate coordinator jobs: %w", err)
	}
	return records, nil
}

// hydrateRecoveredCoordinatorJobs 在关闭 job rows 后加载分片子表并校验持久证据。
func (store *coordinatorStore) hydrateRecoveredCoordinatorJobs(ctx context.Context, records []coordinatorJobRecord) error {
	for index := range records {
		if err := store.hydrateRecoveredCoordinatorJob(ctx, &records[index]); err != nil {
			return err
		}
	}
	return nil
}

// hydrateRecoveredCoordinatorJob 绑定单条 job 与其分片及已签名回执的持久校验。
func (store *coordinatorStore) hydrateRecoveredCoordinatorJob(ctx context.Context, record *coordinatorJobRecord) error {
	shards, err := store.containerShards(ctx, record.JobID)
	if err != nil {
		return err
	}
	record.ContainerShards = shards
	if err := validateCoordinatorContainerMode(*record); err != nil {
		return err
	}
	if record.Receipt != nil {
		return validateStoredResultReceipt(*record)
	}
	return nil
}

func (store *coordinatorStore) replaceRecoveredPassed(ctx context.Context, jobID, message string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET state = ?, gate_results_json = NULL,
error_text = ? WHERE job_id = ? AND state = ?`, jobStateInfraFailed, message, jobID, jobStatePassed)
	if err != nil {
		return fmt.Errorf("invalidate recovered passed job: %w", err)
	}
	return requireOneCoordinatorRow(result, "invalidate recovered passed job")
}

// recordContainerLifecycle 在同一事务内校验并推进单个容器生命周期。
func (store *coordinatorStore) recordContainerLifecycle(
	ctx context.Context,
	jobID string,
	gateID gatecontract.GateID,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) (retErr error) {
	labelsJSON, resourceWitnessJSON, err := encodeLifecyclePersistence(labels, event)
	if err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin coordinator lifecycle transition: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			retErr = errors.Join(retErr, rollbackErr)
		}
	}()
	record, err := scanCoordinatorJob(tx.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, container_exited_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, container_host_config_digest,
container_resource_witness_json, container_resource_witness_digest, container_resource_witness_verified, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE job_id = ?`, jobID), jobID)
	if err != nil {
		return err
	}
	if err := validateLifecycleTransition(record, gateID, labels, event); err != nil {
		return err
	}
	values := coordinatorLifecycleValues(record, event)
	result, err := tx.ExecContext(ctx, `UPDATE coordinator_jobs SET active_gate_id = ?, container_phase = ?,
container_id = ?, container_labels_json = ?, container_image_reference = ?, container_config_digest = ?,
container_host_config_digest = ?, container_resource_witness_json = ?, container_resource_witness_digest = ?,
container_resource_witness_verified = ?,
source_snapshot_dir = ?, started_at = ?, deadline_at = ?, container_exited_at = ?, removal_proof_digest = ?
WHERE job_id = ? AND state = ?`, gateID, event.Phase, values.containerID, labelsJSON,
		event.ImageReference, event.ConfigDigest, event.HostConfigDigest, resourceWitnessJSON,
		event.ResourceWitnessDigest, event.ResourceWitnessDigest != "", event.SourceSnapshotDir, values.startedAt, values.deadline, values.exitedAt,
		values.removalProof, jobID, jobStateStarted)
	if err != nil {
		return fmt.Errorf("persist coordinator container lifecycle: %w", err)
	}
	if err := requireOneCoordinatorRow(result, "record coordinator container lifecycle"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit coordinator lifecycle transition: %w", err)
	}
	return nil
}

func encodeLifecyclePersistence(
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) ([]byte, any, error) {
	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return nil, nil, fmt.Errorf("encode coordinator container labels: %w", err)
	}
	resourceWitnessJSON, err := encodeLifecycleResourceWitness(event)
	if err != nil {
		return nil, nil, err
	}
	return labelsJSON, resourceWitnessJSON, nil
}

func encodeLifecycleResourceWitness(event localci.FreshContainerLifecycleEvent) (any, error) {
	if event.ResourceWitnessDigest == "" {
		return nil, nil
	}
	encoded, err := json.Marshal(event.ResourceWitness)
	if err != nil {
		return nil, fmt.Errorf("encode coordinator container resource witness: %w", err)
	}
	return encoded, nil
}

type coordinatorLifecycleUpdate struct {
	containerID  string
	startedAt    any
	deadline     any
	exitedAt     any
	removalProof string
}

// coordinatorLifecycleValues 保留当前事件未推进的持久化生命周期字段。
func coordinatorLifecycleValues(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
) coordinatorLifecycleUpdate {
	values := coordinatorLifecycleUpdate{
		containerID: event.ContainerID, startedAt: nullableCoordinatorTime(record.StartedAt),
		deadline: nullableCoordinatorTime(record.Deadline), exitedAt: nullableCoordinatorTime(record.ContainerExitedAt),
		removalProof: record.RemovalProofDigest,
	}
	if event.Phase == localci.FreshContainerPhaseStarting && record.StartedAt == nil {
		values.startedAt, values.deadline = event.StartedAt.Format(timeFormat), event.Deadline.Format(timeFormat)
	}
	if event.Phase == localci.FreshContainerPhasePrepared {
		values.containerID, values.removalProof, values.exitedAt = "", "", nil
	}
	if event.Phase == localci.FreshContainerPhaseExited ||
		(event.Phase == localci.FreshContainerPhaseRemovalPending && !event.ExitedAt.IsZero()) {
		values.exitedAt = event.ExitedAt.Format(timeFormat)
	}
	if event.Phase == localci.FreshContainerPhaseRemoved {
		values.removalProof = event.RemovalProofDigest
	}
	return values
}

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

func nullableCoordinatorTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(timeFormat)
}

// validateLifecycleTransition 拒绝身份漂移、时钟重置和越序生命周期事件。
func validateLifecycleTransition(
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) error {
	if err := validateLifecycleBase(record, gateID, labels, event); err != nil {
		return err
	}
	if err := validateLifecyclePhaseOrder(record.ContainerPhase, event.Phase); err != nil {
		return err
	}
	if err := validateLifecycleContainerID(record, event); err != nil {
		return err
	}
	if err := validateLifecycleClock(record, event); err != nil {
		return err
	}
	if err := validateLifecycleResourceWitness(record, event); err != nil {
		return err
	}
	if err := validateLifecycleExitedAt(record, event); err != nil {
		return err
	}
	if event.Phase == localci.FreshContainerPhaseRemoved && event.RemovalProofDigest == "" {
		return fmt.Errorf("%w: lifecycle removal proof is required", errCoordinatorState)
	}
	return nil
}

// validateLifecycleBase 验证 lifecycle 事件携带的 job、gate 与不可变容器身份。
func validateLifecycleBase(
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	labels map[string]string,
	event localci.FreshContainerLifecycleEvent,
) error {
	if record.State != jobStateStarted || gateID == "" || len(labels) == 0 {
		return fmt.Errorf("%w: lifecycle job, gate, or labels are incomplete", errCoordinatorState)
	}
	if event.ImageReference == "" || event.ConfigDigest == "" || event.SourceSnapshotDir == "" {
		return fmt.Errorf("%w: lifecycle immutable container identity is incomplete", errCoordinatorState)
	}
	active := record.ContainerPhase != "" && record.ContainerPhase != localci.FreshContainerPhaseRemoved
	if active && record.ActiveGateID != gateID {
		return fmt.Errorf("%w: lifecycle gate identity drifted", errCoordinatorState)
	}
	return nil
}

// validateLifecycleContainerID 保证 create 后的事件始终引用同一个容器。
func validateLifecycleContainerID(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
) error {
	if !lifecycleContainerIDRequired(record, event) {
		return nil
	}
	if lifecycleContainerIDsMissing(record, event) {
		return fmt.Errorf("%w: lifecycle container ID is missing", errCoordinatorState)
	}
	if lifecycleContainerIDDrifted(record, event) {
		return fmt.Errorf("%w: lifecycle container ID drifted", errCoordinatorState)
	}
	return nil
}

// lifecycleContainerIDRequired 判定当前生命周期阶段是否必须持有容器身份。
func lifecycleContainerIDRequired(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) bool {
	switch event.Phase {
	case localci.FreshContainerPhasePrepared:
		return false
	case localci.FreshContainerPhaseRemoved:
		return record.ContainerPhase != localci.FreshContainerPhaseCreating || event.ContainerID != "" || record.ContainerID != ""
	default:
		return true
	}
}

// lifecycleContainerIDsMissing 判定要求身份的生命周期事件是否同时缺少新旧容器身份。
func lifecycleContainerIDsMissing(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) bool {
	return event.ContainerID == "" && record.ContainerID == ""
}

// lifecycleContainerIDDrifted 判定事件容器身份是否偏离持久化身份。
func lifecycleContainerIDDrifted(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) bool {
	if record.ContainerPhase == localci.FreshContainerPhaseCreating &&
		localci.IsFreshContainerOperationIdentity(record.ContainerID) &&
		event.Phase == localci.FreshContainerPhaseCreated {
		return false
	}
	return record.ContainerID != "" && event.ContainerID != record.ContainerID
}

// validateLifecycleClock 固定首次 start 的 started_at 与 deadline，拒绝重启重算。
func validateLifecycleClock(
	record coordinatorJobRecord,
	event localci.FreshContainerLifecycleEvent,
) error {
	if event.Phase != localci.FreshContainerPhaseStarting {
		return nil
	}
	if event.StartedAt.IsZero() || event.Deadline.IsZero() {
		return fmt.Errorf("%w: lifecycle start timestamps are incomplete", errCoordinatorState)
	}
	if record.StartedAt == nil && !event.Deadline.Equal(event.StartedAt.Add(coordinatorTimeout(record.Profile))) {
		return fmt.Errorf("%w: lifecycle deadline does not match first container start", errCoordinatorState)
	}
	if record.StartedAt != nil && (record.Deadline == nil || !record.Deadline.Equal(event.Deadline)) {
		return fmt.Errorf("%w: lifecycle attempted to reset the original deadline", errCoordinatorState)
	}
	return nil
}

// validateLifecycleExitedAt 将 Docker 退出时刻与协调器完成时刻分离并校验其顺序。
func validateLifecycleExitedAt(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) error {
	if event.Phase == localci.FreshContainerPhaseExited {
		return validateExitedLifecycleExitedAt(record, event)
	}
	if event.Phase == localci.FreshContainerPhaseRemovalPending {
		if event.ExitedAt.IsZero() {
			return nil
		}
		return validateExitedLifecycleExitedAt(record, event)
	}
	if event.Phase == localci.FreshContainerPhaseRemoved {
		return validateRemovedLifecycleExitedAt(record, event)
	}
	return validateNonterminalLifecycleExitedAt(event)
}

// validateExitedLifecycleExitedAt 校验退出事件的可信退出时刻及其与完成时刻的顺序。
func validateExitedLifecycleExitedAt(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) error {
	if event.ExitedAt.IsZero() || event.CompletedAt.IsZero() || record.StartedAt == nil {
		return fmt.Errorf("%w: exited lifecycle timestamps are incomplete", errCoordinatorState)
	}
	if event.ExitedAt.Before(*record.StartedAt) || event.CompletedAt.Before(event.ExitedAt) {
		return fmt.Errorf("%w: exited lifecycle timestamps are invalid", errCoordinatorState)
	}
	if record.ContainerExitedAt != nil && !event.ExitedAt.Equal(*record.ContainerExitedAt) {
		return fmt.Errorf("%w: exited lifecycle timestamp drifted", errCoordinatorState)
	}
	return nil
}

// validateNonterminalLifecycleExitedAt 拒绝未到终态的事件携带伪造退出时刻。
func validateNonterminalLifecycleExitedAt(event localci.FreshContainerLifecycleEvent) error {
	if !event.ExitedAt.IsZero() {
		return fmt.Errorf("%w: nonterminal lifecycle carries an exit timestamp", errCoordinatorState)
	}
	return nil
}

// validateRemovedLifecycleExitedAt 仅允许删除序列中的已退出容器原样携带退出时刻。
func validateRemovedLifecycleExitedAt(record coordinatorJobRecord, event localci.FreshContainerLifecycleEvent) error {
	if record.ContainerPhase != localci.FreshContainerPhaseExited &&
		record.ContainerPhase != localci.FreshContainerPhaseRemovalPending {
		if !event.ExitedAt.IsZero() {
			return fmt.Errorf("%w: unproved removal carries a forged exit timestamp", errCoordinatorState)
		}
		return nil
	}
	if record.ContainerExitedAt == nil {
		if event.ExitedAt.IsZero() {
			return nil
		}
		return fmt.Errorf("%w: removed lifecycle exit timestamp drifted", errCoordinatorState)
	}
	if event.ExitedAt.IsZero() || !event.ExitedAt.Equal(*record.ContainerExitedAt) {
		return fmt.Errorf("%w: removed lifecycle exit timestamp drifted", errCoordinatorState)
	}
	return nil
}

func validateLifecyclePhaseOrder(current, next localci.FreshContainerLifecyclePhase) error {
	allowed := map[localci.FreshContainerLifecyclePhase][]localci.FreshContainerLifecyclePhase{
		"":                                        {localci.FreshContainerPhasePrepared},
		localci.FreshContainerPhasePrepared:       {localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating},
		localci.FreshContainerPhaseCreating:       {localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseRemovalPending, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseCreated:        {localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseRemovalPending, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseStarting:       {localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseRemovalPending, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseStarted:        {localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemovalPending, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseExited:         {localci.FreshContainerPhaseExited, localci.FreshContainerPhaseRemovalPending, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseRemovalPending: {localci.FreshContainerPhaseRemovalPending, localci.FreshContainerPhaseRemoved},
		localci.FreshContainerPhaseRemoved:        {localci.FreshContainerPhaseRemoved, localci.FreshContainerPhasePrepared},
	}
	if slices.Contains(allowed[current], next) {
		return nil
	}
	return fmt.Errorf("%w: container lifecycle transition %q -> %q is invalid", errCoordinatorState, current, next)
}

// cleanupRecoveredShardSource 清理组内共享的 deterministic source snapshot。
func cleanupRecoveredShardSource(record coordinatorJobRecord) error {
	for _, shard := range record.ContainerShards {
		if shard.SourceSnapshotDir != "" {
			return cleanupDeterministicRecoverySource(coordinatorJobRecord{JobID: record.JobID, SourceSnapshotDir: shard.SourceSnapshotDir})
		}
	}
	return nil
}

// finishRecoveredTerminalShardGroup 补齐已终态 job 尚未完成的 scheduler group。
func (owner *coordinatorOwner) finishRecoveredTerminalShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	schedulerStatus localci.WorkloadStatus,
) error {
	if err := owner.requireRecoveredShardRemovalProofs(ctx, record.JobID); err != nil {
		return err
	}
	want := schedulerTerminalState(record.State)
	if schedulerStatus == want {
		return nil
	}
	return owner.completeRecoveredShardSchedulerGroup(ctx, admission, want)
}

// completeRecoveredShardSchedulerGroup 以状态复查覆盖 CompleteGroup 响应丢失窗口。
func (owner *coordinatorOwner) completeRecoveredShardSchedulerGroup(
	ctx context.Context,
	admission coordinatorShardAdmission,
	status localci.WorkloadStatus,
) error {
	current, err := owner.schedulerClient.State(ctx, admission.WorkloadID)
	if err != nil {
		return err
	}
	if current == status {
		return nil
	}
	if err := owner.schedulerClient.CompleteGroup(ctx, admission.WorkloadID, admission.GroupIdentity, status); err == nil {
		return nil
	} else if current, stateErr := owner.schedulerClient.State(ctx, admission.WorkloadID); stateErr == nil && current == status {
		return nil
	} else {
		return errors.Join(err, stateErr)
	}
}

// cleanupRecoveredShardGroup 在有界上下文中尝试全部 sibling 并聚合清理错误。
func (owner *coordinatorOwner) cleanupRecoveredShardGroup(ctx context.Context, record coordinatorJobRecord) error {
	cleanupCtx, cancelCleanup := coordinatorCleanupContext(ctx)
	defer cancelCleanup()
	shards, err := owner.store.containerShards(cleanupCtx, record.JobID)
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, shard := range shards {
		request, err := owner.recoveryShardCleanupRequest(record, shard)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			continue
		}
		if request == nil {
			continue
		}
		result, err := owner.dependencies.RecoveryRunner.CleanupUnprovedFreshContainer(cleanupCtx, *request)
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleanup recovery shard %q: %w", shard.Shard.IdentityDigest, err))
			continue
		}
		if !result.Container.Removed || result.RemovalProofDigest == "" {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleanup recovery shard %q produced no removal proof", shard.Shard.IdentityDigest))
		}
	}
	return errors.Join(cleanupErr, owner.requireRecoveredShardRemovalProofs(cleanupCtx, record.JobID))
}

// requireRecoveredShardRemovalProofs 区分 never-started，并要求其余每片持久 removal proof。
func (owner *coordinatorOwner) requireRecoveredShardRemovalProofs(ctx context.Context, jobID string) error {
	shards, err := owner.store.containerShards(ctx, jobID)
	if err != nil {
		return err
	}
	for _, shard := range shards {
		if recoveryShardNeverStarted(shard) {
			continue
		}
		if shard.ContainerPhase != localci.FreshContainerPhaseRemoved || shard.RemovalProofDigest == "" {
			return fmt.Errorf("recovery shard %q lacks durable removal proof", shard.Shard.IdentityDigest)
		}
	}
	return nil
}
