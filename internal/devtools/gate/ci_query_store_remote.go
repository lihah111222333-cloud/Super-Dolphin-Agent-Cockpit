package gate

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// loadRemoteCIRunRow 从一致性读快照解码远端 CI run 的主记录。
func loadRemoteCIRunRow(database sqliteRowQueryer, jobID string) (RemoteCIRunRecord, error) {
	var (
		record                                          RemoteCIRunRecord
		entrypoint, profile, status, acceptedGeneration string
		authoritative, cleanupComplete                  int
		startedAtMS, completedAtMS                      int64
	)
	err := database.QueryRow(`
		SELECT runs.job_id, identities.agent_token_digest,
			runs.entrypoint, runs.profile, runs.plan_digest, runs.catalog_digest, runs.accepted_generation, runs.image_cache_snapshot_id,
			runs.source_tree_sha, runs.candidate_gate_source_sha256, runs.candidate_gate_toolchain_sha256,
			runs.runner_image, runs.status, runs.authoritative,
			runs.started_at_unix_ms, runs.completed_at_unix_ms,
			runs.cleanup_complete, runs.error_text
		FROM ci_runs AS runs
		INNER JOIN ci_run_agent_identities AS identities ON identities.job_id = runs.job_id
		WHERE runs.job_id = ?
	`, jobID).Scan(
		&record.JobID,
		&record.AgentTokenDigest,
		&entrypoint,
		&profile,
		&record.PlanDigest,
		&record.CatalogDigest,
		&acceptedGeneration,
		&record.ImageCacheSnapshotID,
		&record.SourceTreeSHA,
		&record.CandidateGateSourceSHA256,
		&record.CandidateGateToolchainSHA256,
		&record.RunnerImage,
		&status,
		&authoritative,
		&startedAtMS,
		&completedAtMS,
		&cleanupComplete,
		&record.ErrorText,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RemoteCIRunRecord{}, fmt.Errorf("%w: %s", ErrRemoteCIRunNotFound, jobID)
	}
	if err != nil {
		return RemoteCIRunRecord{}, mapDurationLedgerSQLiteError("load remote CI run", err)
	}
	record.Entrypoint = CIEntrypointID(entrypoint)
	record.Profile = Profile(profile)
	if record.AcceptedGeneration, err = strconv.ParseUint(acceptedGeneration, 10, 64); err != nil || record.AcceptedGeneration == 0 {
		return RemoteCIRunRecord{}, errors.New("stored remote CI accepted generation is invalid")
	}
	record.Status = ResultStatus(status)
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("stored remote CI agent token digest: %w", err)
	}
	record.Authoritative = authoritative == 1
	record.CleanupComplete = cleanupComplete == 1
	record.StartedAt = time.UnixMilli(startedAtMS).UTC()
	record.CompletedAt = time.UnixMilli(completedAtMS).UTC()
	return record, nil
}

// loadRemoteCIRunDetails 在同一个只读事务中补全 run 的关联投影。
func loadRemoteCIRunDetails(transaction *sql.Tx, jobID string, record *RemoteCIRunRecord) error {
	var err error
	record.Shards, err = loadRemoteCIShardRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.Executions, err = loadRemoteCIExecutionRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.WorkloadExecutions, err = loadRemoteCIWorkloadExecutionRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.WorkloadResults, err = loadRemoteCIWorkloadResults(transaction, jobID)
	if err != nil {
		return err
	}
	record.Warnings, err = loadRemoteCIRunWarningRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.TimingWarnings, err = loadRemoteCITimingWarnings(transaction, cicontract.RunTimingWarningsTable, jobID)
	if err != nil {
		return err
	}
	record.TimingObservations, err = loadTimingObservations(transaction, jobID)
	if err != nil {
		return err
	}
	return nil
}

// loadTimingObservations 从同一 SQLite 快照还原真实阶段观测，保留结构化时长和缓存证据而不从日志推断。
func loadTimingObservations(database sqliteRowQueryer, jobID string) ([]TimingObservation, error) {
	rows, err := database.Query(`SELECT scope, shard_identity, workload_id, phase, started_at_unix_ms, completed_at_unix_ms, duration_ms, measurement, reason, aggregation, cache_evidence_json FROM ci_timing_observations WHERE job_id = ? ORDER BY scope, shard_identity, workload_id, phase`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query timing observations", err)
	}
	defer rows.Close()
	var observations []TimingObservation
	for rows.Next() {
		var scope, workloadID, phase, measurement, aggregation string
		var startedMS, completedMS int64
		observation := TimingObservation{JobID: jobID}
		var cacheEvidenceJSON string
		if err := rows.Scan(&scope, &observation.ShardIdentity, &workloadID, &phase, &startedMS, &completedMS, &observation.DurationMS, &measurement, &observation.Reason, &aggregation, &cacheEvidenceJSON); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan timing observation", err)
		}
		observation.Scope, observation.WorkloadID, observation.Phase = cicontract.TimingScope(scope), GateID(workloadID), cicontract.TimingPhase(phase)
		observation.Measurement, observation.Aggregation = cicontract.ObservationState(measurement), cicontract.TimingAggregation(aggregation)
		if err := DecodeStrictJSON([]byte(cacheEvidenceJSON), &observation.CacheEvidence); err != nil {
			return nil, fmt.Errorf("decode stored timing cache evidence: %w", err)
		}
		if startedMS != 0 {
			observation.StartedAt = time.UnixMilli(startedMS).UTC()
		}
		if completedMS != 0 {
			observation.CompletedAt = time.UnixMilli(completedMS).UTC()
		}
		if err := observation.Validate(); err != nil {
			return nil, errors.New("stored timing observation is invalid")
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate timing observations", err)
	}
	return observations, nil
}

// loadRemoteCIWorkloadExecutionRows 读取每个计划 workload 的实际执行结果，供回执验证其未被历史 PASS 跳过。
func loadRemoteCIWorkloadExecutionRows(database sqliteRowQueryer, jobID string) ([]PlanGateExecution, error) {
	rows, err := database.Query(`
		SELECT shard_identity, workload_id, status, exit_code, started_at_unix_ms, completed_at_unix_ms,
			argv_digest, log_digest, test_timings_json, execution_profile_json
		FROM ci_workload_executions
		WHERE job_id = ?
		ORDER BY workload_id
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI workload executions", err)
	}
	defer rows.Close()
	var executions []PlanGateExecution
	for rows.Next() {
		var execution PlanGateExecution
		var startedMS, completedMS int64
		var profileJSON, testTimingsJSON string
		if err := rows.Scan(&execution.ShardIdentity, &execution.GateID, &execution.Status, &execution.ExitCode, &startedMS, &completedMS, &execution.ArgvDigest, &execution.LogDigest, &testTimingsJSON, &profileJSON); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI workload execution", err)
		}
		execution.StartedAt = time.UnixMilli(startedMS).UTC()
		execution.CompletedAt = time.UnixMilli(completedMS).UTC()
		if execution.TestTimings, err = decodeStoredRemoteCIExecutionTestTimings(testTimingsJSON); err != nil {
			return nil, err
		}
		execution.ExecutionProfile, err = decodeStoredRemoteCIExecutionProfile(profileJSON)
		if err != nil {
			return nil, err
		}
		if err := execution.ExecutionProfile.Validate(); err != nil {
			return nil, errors.New("stored remote CI workload execution profile is invalid")
		}
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI workload executions", err)
	}
	return executions, nil
}

// loadRemoteCIShardRows 从读取快照恢复 run 的 shard 投影。
func loadRemoteCIShardRows(
	database sqliteRowQueryer,
	jobID string,
) ([]RemoteCIShardRecord, error) {
	rows, err := database.Query(`
		SELECT shards.shard_identity, shards.container_group_id, shards.container_status, shards.materialization_timing_json, shards.resources_json,
			workloads.workload_id
		FROM ci_shards AS shards
		LEFT JOIN ci_shard_workloads AS workloads
			ON workloads.job_id = shards.job_id
			AND workloads.shard_identity = shards.shard_identity
		WHERE shards.job_id = ?
		ORDER BY shards.shard_identity, workloads.workload_id
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI shards", err)
	}
	defer rows.Close()
	var (
		shards     []RemoteCIShardRecord
		shardIndex = make(map[string]int)
	)
	for rows.Next() {
		var (
			shard         RemoteCIShardRecord
			workloadID    sql.NullString
			timingJSON    string
			resourcesJSON string
		)
		if err := rows.Scan(
			&shard.ShardIdentity,
			&shard.ContainerGroup,
			&shard.ContainerStatus,
			&timingJSON,
			&resourcesJSON,
			&workloadID,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI shard", err)
		}
		if err := decodeStoredRemoteCIShardEvidence(&shard, timingJSON, resourcesJSON); err != nil {
			return nil, err
		}
		index, exists := shardIndex[shard.ShardIdentity]
		if !exists {
			index = len(shards)
			shardIndex[shard.ShardIdentity] = index
			shards = append(shards, shard)
		}
		if workloadID.Valid {
			if strings.TrimSpace(workloadID.String) == "" {
				return nil, errors.New("stored remote CI shard workload ID is invalid")
			}
			shards[index].Workloads = append(shards[index].Workloads, GateID(workloadID.String))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI shards", err)
	}
	return shards, nil
}

// decodeStoredRemoteCIShardEvidence 严格解码并校验 SQLite 分片的资源和物化时序证据。
func decodeStoredRemoteCIShardEvidence(shard *RemoteCIShardRecord, timingJSON, resourcesJSON string) error {
	if resourcesJSON == "" {
		return errors.New("stored remote CI shard resources are required")
	}
	if err := DecodeStrictJSON([]byte(resourcesJSON), &shard.Resources); err != nil {
		return fmt.Errorf("decode stored remote CI shard resources: %w", err)
	}
	if err := shard.Resources.Validate(); err != nil {
		return fmt.Errorf("validate stored remote CI shard resources: %w", err)
	}
	if timingJSON == "" {
		return errors.New("stored remote CI shard materialization timing is required")
	}
	if err := DecodeStrictJSON([]byte(timingJSON), &shard.MaterializationTiming); err != nil {
		return fmt.Errorf("decode stored remote CI shard materialization timing: %w", err)
	}
	if err := validateRemoteCIShardMaterializationTiming(*shard); err != nil {
		return fmt.Errorf("validate stored remote CI shard materialization timing: %w", err)
	}
	return nil
}

// loadRemoteCIExecutionRows 从读取快照恢复 gate 终态投影。
func loadRemoteCIExecutionRows(
	database sqliteRowQueryer,
	jobID string,
) ([]PlanGateExecution, error) {
	rows, err := database.Query(`
		SELECT workload_id, status, exit_code, started_at_unix_ms,
			completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json
		FROM ci_gate_executions
		WHERE job_id = ?
		ORDER BY workload_id
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI gate executions", err)
	}
	defer rows.Close()
	var executions []PlanGateExecution
	for rows.Next() {
		var (
			execution                    PlanGateExecution
			workloadID, status           string
			startedAtMS, completedAtMS   int64
			testTimingsJSON, profileJSON string
		)
		if err := rows.Scan(
			&workloadID,
			&status,
			&execution.ExitCode,
			&startedAtMS,
			&completedAtMS,
			&execution.ArgvDigest,
			&execution.LogDigest,
			&testTimingsJSON,
			&profileJSON,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI gate execution", err)
		}
		execution.GateID = GateID(workloadID)
		execution.Status = ResultStatus(status)
		execution.StartedAt = time.UnixMilli(startedAtMS).UTC()
		execution.CompletedAt = time.UnixMilli(completedAtMS).UTC()
		if execution.TestTimings, err = decodeStoredRemoteCIExecutionTestTimings(testTimingsJSON); err != nil {
			return nil, err
		}
		execution.ExecutionProfile, err = decodeStoredRemoteCIExecutionProfile(profileJSON)
		if err != nil {
			return nil, err
		}
		if err := validateRemoteCIAggregateExecution(execution); err != nil {
			return nil, err
		}
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI gate executions", err)
	}
	return executions, nil
}

func decodeStoredRemoteCIExecutionTestTimings(encoded string) ([]GoTestTiming, error) {
	var timings []GoTestTiming
	if err := decodeStrictJSON(bytes.NewReader([]byte(encoded)), &timings); err != nil ||
		!validPlanGateTestTimings(timings, ExecutorPlanReportSchemaVersion) {
		return nil, errors.New("stored remote CI execution test timings are invalid")
	}
	return timings, nil
}

// decodeStoredRemoteCIExecutionProfile 只接受非空且无未知字段的当前结构化执行画像。
func decodeStoredRemoteCIExecutionProfile(encoded string) (ExecutionProfile, error) {
	if strings.TrimSpace(encoded) == "" {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is required")
	}
	var profile ExecutionProfile
	if err := DecodeStrictJSON([]byte(encoded), &profile); err != nil {
		return ExecutionProfile{}, errors.New("stored remote CI execution profile is invalid")
	}
	return profile, nil
}

// validateRemoteCIAggregateExecution 校验 parent gate 的区间并集/关键路径 profile 未与执行时间戳漂移。
func validateRemoteCIAggregateExecution(execution PlanGateExecution) error {
	if err := execution.ExecutionProfile.ValidateAggregate(); err != nil {
		return errors.New("stored remote CI aggregate execution profile is invalid")
	}
	if execution.StartedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) ||
		execution.ExecutionProfile.TotalMS != execution.CompletedAt.Sub(execution.StartedAt).Milliseconds() {
		return errors.New("stored remote CI aggregate execution interval is invalid")
	}
	return nil
}

func replaceSQLiteRemoteRunWarnings(
	transaction *sql.Tx,
	jobID string,
	warnings []string,
) error {
	if _, err := transaction.Exec(`DELETE FROM ci_run_warnings WHERE job_id = ?`, jobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI run warnings", err)
	}
	for ordinal, warning := range warnings {
		if _, err := transaction.Exec(`
			INSERT INTO ci_run_warnings (job_id, ordinal, warning_text)
			VALUES (?, ?, ?)
		`, jobID, ordinal, warning); err != nil {
			return mapDurationLedgerSQLiteError("store remote CI run warning", err)
		}
	}
	return nil
}

// loadRemoteCIRunWorkloadRows 分别恢复复用与未命中 workload。
func loadRemoteCIRunWarningRows(database sqliteRowQueryer, jobID string) ([]string, error) {
	rows, err := database.Query(`
		SELECT warning_text
		FROM ci_run_warnings
		WHERE job_id = ?
		ORDER BY ordinal
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI run warnings", err)
	}
	defer rows.Close()
	var warnings []string
	for rows.Next() {
		var warning string
		if err := rows.Scan(&warning); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI run warning", err)
		}
		warnings = append(warnings, warning)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI run warnings", err)
	}
	return warnings, nil
}

// validateSQLiteRemoteCIRunCatalogCoverage 确认 run 的 workload 与 gate 记录都属于绑定 catalog。
func validateSQLiteRemoteCIRunCatalogCoverage(transaction *sql.Tx, record RemoteCIRunRecord) error {
	recordedWorkloads := remoteCIRunRecordedWorkloads(record)
	if remoteCIRunHasNoCatalogRecords(record, recordedWorkloads) {
		return nil
	}
	catalog, err := loadSQLiteWorkloadCatalog(transaction, record.CatalogDigest)
	if err != nil {
		return fmt.Errorf("load remote CI workload catalog: %w", err)
	}
	if record.Status == ResultStatusPassed && record.Authoritative && !catalog.Catalog.Authoritative {
		return errors.New("passed remote CI run requires an authoritative workload catalog")
	}
	index, err := newRemoteCIRunCatalogIndex(catalog.Catalog)
	if err != nil {
		return err
	}
	if err := index.validateRecorded(record, recordedWorkloads); err != nil {
		return err
	}
	if record.Status != ResultStatusPassed {
		return nil
	}
	return index.validatePassed(record)
}

type remoteCIRunCatalogIndex struct {
	catalog    WorkloadCatalog
	workloads  map[GateID]struct{}
	shardable  map[GateID]struct{}
	executions map[GateID]struct{}
}

// remoteCIRunRecordedWorkloads 汇总 run 的直接和 shard 记录 workload。
func remoteCIRunRecordedWorkloads(record RemoteCIRunRecord) []GateID {
	var workloads []GateID
	for _, shard := range record.Shards {
		workloads = append(workloads, shard.Workloads...)
	}
	return workloads
}

// remoteCIRunHasNoCatalogRecords 识别非 PASS 且没有任何 catalog 投影的空 run。
func remoteCIRunHasNoCatalogRecords(record RemoteCIRunRecord, workloads []GateID) bool {
	return record.Status != ResultStatusPassed && len(workloads) == 0 && len(record.Executions) == 0
}

// newRemoteCIRunCatalogIndex 建立 workload 与其父 gate 的可验证索引。
func newRemoteCIRunCatalogIndex(catalog WorkloadCatalog) (remoteCIRunCatalogIndex, error) {
	index := remoteCIRunCatalogIndex{catalog: catalog, workloads: make(map[GateID]struct{}), shardable: make(map[GateID]struct{}), executions: make(map[GateID]struct{})}
	for _, workload := range catalog.Workloads {
		id := GateID(workload.ID)
		index.workloads[id] = struct{}{}
		if !workload.Shardable {
			continue
		}
		parent, err := WorkloadParentGateID(workload.ID)
		if err != nil {
			return remoteCIRunCatalogIndex{}, fmt.Errorf("resolve remote CI catalog workload parent: %w", err)
		}
		index.shardable[id], index.executions[parent] = struct{}{}, struct{}{}
	}
	return index, nil
}

// validateRecorded 确认 run 的 workload 与执行 gate 都存在于 catalog。
func (index remoteCIRunCatalogIndex) validateRecorded(record RemoteCIRunRecord, workloads []GateID) error {
	for _, workloadID := range workloads {
		if _, exists := index.workloads[workloadID]; !exists {
			return fmt.Errorf("%s remote CI recorded workload %q is absent from its catalog", record.Status, workloadID)
		}
	}
	for _, execution := range record.Executions {
		if _, exists := index.executions[execution.GateID]; !exists {
			return fmt.Errorf("%s remote CI recorded gate execution %q is absent from its catalog", record.Status, execution.GateID)
		}
	}
	return nil
}

// validatePassed 要求结果覆盖 catalog；fresh 分片和执行仅记录 executed workload。
func (index remoteCIRunCatalogIndex) validatePassed(record RemoteCIRunRecord) error {
	results, executedWorkloads, err := index.passedWorkloadResults(record.WorkloadResults)
	if err != nil {
		return err
	}
	shardWorkloads, shardByWorkload, err := passedFreshShardWorkloads(record.Shards, results)
	if err != nil {
		return err
	}
	if err := validateRemoteCIRunFreshWorkloadSet("shard", shardWorkloads, executedWorkloads); err != nil {
		return err
	}
	executionWorkloads, err := passedFreshExecutionWorkloads(record.WorkloadExecutions, results, shardByWorkload)
	if err != nil {
		return err
	}
	return validateRemoteCIRunFreshWorkloadSet("execution", executionWorkloads, executedWorkloads)
}

// passedWorkloadResults 验证结果精确覆盖可分片目录，并提取本次必须 fresh 执行的 workload。
func (index remoteCIRunCatalogIndex) passedWorkloadResults(workloadResults []RemoteCIWorkloadResult) (map[GateID]string, map[GateID]struct{}, error) {
	results := make(map[GateID]string, len(workloadResults))
	executed := make(map[GateID]struct{})
	for _, result := range workloadResults {
		workloadID := result.Identity.WorkloadID
		if _, exists := index.shardable[workloadID]; !exists {
			return nil, nil, fmt.Errorf("passed remote CI workload result %q is absent from its shardable catalog", workloadID)
		}
		if _, duplicate := results[workloadID]; duplicate {
			return nil, nil, fmt.Errorf("passed remote CI workload result %q is duplicated", workloadID)
		}
		results[workloadID] = result.Disposition
		if result.Disposition == WorkloadDispositionExecuted {
			executed[workloadID] = struct{}{}
		}
	}
	for workloadID := range index.shardable {
		if _, exists := results[workloadID]; !exists {
			return nil, nil, fmt.Errorf("passed remote CI run does not cover shardable workload result %q", workloadID)
		}
	}
	return results, executed, nil
}

// passedFreshShardWorkloads 仅接受标记 executed 的 workload，并保留其 fresh shard 归属。
func passedFreshShardWorkloads(shards []RemoteCIShardRecord, results map[GateID]string) (map[GateID]struct{}, map[GateID]string, error) {
	workloads := make(map[GateID]struct{})
	shardsByWorkload := make(map[GateID]string)
	for _, shard := range shards {
		for _, workloadID := range shard.Workloads {
			if results[workloadID] != WorkloadDispositionExecuted {
				return nil, nil, fmt.Errorf("passed remote CI fresh shard workload %q is not executed", workloadID)
			}
			workloads[workloadID] = struct{}{}
			shardsByWorkload[workloadID] = shard.ShardIdentity
		}
	}
	return workloads, shardsByWorkload, nil
}

func passedFreshExecutionWorkloads(executions []PlanGateExecution, results map[GateID]string, shardsByWorkload map[GateID]string) (map[GateID]struct{}, error) {
	workloads := make(map[GateID]struct{}, len(executions))
	for _, execution := range executions {
		if results[execution.GateID] != WorkloadDispositionExecuted {
			return nil, fmt.Errorf("passed remote CI fresh execution workload %q is not executed", execution.GateID)
		}
		if execution.ShardIdentity != shardsByWorkload[execution.GateID] {
			return nil, fmt.Errorf("passed remote CI fresh execution workload %q does not match its shard", execution.GateID)
		}
		workloads[execution.GateID] = struct{}{}
	}
	return workloads, nil
}

func validateRemoteCIRunFreshWorkloadSet(kind string, recorded, executed map[GateID]struct{}) error {
	for workloadID := range executed {
		if _, exists := recorded[workloadID]; !exists {
			return fmt.Errorf("passed remote CI executed workload %q is missing from fresh %s records", workloadID, kind)
		}
	}
	for workloadID := range recorded {
		if _, exists := executed[workloadID]; !exists {
			return fmt.Errorf("passed remote CI fresh %s workload %q is not executed", kind, workloadID)
		}
	}
	return nil
}

// advanceCIQueryRevision 推进 SQLite 查询投影的单调版本。
func advanceCIQueryRevision(transaction *sql.Tx, now time.Time) error {
	var revisionText string
	err := transaction.QueryRow(`
		SELECT revision FROM ci_query_meta WHERE singleton = 1
	`).Scan(&revisionText)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = transaction.Exec(`
			INSERT INTO ci_query_meta (singleton, revision, updated_at_unix_ms)
			VALUES (1, '1', ?)
		`, now.UnixMilli())
		return mapDurationLedgerSQLiteError("initialize CI query revision", err)
	}
	if err != nil {
		return mapDurationLedgerSQLiteError("load CI query revision", err)
	}
	revision, err := strconv.ParseUint(revisionText, 10, 64)
	if err != nil || revision == ^uint64(0) {
		return errors.New("CI query revision is invalid")
	}
	result, err := transaction.Exec(`
		UPDATE ci_query_meta
		SET revision = ?, updated_at_unix_ms = ?
		WHERE singleton = 1 AND revision = ?
	`, strconv.FormatUint(revision+1, 10), now.UnixMilli(), revisionText)
	if err != nil {
		return mapDurationLedgerSQLiteError("advance CI query revision", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read CI query revision update count: %w", err)
	}
	if affected != 1 {
		return errors.New("CI query revision changed concurrently")
	}
	return nil
}

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}
