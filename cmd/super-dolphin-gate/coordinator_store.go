package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
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
 entrypoint TEXT NOT NULL,
 authority_owner TEXT NOT NULL,
 authority_attestation TEXT NOT NULL DEFAULT '',
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
 container_exited_at TEXT,
 completed_at TEXT,
 active_gate_id TEXT NOT NULL DEFAULT '',
 container_phase TEXT NOT NULL DEFAULT '',
 container_id TEXT NOT NULL DEFAULT '',
 container_labels_json BLOB,
 container_image_reference TEXT NOT NULL DEFAULT '',
 container_config_digest TEXT NOT NULL DEFAULT '',
 container_host_config_digest TEXT NOT NULL DEFAULT '',
 container_resource_witness_json BLOB,
 container_resource_witness_digest TEXT NOT NULL DEFAULT '',
 container_resource_witness_verified INTEGER NOT NULL DEFAULT 0,
 source_snapshot_dir TEXT NOT NULL DEFAULT '',
 removal_proof_digest TEXT NOT NULL DEFAULT '',
 gate_results_json BLOB,
 receipt_id TEXT,
 receipt_json BLOB,
 error_text TEXT NOT NULL DEFAULT ''
);`

type coordinatorStore struct {
	db              *sql.DB
	now             func() time.Time
	shardDeadlineMu sync.Mutex
}

type coordinatorJobRecord struct {
	InvocationID                     string
	JobID                            string
	EnqueueSequence                  uint64
	RepositoryRoot                   string
	Authority                        submissionAuthority
	Plan                             gatecontract.GatePlan
	Profile                          gatecontract.Profile
	JobSourceTreeSHA                 string
	ImageProvenanceSourceTreeSHA     string
	SchedulerSubsequence             uint32
	SchedulerDependencies            []string
	State                            jobState
	SubmittedAt                      time.Time
	StartedAt                        *time.Time
	Deadline                         *time.Time
	ContainerExitedAt                *time.Time
	CompletedAt                      *time.Time
	ActiveGateID                     gatecontract.GateID
	ContainerPhase                   localci.FreshContainerLifecyclePhase
	ContainerID                      string
	ContainerLabels                  map[string]string
	ContainerImageReference          string
	ContainerConfigDigest            string
	ContainerHostConfigDigest        string
	ContainerResourceWitness         *gatecontract.ContainerResourceWitness
	ContainerResourceWitnessDigest   string
	ContainerResourceWitnessVerified bool
	SourceSnapshotDir                string
	RemovalProofDigest               string
	GateResults                      []gatecontract.GateResult
	ReceiptID                        string
	Receipt                          *gatecontract.ResultReceipt
	Error                            string
	ContainerShards                  []coordinatorShardRecord
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
	if err := ensureCoordinatorSchemas(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := ensureCoordinatorShardAdmissionSchema(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("protect coordinator SQLite: %w", err), db.Close())
	}
	db.SetMaxOpenConns(1)
	return &coordinatorStore{db: db, now: time.Now}, nil
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
	authority submissionAuthority,
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
invocation_id, job_id, repository_root, entrypoint, authority_owner, authority_attestation, plan_json, profile, job_source_tree_sha,
scheduler_subsequence, scheduler_dependencies_json, state, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, invocationID, jobID, repositoryRoot,
		authority.Entrypoint, authority.Owner, authority.Attestation, planJSON, plan.Profile,
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
		RepositoryRoot: repositoryRoot, Authority: authority, Plan: plan, Profile: plan.Profile,
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
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, container_exited_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, container_host_config_digest,
container_resource_witness_json, container_resource_witness_digest, container_resource_witness_verified, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE job_id = ?`, jobID)
	record, err := scanCoordinatorJob(row, jobID)
	return store.completeCoordinatorJobRead(ctx, record, err)
}

func (store *coordinatorStore) jobByInvocation(
	ctx context.Context,
	invocationID string,
) (coordinatorJobRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, container_exited_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, container_host_config_digest,
container_resource_witness_json, container_resource_witness_digest, container_resource_witness_verified, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE invocation_id = ?`, invocationID)
	record, err := scanCoordinatorJob(row, invocationID)
	return store.completeCoordinatorJobRead(ctx, record, err)
}

func (store *coordinatorStore) jobByReceiptID(
	ctx context.Context,
	receiptID string,
) (coordinatorJobRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
 scheduler_subsequence, scheduler_dependencies_json,
started_at, deadline_at, container_exited_at, completed_at, active_gate_id, container_phase, container_id,
container_labels_json, container_image_reference, container_config_digest, container_host_config_digest,
container_resource_witness_json, container_resource_witness_digest, container_resource_witness_verified, source_snapshot_dir,
removal_proof_digest, gate_results_json, receipt_id, receipt_json, error_text FROM coordinator_jobs WHERE receipt_id = ?`, receiptID)
	record, err := scanCoordinatorJob(row, receiptID)
	return store.completeCoordinatorJobRead(ctx, record, err)
}

// completeCoordinatorJobRead 补全 job 的分片记录，并校验容器模式及已签发 receipt。
func (store *coordinatorStore) completeCoordinatorJobRead(
	ctx context.Context,
	record coordinatorJobRecord,
	err error,
) (coordinatorJobRecord, error) {
	record, err = requireCoordinatorPassedReceipt(record, err)
	if err != nil {
		return coordinatorJobRecord{}, err
	}
	shards, err := store.containerShards(ctx, record.JobID)
	if err != nil {
		return coordinatorJobRecord{}, err
	}
	record.ContainerShards = shards
	if err := validateCoordinatorContainerMode(record); err != nil {
		return coordinatorJobRecord{}, err
	}
	if record.Receipt != nil {
		if err := validateStoredResultReceipt(record); err != nil {
			return coordinatorJobRecord{}, err
		}
	}
	return record, nil
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

// startJob 以 queued CAS 占有执行权；execution clock 由首次 Starting lifecycle 固化。
func (store *coordinatorStore) startJob(ctx context.Context, jobID string) error {
	if store == nil || store.db == nil {
		return errors.New("coordinator store start dependencies are required")
	}
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
WHERE job_id = ? AND state IN (?, ?)
AND (? NOT IN (?, ?, ?, ?) OR (
  (NOT EXISTS (SELECT 1 FROM coordinator_job_shards WHERE coordinator_job_shards.job_id = coordinator_jobs.job_id)
   AND container_exited_at IS NOT NULL)
  OR
  ((SELECT COUNT(*) FROM coordinator_job_shards WHERE coordinator_job_shards.job_id = coordinator_jobs.job_id) = ?
   AND NOT EXISTS (SELECT 1 FROM coordinator_job_shards
                   WHERE coordinator_job_shards.job_id = coordinator_jobs.job_id AND exited_at IS NULL))
))`, state, completedAt.Format(time.RFC3339Nano),
		resultJSON, receiptID, receiptJSON, errorText, jobID, jobStateQueued, jobStateStarted,
		state, jobStatePassed, jobStateFailed, jobStateCancelled, jobStateTimeout, gatecontract.MaxContainerShards)
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
