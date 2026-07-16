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
 state TEXT NOT NULL,
 submitted_at TEXT NOT NULL,
 started_at TEXT,
 completed_at TEXT,
 gate_results_json BLOB,
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
	State                        jobState
	SubmittedAt                  time.Time
	StartedAt                    *time.Time
	CompletedAt                  *time.Time
	GateResults                  []gatecontract.GateResult
	Error                        string
}

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
	return &coordinatorStore{db: db}, nil
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
) (coordinatorJobRecord, error) {
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("encode coordinator plan: %w", err)
	}
	now := time.Now().UTC()
	result, err := store.db.ExecContext(ctx, `INSERT INTO coordinator_jobs (
invocation_id, job_id, repository_root, plan_json, profile, job_source_tree_sha, state, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, invocationID, jobID, repositoryRoot, planJSON, plan.Profile,
		plan.Source.SourceTreeSHA, jobStateQueued, now.Format(time.RFC3339Nano))
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
		JobSourceTreeSHA: plan.Source.SourceTreeSHA, State: jobStateQueued, SubmittedAt: now,
	}, nil
}

func (store *coordinatorStore) job(ctx context.Context, jobID string) (coordinatorJobRecord, error) {
	row := store.db.QueryRowContext(ctx, `SELECT invocation_id, job_id, enqueue_sequence, repository_root,
plan_json, profile, job_source_tree_sha, image_provenance_source_tree_sha, state, submitted_at,
started_at, completed_at, gate_results_json, error_text FROM coordinator_jobs WHERE job_id = ?`, jobID)
	return scanCoordinatorJob(row, jobID)
}

func (store *coordinatorStore) startJob(ctx context.Context, jobID string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET state = ?, started_at = ?
WHERE job_id = ? AND state = ?`, jobStateStarted, time.Now().UTC().Format(time.RFC3339Nano), jobID, jobStateQueued)
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

func (store *coordinatorStore) finishJob(
	ctx context.Context,
	jobID string,
	state jobState,
	results []gatecontract.GateResult,
	errorText string,
) error {
	if !state.terminal() {
		return fmt.Errorf("%w: finish state %q is not terminal", errCoordinatorState, state)
	}
	resultJSON, err := json.Marshal(results)
	if err != nil {
		return fmt.Errorf("encode coordinator gate results: %w", err)
	}
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_jobs SET state = ?, completed_at = ?,
gate_results_json = ?, error_text = ? WHERE job_id = ? AND state IN (?, ?)`, state,
		time.Now().UTC().Format(time.RFC3339Nano), resultJSON, errorText, jobID, jobStateQueued, jobStateStarted)
	if err != nil {
		return fmt.Errorf("persist coordinator terminal state: %w", err)
	}
	return requireOneCoordinatorRow(result, "finish coordinator job")
}

// scanCoordinatorJob 严格恢复 plan、时间和结果字段，不接受持久化漂移。
func scanCoordinatorJob(row *sql.Row, jobID string) (coordinatorJobRecord, error) {
	var record coordinatorJobRecord
	var planJSON, resultsJSON []byte
	var profile, state, submitted string
	var started, completed sql.NullString
	err := row.Scan(&record.InvocationID, &record.JobID, &record.EnqueueSequence, &record.RepositoryRoot,
		&planJSON, &profile, &record.JobSourceTreeSHA, &record.ImageProvenanceSourceTreeSHA,
		&state, &submitted, &started, &completed, &resultsJSON, &record.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return coordinatorJobRecord{}, fmt.Errorf("%w: %q", errCoordinatorNotFound, jobID)
	}
	if err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("read coordinator job: %w", err)
	}
	if err := gatecontract.DecodeStrictJSON(planJSON, &record.Plan); err != nil {
		return coordinatorJobRecord{}, fmt.Errorf("decode persisted coordinator plan: %w", err)
	}
	record.Profile, record.State = gatecontract.Profile(profile), jobState(state)
	if err := decodeCoordinatorTimes(&record, submitted, started, completed); err != nil {
		return coordinatorJobRecord{}, err
	}
	if len(resultsJSON) > 0 {
		if err := decodeCoordinatorJSON(resultsJSON, &record.GateResults); err != nil {
			return coordinatorJobRecord{}, fmt.Errorf("decode persisted gate results: %w", err)
		}
	}
	if err := validateCoordinatorRecord(record); err != nil {
		return coordinatorJobRecord{}, err
	}
	return record, nil
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

func decodeCoordinatorTimes(record *coordinatorJobRecord, submitted string, started, completed sql.NullString) error {
	var err error
	record.SubmittedAt, err = time.Parse(time.RFC3339Nano, submitted)
	if err != nil {
		return fmt.Errorf("parse coordinator submitted_at: %w", err)
	}
	if record.StartedAt, err = parseCoordinatorTime(started); err != nil {
		return err
	}
	record.CompletedAt, err = parseCoordinatorTime(completed)
	return err
}

// validateCoordinatorRecord 拒绝 plan、profile、tree 或状态字段的任何持久化漂移。
func validateCoordinatorRecord(record coordinatorJobRecord) error {
	if record.InvocationID == "" || record.JobID == "" || record.EnqueueSequence == 0 {
		return fmt.Errorf("%w: persisted invocation/job identity is incomplete", errCoordinatorState)
	}
	if err := record.Plan.Validate(); err != nil {
		return fmt.Errorf("%w: persisted plan is invalid: %v", errCoordinatorState, err)
	}
	if record.Profile != record.Plan.Profile || record.JobSourceTreeSHA != record.Plan.Source.SourceTreeSHA {
		return fmt.Errorf("%w: persisted job fields drifted from plan", errCoordinatorState)
	}
	if record.State != jobStateQueued && record.State != jobStateStarted && !record.State.terminal() {
		return fmt.Errorf("%w: unknown persisted job state %q", errCoordinatorState, record.State)
	}
	return nil
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
		Terminal: record.State.terminal(),
	}
}

func (store *coordinatorStore) close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}
