package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// LoadCheckReceipts 恢复并验证一个 job 的完整、成功必跑检查集合。
func (store *DurationLedgerStore) LoadCheckReceipts(jobID string) ([]CheckReceiptRecord, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if strings.TrimSpace(jobID) == "" {
		return nil, errors.New("check receipt job ID is required")
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("begin check receipt read snapshot", err)
	}
	defer transaction.Rollback()
	receipts, err := loadCheckReceiptReadSnapshot(transaction, jobID)
	if err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, mapDurationLedgerSQLiteError("commit check receipt read snapshot", err)
	}
	return receipts, nil
}

// loadCheckReceiptReadSnapshot 在同一只读快照中绑定 run 投影、scope 和回执。
func loadCheckReceiptReadSnapshot(transaction *sql.Tx, jobID string) ([]CheckReceiptRecord, error) {
	run, err := loadRemoteCIRunRow(transaction, jobID)
	if err != nil {
		return nil, err
	}
	if err := loadRemoteCIRunDetails(transaction, jobID, &run); err != nil {
		return nil, err
	}
	catalog, err := loadSQLiteWorkloadCatalog(transaction, run.CatalogDigest)
	if err != nil {
		return nil, err
	}
	index, err := newRemoteCIRunCatalogIndex(catalog.Catalog)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteCIRunScopeRecordCoverage(run, catalog.Catalog, index); err != nil {
		return nil, fmt.Errorf("stored check receipt workload coverage: %w", err)
	}
	receipts, err := loadSQLiteCheckReceiptRecords(transaction, jobID)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteCIExecutionScopePassingCheckReceipts(catalog.Catalog, run.Scope, receipts); err != nil {
		return nil, fmt.Errorf("stored check receipts: %w", err)
	}
	return receipts, nil
}

// loadSQLiteCheckReceiptRecords 在同一 SQLite 快照读取 canonical 顺序的回执，拒绝不完整或漂移字段。
func loadSQLiteCheckReceiptRecords(transaction *sql.Tx, jobID string) ([]CheckReceiptRecord, error) {
	query := fmt.Sprintf(`SELECT run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id,
		required_check, executed, reused, reuse_proof_sha256, passed, force, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256
		FROM %s WHERE job_id = ?
		ORDER BY CASE required_check
			WHEN 'gate' THEN 1 WHEN 'normal' THEN 2 WHEN 'e2e' THEN 3
			WHEN 'race' THEN 4 WHEN 'frontend' THEN 5 WHEN 'dependency' THEN 6
			ELSE 7 END`, cicontract.CheckReceiptsTable)
	rows, err := transaction.Query(query, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query check receipts", err)
	}
	defer rows.Close()
	var receipts []CheckReceiptRecord
	for rows.Next() {
		receipt, err := scanSQLiteCheckReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate check receipts", err)
	}
	return receipts, nil
}

// scanSQLiteCheckReceipt 将单行回执转换为完整记录，禁止无效 generation 或布尔编码进入验证层。
func scanSQLiteCheckReceipt(rows *sql.Rows) (CheckReceiptRecord, error) {
	var check, acceptedGenerationText string
	var executed, reused, passed, force int
	var startedAtMS, completedAtMS, durationMS int64
	var receipt CheckReceiptRecord
	if err := rows.Scan(&receipt.RunID, &receipt.JobID, &receipt.CandidateTreeSHA, &receipt.AgentTokenDigest, &acceptedGenerationText,
		&receipt.AcceptedSnapshotID, &check, &executed, &reused, &receipt.ReuseProofSHA256, &passed, &force, &startedAtMS, &completedAtMS, &durationMS, &receipt.ReceiptSHA256); err != nil {
		return CheckReceiptRecord{}, mapDurationLedgerSQLiteError("scan check receipt", err)
	}
	generation, err := strconv.ParseUint(acceptedGenerationText, 10, 64)
	if err != nil || generation == 0 {
		return CheckReceiptRecord{}, errors.New("stored check receipt accepted generation is invalid")
	}
	receipt.AcceptedGeneration = generation
	receipt.RequiredCheck = cicontract.RequiredCheck(check)
	receipt.Executed, receipt.Reused, receipt.Passed = executed == 1, reused == 1, passed == 1
	if force != 0 && force != 1 {
		return CheckReceiptRecord{}, errors.New("stored check receipt force identity is invalid")
	}
	receipt.Force = force == 1
	receipt.StartedAt, receipt.CompletedAt = unixMilliUTC(startedAtMS), unixMilliUTC(completedAtMS)
	receipt.Duration = time.Duration(durationMS) * time.Millisecond
	return receipt, nil
}

// verifySQLiteCheckReceiptAuthority 验证回执与持久化 run 身份完全一致。
func verifySQLiteCheckReceiptAuthority(transaction *sql.Tx, receipts []CheckReceiptRecord) error {
	first := receipts[0]
	var tree, imageCacheSnapshotID, agentTokenDigest string
	var force int
	if err := transaction.QueryRow(`SELECT runs.source_tree_sha, runs.image_cache_snapshot_id, runs.force, identities.agent_token_digest FROM ci_runs AS runs INNER JOIN ci_run_agent_identities AS identities ON identities.job_id = runs.job_id WHERE runs.job_id = ?`, first.JobID).Scan(&tree, &imageCacheSnapshotID, &force, &agentTokenDigest); err != nil {
		return mapDurationLedgerSQLiteError("load check receipt run authority", err)
	}
	if tree != first.CandidateTreeSHA {
		return errors.New("check receipt candidate tree does not match run authority")
	}
	if imageCacheSnapshotID != first.AcceptedSnapshotID {
		return errors.New("check receipt accepted snapshot does not match run authority")
	}
	if force != 0 && force != 1 {
		return errors.New("check receipt run force identity is invalid")
	}
	if (force == 1) != first.Force {
		return errors.New("check receipt force mode does not match run authority")
	}
	return verifySQLiteCheckReceiptAgentIdentity(agentTokenDigest, first.AgentTokenDigest)
}

// verifySQLiteCheckReceiptAgentIdentity 拒绝回执携带与同一 SQLite run 不一致的 agent digest。
func verifySQLiteCheckReceiptAgentIdentity(storedDigest, receiptDigest string) error {
	if storedDigest != receiptDigest {
		return errors.New("check receipt agent token digest does not match run authority")
	}
	return nil
}

func unixMilliUTC(value int64) time.Time { return time.UnixMilli(value).UTC() }

// RemoteCITargetBinding 是 authoritative finalizer 后续消费的 immutable normal portable target 身份。
// 本卡只提供独立 SQLite record/readback，不接入 Coordinator 或 finalizer。
type RemoteCITargetBinding struct {
	JobID                 string
	RepositoryID          string
	ObjectFormat          GitObjectFormat
	CommitSHA             string
	TreeSHA               string
	MountPath             string
	ManifestPath          string
	ManifestSHA256        string
	ManifestSchemaVersion string
	ManifestABIDigest     string
	ManifestDigest        string
	Profile               Profile
	// ProfileWorkloadSetDigest 必须是 WorkloadCatalogDigest(mapped selected profile catalog)，
	// 不得以 manifest digest 或 declared command digest 替代。
	ProfileWorkloadSetDigest string
}

// Validate 拒绝隐式 target、非 canonical Git/manifest identity 与 profile workload-set 漂移。
func (binding RemoteCITargetBinding) Validate() error {
	if err := validateRemoteCITargetBindingToken("job ID", binding.JobID, 256); err != nil {
		return err
	}
	if err := validateRemoteCITargetBindingToken("repository ID", binding.RepositoryID, 128); err != nil {
		return err
	}
	if remoteCITargetBindingImplicitToken(binding.RepositoryID) {
		return errors.New("remote target repository ID cannot be implicit")
	}
	if err := validateOID("remote target commit SHA", binding.CommitSHA, binding.ObjectFormat, false); err != nil {
		return err
	}
	if err := validateOID("remote target tree SHA", binding.TreeSHA, binding.ObjectFormat, false); err != nil {
		return err
	}
	if err := validateRemoteCITargetBindingRelativePath("mount path", binding.MountPath); err != nil {
		return err
	}
	if err := validateRemoteCITargetBindingRelativePath("manifest path", binding.ManifestPath); err != nil {
		return err
	}
	if err := validateRemoteCITargetBindingDigests(binding); err != nil {
		return err
	}
	if binding.ManifestSchemaVersion != cicontract.RemoteCITargetBindingManifestSchemaVersion {
		return fmt.Errorf("remote target manifest schema must equal %q", cicontract.RemoteCITargetBindingManifestSchemaVersion)
	}
	return binding.Profile.Validate()
}

func validateRemoteCITargetBindingToken(name, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("remote target %s is invalid", name)
	}
	return nil
}

func remoteCITargetBindingImplicitToken(value string) bool {
	return value == "default" || value == "cwd" || value == "HEAD"
}

func validateRemoteCITargetBindingDigest(name, value string) error {
	if err := validateDigest(name, value); err != nil {
		return err
	}
	if strings.TrimPrefix(value, "sha256:") == strings.Repeat("0", 64) {
		return fmt.Errorf("%s must not be the zero digest", name)
	}
	return nil
}

func validateRemoteCITargetBindingDigests(binding RemoteCITargetBinding) error {
	digests := []struct {
		name, value string
	}{
		{"manifest SHA-256", binding.ManifestSHA256},
		{"manifest ABI digest", binding.ManifestABIDigest},
		{"manifest digest", binding.ManifestDigest},
		{"profile workload-set digest", binding.ProfileWorkloadSetDigest},
	}
	seen := make(map[string]string, len(digests))
	for _, digest := range digests {
		if err := validateRemoteCITargetBindingDigest(digest.name, digest.value); err != nil {
			return err
		}
		if previous, exists := seen[digest.value]; exists {
			return fmt.Errorf("%s must not reuse %s", digest.name, previous)
		}
		seen[digest.value] = digest.name
	}
	return nil
}

func validateRemoteCITargetBindingRelativePath(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "\\") || path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || remoteCITargetBindingImplicitToken(value) || strings.HasPrefix(value, "../") {
		return fmt.Errorf("remote target %s must be a clean repository-relative path", name)
	}
	return nil
}

// RecordRemoteCITargetBinding 只允许为已有 ci_runs job 插入一次 binding，并在同一事务精确回读。
func (store *DurationLedgerStore) RecordRemoteCITargetBinding(binding RemoteCITargetBinding) (RemoteCITargetBinding, error) {
	if store == nil {
		return RemoteCITargetBinding{}, errors.New("duration ledger store is nil")
	}
	if err := binding.Validate(); err != nil {
		return RemoteCITargetBinding{}, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return RemoteCITargetBinding{}, err
	}
	defer database.Close()
	var readback RemoteCITargetBinding
	err = withSQLiteWriteTransaction(database, "record remote CI target binding", func(transaction *sql.Tx) error {
		query := fmt.Sprintf(`INSERT INTO %s (job_id, repository_id, object_format, commit_sha, tree_sha, mount_path, manifest_path, manifest_sha256, manifest_schema_version, manifest_abi_digest, manifest_digest, profile, profile_workload_set_digest) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, cicontract.RemoteRunTargetBindingsTable)
		if _, err := transaction.Exec(query, binding.JobID, binding.RepositoryID, binding.ObjectFormat, binding.CommitSHA, binding.TreeSHA, binding.MountPath, binding.ManifestPath, binding.ManifestSHA256, binding.ManifestSchemaVersion, binding.ManifestABIDigest, binding.ManifestDigest, binding.Profile, binding.ProfileWorkloadSetDigest); err != nil {
			return mapDurationLedgerSQLiteError("insert remote CI target binding", err)
		}
		var err error
		readback, err = loadSQLiteRemoteCITargetBinding(transaction, binding.JobID)
		if err != nil {
			return err
		}
		if readback != binding {
			return errors.New("remote CI target binding exact readback drifted")
		}
		return nil
	})
	return readback, err
}

// LoadRemoteCITargetBinding 在一个只读快照中返回完整 binding；缺失 side row 必须 fail-fast。
func (store *DurationLedgerStore) LoadRemoteCITargetBinding(jobID string) (RemoteCITargetBinding, error) {
	if store == nil {
		return RemoteCITargetBinding{}, errors.New("duration ledger store is nil")
	}
	if err := validateRemoteCITargetBindingToken("job ID", jobID, 256); err != nil {
		return RemoteCITargetBinding{}, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return RemoteCITargetBinding{}, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RemoteCITargetBinding{}, mapDurationLedgerSQLiteError("begin remote CI target binding read", err)
	}
	defer transaction.Rollback()
	binding, err := loadSQLiteRemoteCITargetBinding(transaction, jobID)
	if err != nil {
		return RemoteCITargetBinding{}, err
	}
	if err := transaction.Commit(); err != nil {
		return RemoteCITargetBinding{}, mapDurationLedgerSQLiteError("commit remote CI target binding read", err)
	}
	return binding, nil
}

func loadSQLiteRemoteCITargetBinding(transaction *sql.Tx, jobID string) (RemoteCITargetBinding, error) {
	query := fmt.Sprintf(`SELECT job_id, repository_id, object_format, commit_sha, tree_sha, mount_path, manifest_path, manifest_sha256, manifest_schema_version, manifest_abi_digest, manifest_digest, profile, profile_workload_set_digest FROM %s WHERE job_id = ?`, cicontract.RemoteRunTargetBindingsTable)
	var binding RemoteCITargetBinding
	var objectFormat, profile string
	err := transaction.QueryRow(query, jobID).Scan(&binding.JobID, &binding.RepositoryID, &objectFormat, &binding.CommitSHA, &binding.TreeSHA, &binding.MountPath, &binding.ManifestPath, &binding.ManifestSHA256, &binding.ManifestSchemaVersion, &binding.ManifestABIDigest, &binding.ManifestDigest, &profile, &binding.ProfileWorkloadSetDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteCITargetBinding{}, fmt.Errorf("remote CI target binding for job %q is missing", jobID)
	}
	if err != nil {
		return RemoteCITargetBinding{}, mapDurationLedgerSQLiteError("load remote CI target binding", err)
	}
	binding.ObjectFormat = GitObjectFormat(objectFormat)
	binding.Profile = Profile(profile)
	if err := binding.Validate(); err != nil {
		return RemoteCITargetBinding{}, fmt.Errorf("stored remote CI target binding: %w", err)
	}
	return binding, nil
}
