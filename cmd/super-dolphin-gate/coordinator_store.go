package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const coordinatorStoreSchema = `
CREATE TABLE IF NOT EXISTS coordinator_jobs (
 enqueue_sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 invocation_id TEXT NOT NULL UNIQUE,
 job_id TEXT NOT NULL UNIQUE,
 repository_root TEXT NOT NULL,
 plan_json BLOB NOT NULL,
 profile TEXT NOT NULL,
 job_source_tree_sha TEXT NOT NULL,
 image_provenance_source_tree_sha TEXT NOT NULL DEFAULT '',
 scheduler_subsequence INTEGER NOT NULL DEFAULT 0,
 scheduler_dependencies_json BLOB NOT NULL DEFAULT '[]',
 state TEXT NOT NULL,
 submitted_at TEXT NOT NULL,
 started_at TEXT,
 deadline_at TEXT,
 completed_at TEXT,
 active_gate_id TEXT NOT NULL DEFAULT '',
 container_phase TEXT NOT NULL DEFAULT '',
 container_id TEXT NOT NULL DEFAULT '',
 container_labels_json BLOB,
 container_image_reference TEXT NOT NULL DEFAULT '',
 container_config_digest TEXT NOT NULL DEFAULT '',
 source_snapshot_dir TEXT NOT NULL DEFAULT '',
 removal_proof_digest TEXT NOT NULL DEFAULT '',
 gate_results_json BLOB,
 receipt_id TEXT,
 receipt_json BLOB,
 error_text TEXT NOT NULL DEFAULT ''
);`

type coordinatorStore struct {
	db *sql.DB
}

type coordinatorJobRecord struct {
	InvocationID                 string
	JobID                        string
	EnqueueSequence              uint64
	RepositoryRoot               string
	Plan                         gatecontract.GatePlan
	Profile                      gatecontract.Profile
	JobSourceTreeSHA             string
	ImageProvenanceSourceTreeSHA string
	SchedulerSubsequence         uint32
	SchedulerDependencies        []string
	State                        jobState
	SubmittedAt                  time.Time
	StartedAt                    *time.Time
	Deadline                     *time.Time
	CompletedAt                  *time.Time
	ActiveGateID                 gatecontract.GateID
	ContainerPhase               localci.FreshContainerLifecyclePhase
	ContainerID                  string
	ContainerLabels              map[string]string
	ContainerImageReference      string
	ContainerConfigDigest        string
	SourceSnapshotDir            string
	RemovalProofDigest           string
	GateResults                  []gatecontract.GateResult
	ReceiptID                    string
	Receipt                      *gatecontract.ResultReceipt
	Error                        string
}

// openCoordinatorStore 打开 daemon identity 专属 SQLite，并补齐 recovery 与 receipt schema。
func openCoordinatorStore(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) (*coordinatorStore, error) {
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		return nil, err
	}
	if len(checkpoint.IdentityKey) != 64 {
		return nil, errors.New("coordinator identity key must be a SHA-256 hex digest")
	}
	path := filepath.Join(runtimeRoot, "localci-coordinator-"+checkpoint.IdentityKey+".db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open coordinator SQLite: %w", err)
	}
	if _, err := db.ExecContext(ctx, coordinatorStoreSchema); err != nil {
		return nil, errors.Join(fmt.Errorf("initialize coordinator SQLite: %w", err), db.Close())
	}
	if err := ensureCoordinatorReceiptSchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := ensureCoordinatorActionGrantSchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := ensureCoordinatorRecoverySchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	db.SetMaxOpenConns(1)
	return &coordinatorStore{db: db}, nil
}

// ensureCoordinatorReceiptSchema 校验 receipt 列完整存在，拒绝旧 schema 静默运行。
func ensureCoordinatorReceiptSchema(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(coordinator_jobs)")
	if err != nil {
		return fmt.Errorf("inspect coordinator receipt schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return errors.Join(fmt.Errorf("scan coordinator receipt schema: %w", err), rows.Close())
		}
		columns[name] = true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("read coordinator receipt schema: %w", err)
	}
	for _, migration := range []struct {
		column string
		query  string
	}{
		{column: "receipt_id", query: "ALTER TABLE coordinator_jobs ADD COLUMN receipt_id TEXT"},
		{column: "receipt_json", query: "ALTER TABLE coordinator_jobs ADD COLUMN receipt_json BLOB"},
	} {
		if columns[migration.column] {
			continue
		}
		if _, err := db.ExecContext(ctx, migration.query); err != nil {
			return fmt.Errorf("add coordinator %s: %w", migration.column, err)
		}
	}
	if _, err := db.ExecContext(ctx,
		"CREATE UNIQUE INDEX IF NOT EXISTS coordinator_receipt_id_unique ON coordinator_jobs(receipt_id) WHERE receipt_id IS NOT NULL",
	); err != nil {
		return fmt.Errorf("create coordinator receipt uniqueness index: %w", err)
	}
	return nil
}

func coordinatorRuntimeRoot() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve coordinator cache root: %w", err)
	}
	runtimeRoot := filepath.Join(filepath.Clean(cacheRoot), "super-dolphin", "localci")
	info, err := os.Lstat(runtimeRoot)
	if err != nil {
		return "", fmt.Errorf("inspect scheduler-created runtime root: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("coordinator runtime root must be a private directory")
	}
	return runtimeRoot, nil
}

// createJob 在 scheduler enqueue 前持久化独立 invocation 与 workload metadata。
func (store *coordinatorStore) createJob(
	ctx context.Context,
	invocationID string,
	jobID string,
	repositoryRoot string,
	plan gatecontract.GatePlan,
	candidatePlan localci.PromotionCandidatePlan,
) (coordinatorJobRecord, error) {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("encode coordinator plan: %w", err)
	}
	subsequence, dependencies, err := schedulerMetadataForCandidatePlan(candidatePlan)
	if err != nil {
		return coordinatorJobRecord{}, err
	}
	dependenciesJSON, err := json.Marshal(dependencies)
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("encode coordinator scheduler dependencies: %w", err)
	}
	now := time.Now().UTC()
	result, err := store.db.ExecContext(ctx, `INSERT INTO coordinator_jobs (
invocation_id, job_id, repository_root, plan_json, profile, job_source_tree_sha,
scheduler_subsequence, scheduler_dependencies_json, state, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, invocationID, jobID, repositoryRoot, planJSON, plan.Profile,
		plan.Source.SourceTreeSHA, subsequence, dependenciesJSON, jobStateQueued, now.Format(time.RFC3339Nano))
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("persist coordinator invocation/job: %w", err)
	}
	sequence, err := result.LastInsertId()
	if err != nil || sequence <= 0 {
		return coordinatorJobRecord{}, fmt.Errorf("read coordinator enqueue sequence: %w", err)
	}
	return coordinatorJobRecord{
		InvocationID: invocationID, JobID: jobID, EnqueueSequence: uint64(sequence),
		RepositoryRoot: repositoryRoot, Plan: plan, Profile: plan.Profile,
		JobSourceTreeSHA: plan.Source.SourceTreeSHA, SchedulerSubsequence: subsequence,
		SchedulerDependencies: dependencies, State: jobStateQueued, SubmittedAt: now,
	}, nil
}

func schedulerMetadataForCandidatePlan(plan localci.PromotionCandidatePlan) (uint32, []string, error) {
	if plan.BuildRequired {
		if plan.WorkloadID == "" {
			return 0, nil, errors.New("candidate build workload ID is required")
		}
		return 1, []string{plan.WorkloadID}, nil
	}
	if plan.WorkloadID != "" {
		return 0, nil, errors.New("candidate workload ID requires a build dependency")
	}
	return 0, []string{}, nil
}

func (store *coordinatorStore) job(ctx context.Context, jobID string) (coordinatorJobRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE job_id = ?`, jobID)
	record, err := scanCoordinatorJob(row, jobID)
	return requireCoordinatorPassedReceipt(record, err)
}

func (store *coordinatorStore) jobByInvocation(
	ctx context.Context,
	invocationID string,
) (coordinatorJobRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE invocation_id = ?`, invocationID)
	record, err := scanCoordinatorJob(row, invocationID)
	return requireCoordinatorPassedReceipt(record, err)
}

func (store *coordinatorStore) jobByReceiptID(
	ctx context.Context,
	receiptID string,
) (coordinatorJobRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE receipt_id = ?`, receiptID)
	record, err := scanCoordinatorJob(row, receiptID)
	return requireCoordinatorPassedReceipt(record, err)
}

// requireCoordinatorPassedReceipt 阻止普通读取暴露缺少权威 receipt 的 passed。
func requireCoordinatorPassedReceipt(
	record coordinatorJobRecord,
	err error,
) (coordinatorJobRecord, error) {
	if err != nil {
		return coordinatorJobRecord{}, err
	}
	if record.State == jobStatePassed && record.Receipt == nil {
		return coordinatorJobRecord{}, fmt.Errorf("%w: persisted passed job has no signed result receipt", errCoordinatorState)
	}
	return record, nil
}

func (store *coordinatorStore) startJob(ctx context.Context, jobID string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET state = ?
WHERE job_id = ? AND state = ?`, jobStateStarted, jobID, jobStateQueued)
	if err != nil {
		return fmt.Errorf("persist coordinator job start: %w", err)
	}
	return requireOneCoordinatorRow(result, "start coordinator job")
}

func (store *coordinatorStore) recordImageProvenance(ctx context.Context, jobID, tree string) error {
	if tree == "" {
		return errors.New("image provenance source tree SHA is required")
	}
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET image_provenance_source_tree_sha = ?
WHERE job_id = ? AND state = ?`, tree, jobID, jobStateStarted)
	if err != nil {
		return fmt.Errorf("persist image provenance source tree: %w", err)
	}
	return requireOneCoordinatorRow(result, "record image provenance")
}

// finishJob 原子写入 terminal 状态、逐 gate 结果与可选权威 receipt。
func (store *coordinatorStore) finishJob(
	ctx context.Context,
	jobID string,
	state jobState,
	results []gatecontract.GateResult,
	errorText string,
	receipt *gatecontract.ResultReceipt,
) error {
	if err := validateCoordinatorFinishState(state, receipt); err != nil {
		return err
	}
	resultJSON, completedAt, receiptID, receiptJSON, err := encodeCoordinatorCompletion(jobID, results, receipt)
	if err != nil {
		return err
	}
	result, err := store.persistCoordinatorCompletion(
		ctx, jobID, state, resultJSON, completedAt, receiptID, receiptJSON, errorText,
	)
	if err != nil {
		return err
	}
	if err := requireOneCoordinatorRow(result, "finish coordinator job"); err == nil {
		return nil
	}
	return store.validateIdempotentCompletion(ctx, jobID, state, results, errorText, receipt)
}

// validateCoordinatorFinishState 校验 terminal 状态与 receipt 的允许组合。
func validateCoordinatorFinishState(state jobState, receipt *gatecontract.ResultReceipt) error {
	if !state.terminal() {
		return fmt.Errorf("%w: finish state %q is not terminal", errCoordinatorState, state)
	}
	if state == jobStatePassed && receipt == nil {
		return fmt.Errorf("%w: passed job requires a signed result receipt", errCoordinatorState)
	}
	if state != jobStatePassed && receipt != nil {
		return fmt.Errorf("%w: only passed jobs may persist a result receipt", errCoordinatorState)
	}
	return nil
}

// encodeCoordinatorCompletion 编码同一原子写所需的结果和 receipt 数据。
func encodeCoordinatorCompletion(
	jobID string,
	results []gatecontract.GateResult,
	receipt *gatecontract.ResultReceipt,
) ([]byte, time.Time, any, any, error) {
	resultJSON, err := json.Marshal(results)
	if err != nil {
		return nil, time.Time{}, nil, nil, fmt.Errorf("encode coordinator gate results: %w", err)
	}
	completedAt := time.Now().UTC()
	if receipt == nil {
		return resultJSON, completedAt, nil, nil, nil
	}
	receiptJSON, err := encodeCoordinatorResultReceipt(jobID, results, receipt)
	if err != nil {
		return nil, time.Time{}, nil, nil, err
	}
	return resultJSON, receipt.CompletedAt, receipt.ReceiptID, receiptJSON, nil
}

// encodeCoordinatorResultReceipt 校验 receipt 与 job/result 绑定后执行规范编码。
func encodeCoordinatorResultReceipt(
	jobID string,
	results []gatecontract.GateResult,
	receipt *gatecontract.ResultReceipt,
) ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, fmt.Errorf("validate coordinator result receipt: %w", err)
	}
	if receipt.ReceiptID != resultReceiptID(jobID) || !reflect.DeepEqual(receipt.GateResults, results) {
		return nil, fmt.Errorf("%w: receipt does not match coordinator job or gate results", errCoordinatorState)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode coordinator result receipt: %w", err)
	}
	return encoded, nil
}

// persistCoordinatorCompletion 使用单条 UPDATE 保持 terminal 与 receipt 原子可见。
func (store *coordinatorStore) persistCoordinatorCompletion(
	ctx context.Context,
	jobID string,
	state jobState,
	resultJSON []byte,
	completedAt time.Time,
	receiptID any,
	receiptJSON any,
	errorText string,
) (sql.Result, error) {
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET state = ?, completed_at = ?,
gate_results_json = ?, receipt_id = ?, receipt_json = ?, error_text = ?
WHERE job_id = ? AND state IN (?, ?)`, state, completedAt.Format(time.RFC3339Nano),
		resultJSON, receiptID, receiptJSON, errorText, jobID, jobStateQueued, jobStateStarted)
	if err != nil {
		return nil, fmt.Errorf("persist coordinator terminal state: %w", err)
	}
	return result, nil
}

// validateIdempotentCompletion 只接受与首次 terminal 写完全一致的重放。
func (store *coordinatorStore) validateIdempotentCompletion(
	ctx context.Context,
	jobID string,
	state jobState,
	results []gatecontract.GateResult,
	errorText string,
	receipt *gatecontract.ResultReceipt,
) error {
	existing, loadErr := store.job(ctx, jobID)
	if loadErr == nil && existing.State == state && reflect.DeepEqual(existing.GateResults, results) &&
		existing.Error == errorText && receiptsEqual(existing.Receipt, receipt) {
		return nil
	}
	return errors.Join(fmt.Errorf("%w: terminal write was not idempotent", errCoordinatorState), loadErr)
}

// scanCoordinatorJob 严格恢复 plan、时间和结果字段，不接受持久化漂移。
type coordinatorRowScanner interface {
	Scan(...any) error
}

// scanCoordinatorJob 严格解码数据库行并执行完整持久字段守卫。
func scanCoordinatorJob(row coordinatorRowScanner, jobID string) (coordinatorJobRecord, error) {
	var record coordinatorJobRecord
	var planJSON, schedulerDependenciesJSON, resultsJSON, containerLabelsJSON, receiptJSON []byte
	var profile, state, submitted, activeGateID, containerPhase string
	var started, deadline, completed, receiptID sql.NullString
	err := row.Scan(&record.InvocationID, &record.JobID, &record.EnqueueSequence, &record.RepositoryRoot,
		&planJSON, &profile, &record.JobSourceTreeSHA, &record.ImageProvenanceSourceTreeSHA,
		&state, &submitted, &record.SchedulerSubsequence, &schedulerDependenciesJSON,
		&started, &deadline, &completed, &activeGateID, &containerPhase,
		&record.ContainerID, &containerLabelsJSON, &record.ContainerImageReference,
		&record.ContainerConfigDigest, &record.SourceSnapshotDir, &record.RemovalProofDigest,
		&resultsJSON, &receiptID, &receiptJSON, &record.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return coordinatorJobRecord{}, fmt.Errorf("%w: %q", errCoordinatorNotFound, jobID)
	}
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("read coordinator job: %w", err)
	}
	record.Profile, record.State = gatecontract.Profile(profile), jobState(state)
	record.ActiveGateID = gatecontract.GateID(activeGateID)
	record.ContainerPhase = localci.FreshContainerLifecyclePhase(containerPhase)
	if err := decodeCoordinatorJSON(schedulerDependenciesJSON, &record.SchedulerDependencies); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted scheduler dependencies: %w", err)
	}
	if err := decodeOptionalCoordinatorJSON(containerLabelsJSON, &record.ContainerLabels); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted container labels: %w", err)
	}
	return decodeScannedCoordinatorJob(
		record, planJSON, resultsJSON, receiptJSON, submitted, started, deadline, completed, receiptID,
	)
}

// decodeScannedCoordinatorJob 严格解码 scan 已取得的结构化字段。
func decodeScannedCoordinatorJob(
	record coordinatorJobRecord,
	planJSON []byte,
	resultsJSON []byte,
	receiptJSON []byte,
	submitted string,
	started sql.NullString,
	deadline sql.NullString,
	completed sql.NullString,
	receiptID sql.NullString,
) (coordinatorJobRecord, error) {
	if err := gatecontract.DecodeStrictJSON(planJSON, &record.Plan); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted coordinator plan: %w", err)
	}
	if err := decodeCoordinatorTimes(&record, submitted, started, deadline, completed); err != nil {
		return coordinatorJobRecord{}, err
	}
	if err := decodeCoordinatorGateResults(&record, resultsJSON); err != nil {
		return coordinatorJobRecord{}, err
	}
	if err := decodeCoordinatorStoredReceipt(&record, receiptID, receiptJSON); err != nil {
		return coordinatorJobRecord{}, err
	}
	if err := validateCoordinatorRecord(record); err != nil {
		return coordinatorJobRecord{}, err
	}
	return record, nil
}

// decodeCoordinatorGateResults 解码可选的逐 gate 结果数组。
func decodeCoordinatorGateResults(record *coordinatorJobRecord, resultsJSON []byte) error {
	if len(resultsJSON) == 0 {
		return nil
	}
	if err := decodeCoordinatorJSON(resultsJSON, &record.GateResults); err != nil {
		return fmt.Errorf("decode persisted gate results: %w", err)
	}
	return nil
}

// decodeCoordinatorStoredReceipt 校验双列完整性并严格解码可选 receipt。
func decodeCoordinatorStoredReceipt(
	record *coordinatorJobRecord,
	receiptID sql.NullString,
	receiptJSON []byte,
) error {
	if receiptID.Valid != (len(receiptJSON) > 0) {
		return fmt.Errorf("%w: persisted receipt columns are incomplete", errCoordinatorState)
	}
	if !receiptID.Valid {
		return nil
	}
	var receipt gatecontract.ResultReceipt
	if err := decodeCoordinatorJSON(receiptJSON, &receipt); err != nil {
		return fmt.Errorf("decode persisted result receipt: %w", err)
	}
	record.ReceiptID, record.Receipt = receiptID.String, &receipt
	return nil
}

func decodeCoordinatorJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("coordinator JSON contains trailing data")
	}
	return nil
}

func decodeOptionalCoordinatorJSON(payload []byte, target any) error {
	if len(payload) == 0 {
		return nil
	}
	return decodeCoordinatorJSON(payload, target)
}

func decodeCoordinatorTimes(record *coordinatorJobRecord, submitted string, started, deadline, completed sql.NullString) error {
	var err error
	record.SubmittedAt, err = time.Parse(time.RFC3339Nano, submitted)
	if err != nil {
		return fmt.Errorf("parse coordinator submitted_at: %w", err)
	}
	if record.StartedAt, err = parseCoordinatorTime(started); err != nil {
		return err
	}
	if record.Deadline, err = parseCoordinatorTime(deadline); err != nil {
		return err
	}
	record.CompletedAt, err = parseCoordinatorTime(completed)
	return err
}

// validateCoordinatorRecord 拒绝 plan、profile、tree 或状态字段的任何持久化漂移。
func validateCoordinatorRecord(record coordinatorJobRecord) error {
	validators := []func(coordinatorJobRecord) error{
		validateCoordinatorIdentity,
		validateCoordinatorPlanState,
		validateCoordinatorRecordState,
		validateCoordinatorClock,
		validateCoordinatorContainer,
	}
	for _, validate := range validators {
		if err := validate(record); err != nil {
			return err
		}
	}
	if record.Receipt == nil {
		return nil
	}
	return validateStoredResultReceipt(record)
}

// validateCoordinatorIdentity 校验持久化 invocation 与 job 主身份。
func validateCoordinatorIdentity(record coordinatorJobRecord) error {
	if record.InvocationID == "" || record.JobID == "" || record.EnqueueSequence == 0 {
		return fmt.Errorf("%w: persisted invocation/job identity is incomplete", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorPlanState 校验 plan 派生字段及可识别的 job 状态。
func validateCoordinatorPlanState(record coordinatorJobRecord) error {
	if err := record.Plan.Validate(); err != nil {
		return fmt.Errorf("%w: persisted plan is invalid: %v", errCoordinatorState, err)
	}
	if record.Profile != record.Plan.Profile || record.JobSourceTreeSHA != record.Plan.Source.SourceTreeSHA {
		return fmt.Errorf("%w: persisted job fields drifted from plan", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorRecordState 校验状态枚举及 passed/receipt 不变量。
func validateCoordinatorRecordState(record coordinatorJobRecord) error {
	if record.State != jobStateQueued && record.State != jobStateStarted && !record.State.terminal() {
		return fmt.Errorf("%w: unknown persisted job state %q", errCoordinatorState, record.State)
	}
	if err := validateCoordinatorSchedulerMetadata(record); err != nil {
		return err
	}
	return nil
}

// validateStoredResultReceipt 校验持久化 receipt 与当前 durable job 完全一致。
func validateStoredResultReceipt(record coordinatorJobRecord) error {
	receipt := record.Receipt
	if receipt == nil || receipt.ReceiptID != record.ReceiptID || receipt.ReceiptID != resultReceiptID(record.JobID) {
		return fmt.Errorf("%w: persisted receipt identity does not match job", errCoordinatorState)
	}
	if err := receipt.Validate(); err != nil {
		return fmt.Errorf("%w: persisted receipt is invalid: %v", errCoordinatorState, err)
	}
	if storedResultReceiptDrifted(record, receipt) {
		return fmt.Errorf("%w: persisted receipt drifted from coordinator job", errCoordinatorState)
	}
	return validateStoredReceiptCompletion(record, receipt)
}

// storedResultReceiptDrifted 比较 receipt 中所有 job 侧权威绑定。
func storedResultReceiptDrifted(
	record coordinatorJobRecord,
	receipt *gatecontract.ResultReceipt,
) bool {
	return receipt.InvocationID != record.InvocationID || !reflect.DeepEqual(receipt.Source, record.Plan.Source) ||
		receipt.PlanDigest != record.Plan.PlanDigest || receipt.PolicyDigest != record.Plan.PolicyDigest ||
		!reflect.DeepEqual(receipt.GateResults, record.GateResults) ||
		receipt.Status != gatecontract.ResultStatusPassed
}

// validateStoredReceiptCompletion 校验 terminal 完成时间与 receipt 时间一致。
func validateStoredReceiptCompletion(
	record coordinatorJobRecord,
	receipt *gatecontract.ResultReceipt,
) error {
	if record.CompletedAt == nil || !record.CompletedAt.Equal(receipt.CompletedAt) {
		return fmt.Errorf("%w: persisted receipt completion time drifted from job", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorSchedulerMetadata 仅接受无依赖 job 或单一 build 前驱。
func validateCoordinatorSchedulerMetadata(record coordinatorJobRecord) error {
	if record.SchedulerSubsequence == 0 && len(record.SchedulerDependencies) == 0 {
		return nil
	}
	if record.SchedulerSubsequence != 1 || len(record.SchedulerDependencies) != 1 ||
		record.SchedulerDependencies[0] == "" {
		return fmt.Errorf("%w: persisted scheduler build dependency is invalid", errCoordinatorState)
	}
	return nil
}

func receiptsEqual(left, right *gatecontract.ResultReceipt) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return reflect.DeepEqual(*left, *right)
}

// validateCoordinatorClock 校验首次启动时钟成对存在且未被重算。
func validateCoordinatorClock(record coordinatorJobRecord) error {
	if (record.StartedAt == nil) != (record.Deadline == nil) {
		return fmt.Errorf("%w: persisted started_at and deadline_at are incomplete", errCoordinatorState)
	}
	if record.StartedAt != nil && !record.Deadline.Equal(record.StartedAt.Add(coordinatorTimeout(record.Profile))) {
		return fmt.Errorf("%w: persisted deadline drifted from first start", errCoordinatorState)
	}
	if record.State == jobStateQueued && (record.ContainerPhase != "" || record.StartedAt != nil) {
		return fmt.Errorf("%w: queued job contains execution lifecycle state", errCoordinatorState)
	}
	return nil
}

// validateCoordinatorContainer 校验生命周期身份、阶段与销毁证明。
func validateCoordinatorContainer(record coordinatorJobRecord) error {
	if record.ContainerPhase != "" {
		if !completeCoordinatorContainerIdentity(record) {
			return fmt.Errorf("%w: persisted container identity is incomplete", errCoordinatorState)
		}
		if !knownCoordinatorContainerPhase(record.ContainerPhase) {
			return fmt.Errorf("%w: unknown persisted container phase %q", errCoordinatorState, record.ContainerPhase)
		}
	}
	if record.ContainerPhase == localci.FreshContainerPhaseRemoved && record.RemovalProofDigest == "" {
		return fmt.Errorf("%w: removed container lacks removal proof", errCoordinatorState)
	}
	if record.State == jobStatePassed && record.RemovalProofDigest == "" {
		return fmt.Errorf("%w: passed job lacks removal proof", errCoordinatorState)
	}
	return nil
}

func completeCoordinatorContainerIdentity(record coordinatorJobRecord) bool {
	return record.ActiveGateID != "" && len(record.ContainerLabels) > 0 &&
		record.ContainerImageReference != "" && record.ContainerConfigDigest != "" &&
		record.SourceSnapshotDir != ""
}

func knownCoordinatorContainerPhase(phase localci.FreshContainerLifecyclePhase) bool {
	_, exists := map[localci.FreshContainerLifecyclePhase]struct{}{
		localci.FreshContainerPhasePrepared: {}, localci.FreshContainerPhaseCreated: {},
		localci.FreshContainerPhaseStarting: {}, localci.FreshContainerPhaseStarted: {},
		localci.FreshContainerPhaseExited: {}, localci.FreshContainerPhaseRemoved: {},
	}[phase]
	return exists
}

func parseCoordinatorTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse coordinator timestamp: %w", err)
	}
	return &parsed, nil
}

func requireOneCoordinatorRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s changed %d rows", errCoordinatorState, action, rows)
	}
	return nil
}

func (record coordinatorJobRecord) status() jobStatus {
	return jobStatus{
		InvocationID: record.InvocationID, JobID: record.JobID, EnqueueSequence: record.EnqueueSequence,
		State: record.State, Profile: record.Profile, JobSourceTreeSHA: record.JobSourceTreeSHA,
		ImageProvenanceSourceTreeSHA: record.ImageProvenanceSourceTreeSHA,
		SubmittedAt:                  record.SubmittedAt, StartedAt: record.StartedAt, CompletedAt: record.CompletedAt,
		GateResults: append([]gatecontract.GateResult(nil), record.GateResults...), Error: record.Error,
		ReceiptID: record.ReceiptID,
		Terminal:  record.State.terminal(),
	}
}

func (store *coordinatorStore) close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}
