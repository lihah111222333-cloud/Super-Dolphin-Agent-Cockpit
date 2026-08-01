package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrRemoteCIRunNotFound 表示查询投影尚未收录指定 job。
var ErrRemoteCIRunNotFound = errors.New("remote CI run not found")

// WorkloadPassProof 是已验证 OSS PASS 标记在 SQLite 中的可查询投影。
type WorkloadPassProof struct {
	IdentityDigest    string
	WorkloadID        string
	ExecutionDigest   string
	InputDigest       string
	EnvironmentDigest string
	ObjectKey         string
	ObservedAt        time.Time
}

// WorkloadFingerprintRecord 保存一次目标生产代码指纹计算结果。
type WorkloadFingerprintRecord struct {
	IdentityDigest    string
	WorkloadID        string
	ExecutionDigest   string
	InputDigest       string
	EnvironmentDigest string
	SourceTreeSHA     string
	ObservedAt        time.Time
}

// WorkloadPassCandidateQuery 描述一次旧 PASS 证据兼容性查询。
type WorkloadPassCandidateQuery struct {
	WorkloadID        string
	ExecutionDigest   string
	EnvironmentDigest string
}

// WorkloadPassCandidate 关联旧 PASS 证据与其实际验证过的源码树。
type WorkloadPassCandidate struct {
	Proof         WorkloadPassProof
	SourceTreeSHA string
}

// RemoteCIRunRecord 是一次协调器执行及其分片和 gate 终态的查询投影。
type RemoteCIRunRecord struct {
	JobID                                   string
	RequesterFingerprint                    RequesterFingerprint
	Entrypoint                              CIEntrypointID
	Profile                                 Profile
	PlanDigest                              string
	CatalogDigest                           string
	SourceTreeSHA                           string
	CandidateCLIManifestSHA256              string
	CandidateTestBinaryReceiptBindingDigest string
	RunnerImage                             string
	Status                                  ResultStatus
	Authoritative                           bool
	StartedAt                               time.Time
	CompletedAt                             time.Time
	CleanupComplete                         bool
	ErrorText                               string
	Shards                                  []RemoteCIShardRecord
	Executions                              []PlanGateExecution
	WorkloadExecutions                      []PlanGateExecution
	ReusedWorkloads                         []GateID
	CacheMisses                             []GateID
	Warnings                                []string
	PhaseTimings                            []RemoteCIPhaseTiming
	CandidateTestBinaryBuilds               []CandidateTestBinaryBuildRecord
}

// CandidateTestBinaryBuildRecord is one coordinator-validated package build; it is never charged to an individual test execution.
type CandidateTestBinaryBuildRecord struct {
	CandidateTree                   string
	Package                         string
	Mode                            string
	Platform                        string
	GoToolchain                     string
	CGOEnabled                      bool
	ToolchainSHA256                 string
	BuildFlags                      []string
	CompileClosureSHA256            string
	ManifestSHA256                  string
	ArtifactSHA256                  string
	BinarySize                      int64
	GoListWallMS                    uint64
	BuildWallMS                     uint64
	CompileActionMS                 uint64
	LinkActionMS                    uint64
	CompileCriticalWallMS           uint64
	GOCachePrivateHits              uint64
	GOCachePrivateRootIdentity      string
	GOCacheBaselineHitsByGeneration map[string]uint64
	GOCacheBaselineHitRecords       []CandidateTestBinaryCacheGenerationRecord
	GOCacheMisses                   uint64
	GOCachePuts                     uint64
}

// CandidateTestBinaryCacheGenerationRecord retains the baseline cache provenance
// observed for one immutable generation.
type CandidateTestBinaryCacheGenerationRecord struct {
	Generation           uint64 `json:"generation"`
	Hits                 uint64 `json:"hits"`
	AnchorGeneration     uint64 `json:"anchor_generation"`
	AnchorManifestDigest string `json:"anchor_manifest_digest"`
	ManifestDigest       string `json:"manifest_digest"`
	CacheRootIdentity    string `json:"cache_root_identity"`
}

// RemoteCIShardRecord 保存一个远程分片的稳定云资源身份和终态。
type RemoteCIShardRecord struct {
	ShardIdentity         string
	ContainerGroup        string
	ContainerStatus       string
	Workloads             []GateID
	MaterializationTiming ShardMaterializationTiming
}

// LookupWorkloadPassProofs 用内容身份主键批量查询已验证 PASS，不访问 OSS。
func (store *DurationLedgerStore) LookupWorkloadPassProofs(
	identityDigests []string,
) (map[string]WorkloadPassProof, error) {
	proofs := make(map[string]WorkloadPassProof)
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if len(identityDigests) == 0 {
		return proofs, nil
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	const queryChunkSize = 400
	for start := 0; start < len(identityDigests); start += queryChunkSize {
		end := min(start+queryChunkSize, len(identityDigests))
		if err := lookupWorkloadPassProofChunk(database, identityDigests[start:end], proofs); err != nil {
			return nil, err
		}
	}
	return proofs, nil
}

// LookupCompatibleWorkloadPassCandidates 查询同目标、同执行内容和同环境的最近 PASS。
// 调用方必须用当前指纹算法重算 SourceTreeSHA 后才能提升这些历史证据。
func (store *DurationLedgerStore) LookupCompatibleWorkloadPassCandidates(
	requests []WorkloadPassCandidateQuery,
	perWorkloadLimit int,
) (map[string][]WorkloadPassCandidate, error) {
	candidates := make(map[string][]WorkloadPassCandidate)
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if len(requests) == 0 {
		return candidates, nil
	}
	if perWorkloadLimit <= 0 || perWorkloadLimit > 8 {
		return nil, errors.New("compatible PASS candidate limit must be between 1 and 8")
	}
	unique, err := uniqueWorkloadPassCandidateQueries(requests)
	if err != nil {
		return nil, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()

	const queryChunkSize = 100
	for start := 0; start < len(unique); start += queryChunkSize {
		end := min(start+queryChunkSize, len(unique))
		if err := lookupCompatibleWorkloadPassCandidateChunk(
			database,
			unique[start:end],
			perWorkloadLimit,
			candidates,
		); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

// uniqueWorkloadPassCandidateQueries 验证请求并拒绝同 workload 的冲突身份。
func uniqueWorkloadPassCandidateQueries(requests []WorkloadPassCandidateQuery) ([]WorkloadPassCandidateQuery, error) {
	unique := make([]WorkloadPassCandidateQuery, 0, len(requests))
	byWorkload := make(map[string]WorkloadPassCandidateQuery, len(requests))
	for index, request := range requests {
		if err := validateWorkloadPassCandidateQuery(request); err != nil {
			return nil, fmt.Errorf("compatible PASS request[%d]: %w", index, err)
		}
		previous, exists := byWorkload[request.WorkloadID]
		if exists && previous != request {
			return nil, fmt.Errorf("compatible PASS workload %q has conflicting query identities", request.WorkloadID)
		}
		if !exists {
			byWorkload[request.WorkloadID] = request
			unique = append(unique, request)
		}
	}
	return unique, nil
}

// RecordWorkloadPassProofs 在同一事务中写入 PASS 查询投影并推进查询 revision。
func (store *DurationLedgerStore) RecordWorkloadPassProofs(proofs []WorkloadPassProof) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if len(proofs) == 0 {
		return nil
	}
	for index, proof := range proofs {
		if err := validateWorkloadPassProof(proof); err != nil {
			return fmt.Errorf("PASS proof[%d]: %w", index, err)
		}
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "PASS proof", func(transaction *sql.Tx) error {
		for _, proof := range proofs {
			if err := verifySQLiteWorkloadPassProofIdentity(transaction, proof); err != nil {
				return err
			}
			if _, err := transaction.Exec(`
			INSERT INTO ci_workload_pass_proofs (
				identity_digest, workload_id, execution_digest, input_digest,
				environment_digest, object_key, observed_at_unix_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(identity_digest) DO UPDATE SET
				object_key = excluded.object_key,
				observed_at_unix_ms = excluded.observed_at_unix_ms
		`,
				proof.IdentityDigest,
				proof.WorkloadID,
				proof.ExecutionDigest,
				proof.InputDigest,
				proof.EnvironmentDigest,
				proof.ObjectKey,
				proof.ObservedAt.UTC().UnixMilli(),
			); err != nil {
				return mapDurationLedgerSQLiteError("store PASS proof", err)
			}
			if err := recordSQLiteWorkloadIdentityAlias(
				transaction, proof.IdentityDigest, proof.WorkloadID, proof.ObservedAt,
			); err != nil {
				return err
			}
		}
		return advanceCIQueryRevision(transaction, store.nowFunc().UTC())
	})
}

// RecordWorkloadFingerprints 记录所有已计算目标，无论其是否命中 PASS。
func (store *DurationLedgerStore) RecordWorkloadFingerprints(
	records []WorkloadFingerprintRecord,
) error {
	if store == nil {
		return errors.New("duration ledger store is nil")
	}
	if len(records) == 0 {
		return nil
	}
	for index, record := range records {
		if err := validateWorkloadFingerprintRecord(record); err != nil {
			return fmt.Errorf("workload fingerprint[%d]: %w", index, err)
		}
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "workload fingerprint", func(transaction *sql.Tx) error {
		for _, record := range records {
			if err := recordSQLiteWorkloadFingerprint(transaction, record); err != nil {
				return err
			}
		}
		return advanceCIQueryRevision(transaction, store.nowFunc().UTC())
	})
}

func recordSQLiteWorkloadFingerprint(transaction *sql.Tx, record WorkloadFingerprintRecord) error {
	if err := verifySQLiteWorkloadFingerprintIdentity(transaction, record); err != nil {
		return err
	}
	if _, err := transaction.Exec(`
		INSERT INTO ci_workload_fingerprints (
			identity_digest, workload_id, execution_digest, input_digest,
			environment_digest, source_tree_sha, observed_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(identity_digest) DO NOTHING
	`, record.IdentityDigest, record.WorkloadID, record.ExecutionDigest, record.InputDigest,
		record.EnvironmentDigest, record.SourceTreeSHA, record.ObservedAt.UTC().UnixMilli(),
	); err != nil {
		return mapDurationLedgerSQLiteError("store workload fingerprint", err)
	}
	if err := recordSQLiteWorkloadIdentityAlias(
		transaction, record.IdentityDigest, record.WorkloadID, record.ObservedAt,
	); err != nil {
		return err
	}
	if _, err := transaction.Exec(`
		INSERT INTO ci_workload_fingerprint_observations (
			identity_digest, source_tree_sha, observed_at_unix_ms
		) VALUES (?, ?, ?)
		ON CONFLICT(identity_digest, source_tree_sha) DO UPDATE SET
			observed_at_unix_ms = MAX(
				ci_workload_fingerprint_observations.observed_at_unix_ms,
				excluded.observed_at_unix_ms
			)
	`, record.IdentityDigest, record.SourceTreeSHA, record.ObservedAt.UTC().UnixMilli()); err != nil {
		return mapDurationLedgerSQLiteError("store workload fingerprint observation", err)
	}
	return nil
}

// RecordRemoteCIRun 原子替换一个 job 的 run、shard、workload 和 gate 查询投影。
func (store *DurationLedgerStore) RecordRemoteCIRun(record RemoteCIRunRecord) error {
	_, err := store.RecordRemoteCIRunProfiled(record)
	return err
}

// RecordRemoteCIRunProfiled 写入远程运行并返回本次 SQLite 投影的内部阶段计时。
func (store *DurationLedgerStore) RecordRemoteCIRunProfiled(
	record RemoteCIRunRecord,
) ([]RemoteCIPhaseTiming, error) {
	if store == nil {
		return nil, errors.New("duration ledger store is nil")
	}
	if err := validateRemoteCIRunRecord(record); err != nil {
		return nil, err
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	var projectionTimings []RemoteCIPhaseTiming
	err = withSQLiteWriteTransaction(database, "remote CI run", func(transaction *sql.Tx) error {
		attemptTimings, err := storeSQLiteRemoteCIRunProjection(
			transaction,
			record,
			store.nowFunc,
		)
		if err == nil {
			projectionTimings = attemptTimings
		}
		return err
	})
	return projectionTimings, err
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
func (store *DurationLedgerStore) ListRemoteCIRunIDsByRequester(
	fingerprint RequesterFingerprint,
	limit int,
) ([]string, error) {
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
	rows, err := database.Query(`
		SELECT job_id
		FROM ci_run_requesters
		WHERE requester_fingerprint = ?
		ORDER BY started_at_unix_ms DESC, job_id DESC
		LIMIT ?
	`, fingerprint.String(), limit)
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

// verifySQLiteWorkloadPassProofIdentity 拒绝同一内容身份映射到不同 SQLite 记录。
func verifySQLiteWorkloadPassProofIdentity(transaction *sql.Tx, proof WorkloadPassProof) error {
	var existing WorkloadPassProof
	var observedAtMS int64
	err := transaction.QueryRow(`
		SELECT workload_id, execution_digest, input_digest, environment_digest,
			object_key, observed_at_unix_ms
		FROM ci_workload_pass_proofs WHERE identity_digest = ?
	`, proof.IdentityDigest).Scan(
		&existing.WorkloadID,
		&existing.ExecutionDigest,
		&existing.InputDigest,
		&existing.EnvironmentDigest,
		&existing.ObjectKey,
		&observedAtMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing PASS proof", err)
	}
	if existing.ExecutionDigest != proof.ExecutionDigest ||
		existing.InputDigest != proof.InputDigest ||
		existing.EnvironmentDigest != proof.EnvironmentDigest {
		return fmt.Errorf("PASS proof identity %q conflicts with immutable workload identity", proof.IdentityDigest)
	}
	if proof.ObservedAt.UTC().UnixMilli() < observedAtMS {
		return fmt.Errorf("PASS proof identity %q is older than the stored observation", proof.IdentityDigest)
	}
	if proof.ObservedAt.UTC().UnixMilli() == observedAtMS && existing.ObjectKey != proof.ObjectKey {
		return fmt.Errorf("PASS proof identity %q conflicts at the stored observation time", proof.IdentityDigest)
	}
	return nil
}

// verifySQLiteWorkloadFingerprintIdentity 保持 workload 指纹投影的不可变身份。
func verifySQLiteWorkloadFingerprintIdentity(
	transaction *sql.Tx,
	record WorkloadFingerprintRecord,
) error {
	var existing WorkloadFingerprintRecord
	err := transaction.QueryRow(`
		SELECT workload_id, execution_digest, input_digest, environment_digest
		FROM ci_workload_fingerprints WHERE identity_digest = ?
	`, record.IdentityDigest).Scan(
		&existing.WorkloadID,
		&existing.ExecutionDigest,
		&existing.InputDigest,
		&existing.EnvironmentDigest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing workload fingerprint", err)
	}
	if existing.ExecutionDigest != record.ExecutionDigest ||
		existing.InputDigest != record.InputDigest ||
		existing.EnvironmentDigest != record.EnvironmentDigest {
		return fmt.Errorf(
			"workload fingerprint identity %q conflicts with immutable fields",
			record.IdentityDigest,
		)
	}
	return nil
}

// recordSQLiteWorkloadIdentityAlias 记录同一内容身份在不同 CI 场景中的 workload 名称。
func recordSQLiteWorkloadIdentityAlias(
	transaction *sql.Tx,
	identityDigest string,
	workloadID string,
	observedAt time.Time,
) error {
	if _, err := transaction.Exec(`
		INSERT INTO ci_workload_identity_aliases (
			identity_digest, workload_id, observed_at_unix_ms
		) VALUES (?, ?, ?)
		ON CONFLICT(identity_digest, workload_id) DO UPDATE SET
			observed_at_unix_ms = MAX(
				ci_workload_identity_aliases.observed_at_unix_ms,
				excluded.observed_at_unix_ms
			)
	`, identityDigest, workloadID, observedAt.UTC().UnixMilli()); err != nil {
		return mapDurationLedgerSQLiteError("store workload identity alias", err)
	}
	return nil
}

// verifySQLiteRemoteCIRunIdentity 校验已存在 run 与写入请求的不可变字段一致。
func verifySQLiteRemoteCIRunIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	var (
		entrypoint      string
		profile         string
		planDigest      string
		catalogDigest   string
		sourceTreeSHA   string
		runnerImage     string
		startedAtUnixMS int64
	)
	err := transaction.QueryRow(`
		SELECT entrypoint, profile, plan_digest, catalog_digest, source_tree_sha,
			runner_image, started_at_unix_ms
		FROM ci_runs WHERE job_id = ?
	`, record.JobID).Scan(
		&entrypoint,
		&profile,
		&planDigest,
		&catalogDigest,
		&sourceTreeSHA,
		&runnerImage,
		&startedAtUnixMS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load existing remote CI run identity", err)
	}
	if entrypoint != string(record.Entrypoint) ||
		profile != string(record.Profile) ||
		planDigest != record.PlanDigest ||
		catalogDigest != record.CatalogDigest ||
		sourceTreeSHA != record.SourceTreeSHA ||
		runnerImage != record.RunnerImage ||
		startedAtUnixMS != record.StartedAt.UTC().UnixMilli() {
		return fmt.Errorf("remote CI job %q conflicts with immutable run identity", record.JobID)
	}
	return verifySQLiteRemoteCIRequesterIdentity(transaction, record)
}

// verifySQLiteRemoteCIRequesterIdentity 校验可选请求者投影与 run 的不可变身份一致。
func verifySQLiteRemoteCIRequesterIdentity(transaction *sql.Tx, record RemoteCIRunRecord) error {
	var requesterFingerprint string
	err := transaction.QueryRow(`
		SELECT requester_fingerprint
		FROM ci_run_requesters
		WHERE job_id = ?
	`, record.JobID).Scan(&requesterFingerprint)
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

// lookupWorkloadPassProofChunk 在 SQLite 变量上限内装载一组 PASS 证据。
func lookupWorkloadPassProofChunk(
	database *sql.DB,
	identityDigests []string,
	destination map[string]WorkloadPassProof,
) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(identityDigests)), ",")
	arguments := make([]any, len(identityDigests))
	for index, digest := range identityDigests {
		if !isPrefixedSHA256Digest(digest) {
			return fmt.Errorf("PASS proof identity digest %q is invalid", digest)
		}
		arguments[index] = digest
	}
	rows, err := database.Query(`
		SELECT identity_digest, workload_id, execution_digest, input_digest,
			environment_digest, object_key, observed_at_unix_ms
		FROM ci_workload_pass_proofs
		WHERE identity_digest IN (`+placeholders+`)
	`, arguments...)
	if err != nil {
		return mapDurationLedgerSQLiteError("query PASS proofs", err)
	}
	defer rows.Close()
	for rows.Next() {
		var proof WorkloadPassProof
		var observedAtMS int64
		if err := rows.Scan(
			&proof.IdentityDigest,
			&proof.WorkloadID,
			&proof.ExecutionDigest,
			&proof.InputDigest,
			&proof.EnvironmentDigest,
			&proof.ObjectKey,
			&observedAtMS,
		); err != nil {
			return mapDurationLedgerSQLiteError("scan PASS proof", err)
		}
		proof.ObservedAt = time.UnixMilli(observedAtMS).UTC()
		destination[proof.IdentityDigest] = proof
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate PASS proofs", err)
	}
	return nil
}

// lookupCompatibleWorkloadPassCandidateChunk 读取每个 workload 的有限兼容 PASS 候选。
func lookupCompatibleWorkloadPassCandidateChunk(
	database *sql.DB,
	requests []WorkloadPassCandidateQuery,
	perWorkloadLimit int,
	destination map[string][]WorkloadPassCandidate,
) error {
	placeholders, arguments := compatibleWorkloadPassCandidateQueryArguments(requests, perWorkloadLimit)
	rows, err := database.Query(`
		WITH requested(workload_id, execution_digest, environment_digest) AS (
			VALUES `+placeholders+`
		),
		ranked AS (
			SELECT proofs.identity_digest, requested.workload_id,
				proofs.execution_digest, proofs.input_digest,
				proofs.environment_digest, proofs.object_key,
				proofs.observed_at_unix_ms, observations.source_tree_sha,
				ROW_NUMBER() OVER (
					PARTITION BY requested.workload_id
					ORDER BY proofs.observed_at_unix_ms DESC, proofs.identity_digest DESC
				) AS candidate_rank
			FROM requested
			JOIN ci_workload_identity_aliases AS aliases
				ON aliases.workload_id = requested.workload_id
			JOIN ci_workload_pass_proofs AS proofs
				ON proofs.identity_digest = aliases.identity_digest
				AND proofs.execution_digest = requested.execution_digest
				AND proofs.environment_digest = requested.environment_digest
			JOIN ci_workload_fingerprints AS fingerprints
				ON fingerprints.identity_digest = proofs.identity_digest
				AND fingerprints.execution_digest = proofs.execution_digest
				AND fingerprints.input_digest = proofs.input_digest
				AND fingerprints.environment_digest = proofs.environment_digest
			JOIN ci_workload_fingerprint_observations AS observations
				ON observations.identity_digest = proofs.identity_digest
				AND observations.source_tree_sha = (
					SELECT latest.source_tree_sha
					FROM ci_workload_fingerprint_observations AS latest
					WHERE latest.identity_digest = proofs.identity_digest
					ORDER BY latest.observed_at_unix_ms DESC, latest.source_tree_sha DESC
					LIMIT 1
				)
		)
		SELECT identity_digest, workload_id, execution_digest, input_digest,
			environment_digest, object_key, observed_at_unix_ms, source_tree_sha,
			candidate_rank
		FROM ranked
		WHERE candidate_rank <= ?
		ORDER BY workload_id, candidate_rank
	`, arguments...)
	if err != nil {
		return mapDurationLedgerSQLiteError("query compatible PASS candidates", err)
	}
	defer rows.Close()
	for rows.Next() {
		candidate, err := scanCompatibleWorkloadPassCandidate(rows, perWorkloadLimit)
		if err != nil {
			return err
		}
		destination[candidate.Proof.WorkloadID] = append(
			destination[candidate.Proof.WorkloadID],
			candidate,
		)
	}
	return mapDurationLedgerSQLiteError("iterate compatible PASS candidates", rows.Err())
}

func scanCompatibleWorkloadPassCandidate(rows *sql.Rows, perWorkloadLimit int) (WorkloadPassCandidate, error) {
	var candidate WorkloadPassCandidate
	var observedAtMS, rank int64
	if err := rows.Scan(
		&candidate.Proof.IdentityDigest,
		&candidate.Proof.WorkloadID,
		&candidate.Proof.ExecutionDigest,
		&candidate.Proof.InputDigest,
		&candidate.Proof.EnvironmentDigest,
		&candidate.Proof.ObjectKey,
		&observedAtMS,
		&candidate.SourceTreeSHA,
		&rank,
	); err != nil {
		return candidate, mapDurationLedgerSQLiteError("scan compatible PASS candidate", err)
	}
	candidate.Proof.ObservedAt = time.UnixMilli(observedAtMS).UTC()
	if err := validateWorkloadPassProof(candidate.Proof); err != nil {
		return candidate, fmt.Errorf("compatible PASS candidate: %w", err)
	}
	if !validCalibrationOID(candidate.SourceTreeSHA) {
		return candidate, errors.New("compatible PASS candidate source tree SHA is invalid")
	}
	if rank <= 0 || rank > int64(perWorkloadLimit) {
		return candidate, errors.New("compatible PASS candidate rank is invalid")
	}
	return candidate, nil
}

// compatibleWorkloadPassCandidateQueryArguments 生成批量兼容性查询的 VALUES 参数。
func compatibleWorkloadPassCandidateQueryArguments(requests []WorkloadPassCandidateQuery, perWorkloadLimit int) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("(?, ?, ?),", len(requests)), ",")
	arguments := make([]any, 0, len(requests)*3+1)
	for _, request := range requests {
		arguments = append(arguments, request.WorkloadID, request.ExecutionDigest, request.EnvironmentDigest)
	}
	return placeholders, append(arguments, perWorkloadLimit)
}

func validateWorkloadPassCandidateQuery(request WorkloadPassCandidateQuery) error {
	if strings.TrimSpace(request.WorkloadID) == "" {
		return errors.New("workload ID is required")
	}
	if !isSHA256Digest(request.ExecutionDigest) {
		return errors.New("execution digest is invalid")
	}
	if !isPrefixedSHA256Digest(request.EnvironmentDigest) {
		return errors.New("environment digest is invalid")
	}
	return nil
}

// validateWorkloadPassProof 校验可复用 PASS 证据的必填身份字段。
func validateWorkloadPassProof(proof WorkloadPassProof) error {
	if strings.TrimSpace(proof.WorkloadID) == "" {
		return errors.New("workload ID is required")
	}
	for field, digest := range map[string]string{
		"identity":    proof.IdentityDigest,
		"input":       proof.InputDigest,
		"environment": proof.EnvironmentDigest,
	} {
		if !isPrefixedSHA256Digest(digest) {
			return fmt.Errorf("%s digest is invalid", field)
		}
	}
	if !isSHA256Digest(proof.ExecutionDigest) {
		return errors.New("execution digest is invalid")
	}
	if strings.TrimSpace(proof.ObjectKey) == "" {
		return errors.New("object key is required")
	}
	if proof.ObservedAt.IsZero() {
		return errors.New("observed time is required")
	}
	return nil
}

// validateWorkloadFingerprintRecord 校验 workload 指纹记录的确定性身份字段。
func validateWorkloadFingerprintRecord(record WorkloadFingerprintRecord) error {
	if strings.TrimSpace(record.WorkloadID) == "" {
		return errors.New("workload ID is required")
	}
	for field, digest := range map[string]string{
		"identity":    record.IdentityDigest,
		"input":       record.InputDigest,
		"environment": record.EnvironmentDigest,
	} {
		if !isPrefixedSHA256Digest(digest) {
			return fmt.Errorf("%s digest is invalid", field)
		}
	}
	if !isSHA256Digest(record.ExecutionDigest) {
		return errors.New("execution digest is invalid")
	}
	if !validCalibrationOID(record.SourceTreeSHA) {
		return errors.New("source tree SHA is invalid")
	}
	if record.ObservedAt.IsZero() {
		return errors.New("observed time is required")
	}
	return nil
}
