package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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
	AgentTokenDigest             string
	Entrypoint                   CIEntrypointID
	Profile                      Profile
	PlanDigest                   string
	CatalogDigest                string
	AcceptedGeneration           uint64
	ImageCacheSnapshotID         string
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
	WorkloadResults              []RemoteCIWorkloadResult
	Warnings                     []string
	TimingWarnings               []RemoteCITimingWarning
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

// Validate 拒绝缺失或畸形的实际分片 CPU、内存与规格身份。
// 校准固定规格由 remote CI 请求与 checkpoint 额外严格绑定，normal 则保留策略实际选择。
func (resources RemoteCIShardResources) Validate() error {
	if strings.TrimSpace(resources.ClassID) == "" || resources.ClassID != strings.TrimSpace(resources.ClassID) ||
		resources.CPU <= 0 || resources.MemoryGiB <= 0 ||
		math.IsNaN(resources.CPU) || math.IsInf(resources.CPU, 0) ||
		math.IsNaN(resources.MemoryGiB) || math.IsInf(resources.MemoryGiB, 0) {
		return errors.New("remote CI shard class, CPU, and memory are required")
	}
	return nil
}

// RecordProvisionalRemoteCIRun 原子替换一个尚未最终化的 job 投影。
// 权威标记、check receipt 与 workload PASS evidence 只能由
// FinalizeRemoteCIRunAuthorityWithSamples 在同一 SQLite 事务中写入。
func (store *DurationLedgerStore) RecordProvisionalRemoteCIRun(record RemoteCIRunRecord) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if record.Authoritative {
		return errors.New("provisional remote CI run must not be authoritative")
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
		if err := store.appendDurationLedgerObservationEvent(
			transaction,
			durationLedgerObservationEventRemoteRunPersist,
			record.JobID,
			strconv.FormatUint(record.AcceptedGeneration, 10),
			record,
		); err != nil {
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

// verifySQLiteRemoteCIRunIdentity 校验已存在 run 与写入请求的不可变字段一致。
func verifySQLiteRemoteCIRunIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return fmt.Errorf("remote CI agent token digest: %w", err)
	}
	var stored sqliteRemoteCIRunIdentity
	err := transaction.QueryRow(`SELECT entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, started_at_unix_ms FROM ci_runs WHERE job_id = ?`, record.JobID).Scan(&stored.Entrypoint, &stored.Profile, &stored.PlanDigest, &stored.CatalogDigest, &stored.AcceptedGeneration, &stored.ImageCacheSnapshotID, &stored.SourceTreeSHA, &stored.CandidateGateSourceSHA256, &stored.CandidateGateToolchainSHA256, &stored.RunnerImage, &stored.StartedAtUnixMS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing remote CI run identity", err)
	}
	if stored != newSQLiteRemoteCIRunIdentity(record) {
		return fmt.Errorf("remote CI job %q conflicts with immutable run identity", record.JobID)
	}
	return verifySQLiteRemoteCIAgentIdentity(transaction, record)
}

// sqliteRemoteCIRunIdentity 保存 SQLite 中不可变的运行身份投影。
type sqliteRemoteCIRunIdentity struct {
	Entrypoint                   string
	Profile                      string
	PlanDigest                   string
	CatalogDigest                string
	AcceptedGeneration           string
	ImageCacheSnapshotID         string
	SourceTreeSHA                string
	CandidateGateSourceSHA256    string
	CandidateGateToolchainSHA256 string
	RunnerImage                  string
	StartedAtUnixMS              int64
}

// newSQLiteRemoteCIRunIdentity 将写入请求转换为可直接比较的 SQLite 身份投影。
func newSQLiteRemoteCIRunIdentity(record RemoteCIRunRecord) sqliteRemoteCIRunIdentity {
	return sqliteRemoteCIRunIdentity{
		Entrypoint:                   string(record.Entrypoint),
		Profile:                      string(record.Profile),
		PlanDigest:                   record.PlanDigest,
		CatalogDigest:                record.CatalogDigest,
		AcceptedGeneration:           strconv.FormatUint(record.AcceptedGeneration, 10),
		ImageCacheSnapshotID:         record.ImageCacheSnapshotID,
		SourceTreeSHA:                record.SourceTreeSHA,
		CandidateGateSourceSHA256:    record.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: record.CandidateGateToolchainSHA256,
		RunnerImage:                  record.RunnerImage,
		StartedAtUnixMS:              record.StartedAt.UTC().UnixMilli(),
	}
}

// verifySQLiteRemoteCIAgentIdentity 校验每个 run 都有唯一、不可变的 agent digest。
func verifySQLiteRemoteCIAgentIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	var digest string
	err := transaction.QueryRow(`SELECT agent_token_digest FROM ci_run_agent_identities WHERE job_id = ?`, record.JobID).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("remote CI job %q lacks required agent identity", record.JobID)
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing remote CI agent identity", err)
	}
	if err := cicontract.ValidateAgentTokenDigest(digest); err != nil {
		return fmt.Errorf("stored remote CI agent token digest: %w", err)
	}
	if digest != record.AgentTokenDigest {
		return fmt.Errorf("remote CI job %q conflicts with immutable agent identity", record.JobID)
	}
	return nil
}
