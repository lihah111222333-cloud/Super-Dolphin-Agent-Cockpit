package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// ErrRemoteCIRunNotFound 表示查询投影尚未收录指定 job。
var ErrRemoteCIRunNotFound = errors.New("remote CI run not found")

// RemoteCIRunRecord 是一次协调器执行及其分片和 gate 终态的查询投影。
type RemoteCIRunRecord struct {
	JobID                        string
	RequesterFingerprint         RequesterFingerprint
	Entrypoint                   CIEntrypointID
	Profile                      Profile
	PlanDigest                   string
	CatalogDigest                string
	AcceptedGeneration           uint64
	SourceTreeSHA                string
	CandidateGateSourceSHA256    string
	CandidateGateToolchainSHA256 string
	RunnerImage                  string
	Status                       ResultStatus
	Authoritative                bool
	StartedAt                    time.Time
	CompletedAt                  time.Time
	CleanupComplete              bool
	ErrorText                    string
	Shards                       []RemoteCIShardRecord
	Executions                   []PlanGateExecution
	WorkloadExecutions           []PlanGateExecution
	Warnings                     []string
	TimingObservations           []TimingObservation
}

// RemoteCIShardRecord 保存一个远程分片的稳定云资源身份和终态。
type RemoteCIShardRecord struct {
	ShardIdentity         string
	ContainerGroup        string
	ContainerStatus       string
	Workloads             []GateID
	MaterializationTiming ShardMaterializationTiming
	Resources             RemoteCIShardResources
}

// RemoteCIShardResources is the scheduled CPU/memory evidence for one ECI shard.
type RemoteCIShardResources struct {
	ClassID   string  `json:"class_id"`
	CPU       float64 `json:"cpu"`
	MemoryGiB float64 `json:"memory_gib"`
}

func (resources RemoteCIShardResources) Validate() error {
	return cicontract.ValidateCalibrationResources(resources.ClassID, resources.CPU, resources.MemoryGiB)
}

// RecordRemoteCIRun 原子替换一个 job 的 run、shard、workload 和 gate 查询投影。
func (store *DurationLedgerStore) RecordRemoteCIRun(record RemoteCIRunRecord) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if err := validateRemoteCIRunRecord(record); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "remote CI run", func(transaction *sql.Tx) error {
		if err := requireHistoricallyAcceptedGeneration(transaction, record.AcceptedGeneration); err != nil {
			return err
		}
		if err := storeSQLiteRemoteCIRunProjection(transaction, record, store.nowFunc); err != nil {
			return err
		}
		return compactDurationLedgerAuthority(transaction)
	})
}

// LoadRemoteCIRun 按 job ID 从 SQLite 恢复 run、shard 和 gate 终态。
func (store *DurationLedgerStore) LoadRemoteCIRun(jobID string) (RemoteCIRunRecord, error) {
	if store == nil {
		return RemoteCIRunRecord{}, errors.New("duration ledger store is nil")
	}
	if strings.TrimSpace(jobID) == "" {
		return RemoteCIRunRecord{}, errors.New("remote CI job ID is required")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return RemoteCIRunRecord{}, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RemoteCIRunRecord{}, mapDurationLedgerSQLiteError("begin remote CI run read snapshot", err)
	}
	defer transaction.Rollback()
	record, err := loadRemoteCIRunRow(transaction, jobID)
	if err != nil {
		return RemoteCIRunRecord{}, err
	}
	if err := loadRemoteCIRunDetails(transaction, jobID, &record); err != nil {
		return RemoteCIRunRecord{}, err
	}
	if err := transaction.Commit(); err != nil {
		return RemoteCIRunRecord{}, mapDurationLedgerSQLiteError("commit remote CI run read snapshot", err)
	}
	return record, nil
}

// ListRemoteCIRunIDsByRequester 按索引返回一个逻辑发起方最近的远程运行。
func (store *DurationLedgerStore) ListRemoteCIRunIDsByRequester(fingerprint RequesterFingerprint, limit int) ([]string, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := fingerprint.Validate(); err != nil {
		return nil, fmt.Errorf("requester fingerprint: %w", err)
	}
	if limit <= 0 || limit > 1_000 {
		return nil, errors.New("requester run query limit must be between 1 and 1000")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	rows, err := database.Query(`SELECT job_id FROM ci_run_requesters WHERE requester_fingerprint = ? ORDER BY started_at_unix_ms DESC, job_id DESC LIMIT ?`, fingerprint.String(), limit)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI requester runs", err)
	}
	defer rows.Close()
	jobIDs := make([]string, 0)
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI requester run", err)
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI requester runs", err)
	}
	return jobIDs, nil
}

// verifySQLiteRemoteCIRunIdentity 校验已存在 run 与写入请求的不可变字段一致。
func verifySQLiteRemoteCIRunIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	var entrypoint, profile, planDigest, catalogDigest, acceptedGeneration, sourceTreeSHA, candidateGateSourceSHA256, candidateGateToolchainSHA256, runnerImage string
	var startedAtUnixMS int64
	err := transaction.QueryRow(`SELECT entrypoint, profile, plan_digest, catalog_digest, accepted_generation, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, started_at_unix_ms FROM ci_runs WHERE job_id = ?`, record.JobID).Scan(&entrypoint, &profile, &planDigest, &catalogDigest, &acceptedGeneration, &sourceTreeSHA, &candidateGateSourceSHA256, &candidateGateToolchainSHA256, &runnerImage, &startedAtUnixMS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing remote CI run identity", err)
	}
	if entrypoint != string(record.Entrypoint) || profile != string(record.Profile) || planDigest != record.PlanDigest || catalogDigest != record.CatalogDigest || acceptedGeneration != strconv.FormatUint(record.AcceptedGeneration, 10) || sourceTreeSHA != record.SourceTreeSHA || candidateGateSourceSHA256 != record.CandidateGateSourceSHA256 || candidateGateToolchainSHA256 != record.CandidateGateToolchainSHA256 || runnerImage != record.RunnerImage || startedAtUnixMS != record.StartedAt.UTC().UnixMilli() {
		return fmt.Errorf("remote CI job %q conflicts with immutable run identity", record.JobID)
	}
	return verifySQLiteRemoteCIRequesterIdentity(transaction, record)
}

// verifySQLiteRemoteCIRequesterIdentity 校验可选请求者投影与 run 的不可变身份一致。
func verifySQLiteRemoteCIRequesterIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	var requesterFingerprint string
	err := transaction.QueryRow(`SELECT requester_fingerprint FROM ci_run_requesters WHERE job_id = ?`, record.JobID).Scan(&requesterFingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		if record.RequesterFingerprint != "" {
			return fmt.Errorf("remote CI job %q conflicts with immutable requester identity", record.JobID)
		}
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing remote CI requester identity", err)
	}
	if requesterFingerprint != record.RequesterFingerprint.String() {
		return fmt.Errorf("remote CI job %q conflicts with immutable requester identity", record.JobID)
	}
	return nil
}
