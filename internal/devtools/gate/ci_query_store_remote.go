package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// loadRemoteCIRunRow 从一致性读快照解码远端 CI run 的主记录。
func loadRemoteCIRunRow(database sqliteRowQueryer, jobID string) (RemoteCIRunRecord, error) {
	var (
		record                         RemoteCIRunRecord
		entrypoint, profile, status    string
		authoritative, cleanupComplete int
		startedAtMS, completedAtMS     int64
	)
	err := database.QueryRow(`
		SELECT runs.job_id, COALESCE(requesters.requester_fingerprint, ''),
			runs.entrypoint, runs.profile, runs.plan_digest, runs.catalog_digest,
			runs.source_tree_sha, runs.runner_image, runs.status, runs.authoritative,
			runs.started_at_unix_ms, runs.completed_at_unix_ms,
			runs.cleanup_complete, runs.error_text
		FROM ci_runs AS runs
		LEFT JOIN ci_run_requesters AS requesters ON requesters.job_id = runs.job_id
		WHERE runs.job_id = ?
	`, jobID).Scan(
		&record.JobID,
		&record.RequesterFingerprint,
		&entrypoint,
		&profile,
		&record.PlanDigest,
		&record.CatalogDigest,
		&record.SourceTreeSHA,
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
	record.Status = ResultStatus(status)
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
	record.ReusedWorkloads, record.CacheMisses, err = loadRemoteCIRunWorkloadRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.Warnings, err = loadRemoteCIRunWarningRows(transaction, jobID)
	if err != nil {
		return err
	}
	record.PhaseTimings, err = loadRemoteCIRunPhaseTimingRows(transaction, jobID)
	return err
}

// loadRemoteCIShardRows 从读取快照恢复 run 的 shard 投影。
func loadRemoteCIShardRows(
	database sqliteRowQueryer,
	jobID string,
) ([]RemoteCIShardRecord, error) {
	rows, err := database.Query(`
		SELECT shards.shard_identity, shards.container_group_id, shards.container_status,
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
			shard      RemoteCIShardRecord
			workloadID sql.NullString
		)
		if err := rows.Scan(
			&shard.ShardIdentity,
			&shard.ContainerGroup,
			&shard.ContainerStatus,
			&workloadID,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI shard", err)
		}
		index, exists := shardIndex[shard.ShardIdentity]
		if !exists {
			index = len(shards)
			shardIndex[shard.ShardIdentity] = index
			shards = append(shards, shard)
		}
		if workloadID.Valid {
			shards[index].Workloads = append(shards[index].Workloads, GateID(workloadID.String))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI shards", err)
	}
	return shards, nil
}

// loadRemoteCIExecutionRows 从读取快照恢复 gate 终态投影。
func loadRemoteCIExecutionRows(
	database sqliteRowQueryer,
	jobID string,
) ([]PlanGateExecution, error) {
	rows, err := database.Query(`
		SELECT workload_id, status, exit_code, started_at_unix_ms,
			completed_at_unix_ms, argv_digest, log_digest
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
			execution                  PlanGateExecution
			workloadID, status         string
			startedAtMS, completedAtMS int64
		)
		if err := rows.Scan(
			&workloadID,
			&status,
			&execution.ExitCode,
			&startedAtMS,
			&completedAtMS,
			&execution.ArgvDigest,
			&execution.LogDigest,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI gate execution", err)
		}
		execution.GateID = GateID(workloadID)
		execution.Status = ResultStatus(status)
		execution.StartedAt = time.UnixMilli(startedAtMS).UTC()
		execution.CompletedAt = time.UnixMilli(completedAtMS).UTC()
		executions = append(executions, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI gate executions", err)
	}
	return executions, nil
}

// replaceSQLiteRemoteRunWorkloads 用当前 run 的缓存命中和未命中集替换投影。
func replaceSQLiteRemoteRunWorkloads(
	transaction *sql.Tx,
	record RemoteCIRunRecord,
) error {
	if _, err := transaction.Exec(`DELETE FROM ci_run_workloads WHERE job_id = ?`, record.JobID); err != nil {
		return mapDurationLedgerSQLiteError("clear remote CI run workloads", err)
	}
	for disposition, workloads := range map[string][]GateID{
		"reused":     record.ReusedWorkloads,
		"cache_miss": record.CacheMisses,
	} {
		for _, workloadID := range workloads {
			if _, err := transaction.Exec(`
				INSERT INTO ci_run_workloads (job_id, workload_id, disposition)
				VALUES (?, ?, ?)
			`, record.JobID, string(workloadID), disposition); err != nil {
				return mapDurationLedgerSQLiteError("store remote CI run workload", err)
			}
		}
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
func loadRemoteCIRunWorkloadRows(
	database sqliteRowQueryer,
	jobID string,
) ([]GateID, []GateID, error) {
	rows, err := database.Query(`
		SELECT workload_id, disposition
		FROM ci_run_workloads
		WHERE job_id = ?
		ORDER BY disposition, workload_id
	`, jobID)
	if err != nil {
		return nil, nil, mapDurationLedgerSQLiteError("query remote CI run workloads", err)
	}
	defer rows.Close()
	var reused, cacheMisses []GateID
	for rows.Next() {
		var workloadID, disposition string
		if err := rows.Scan(&workloadID, &disposition); err != nil {
			return nil, nil, mapDurationLedgerSQLiteError("scan remote CI run workload", err)
		}
		switch disposition {
		case "reused":
			reused = append(reused, GateID(workloadID))
		case "cache_miss":
			cacheMisses = append(cacheMisses, GateID(workloadID))
		default:
			return nil, nil, fmt.Errorf(
				"remote CI run workload disposition %q is invalid",
				disposition,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, mapDurationLedgerSQLiteError("iterate remote CI run workloads", err)
	}
	return reused, cacheMisses, nil
}

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

func loadRemoteCIRunPhaseTimingRows(
	database sqliteRowQueryer,
	jobID string,
) ([]RemoteCIPhaseTiming, error) {
	rows, err := database.Query(`
		SELECT phase, started_at_unix_ms, duration_ms, outcome,
			workload_count, shard_count, cache_hit_count, cache_miss_count
		FROM ci_run_phase_timings
		WHERE job_id = ?
		ORDER BY ordinal
	`, jobID)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("query remote CI phase timings", err)
	}
	defer rows.Close()
	var timings []RemoteCIPhaseTiming
	for rows.Next() {
		var (
			timing      RemoteCIPhaseTiming
			startedAtMS int64
			outcome     string
		)
		if err := rows.Scan(
			&timing.Phase,
			&startedAtMS,
			&timing.DurationMillis,
			&outcome,
			&timing.WorkloadCount,
			&timing.ShardCount,
			&timing.CacheHitCount,
			&timing.CacheMissCount,
		); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan remote CI phase timing", err)
		}
		timing.StartedAt = time.UnixMilli(startedAtMS).UTC()
		timing.Outcome = RemoteCIPhaseOutcome(outcome)
		timings = append(timings, timing)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate remote CI phase timings", err)
	}
	return timings, nil
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
	workloads := append([]GateID(nil), record.ReusedWorkloads...)
	workloads = append(workloads, record.CacheMisses...)
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

// validatePassed 要求成功 run 覆盖每一个可分片 workload。
func (index remoteCIRunCatalogIndex) validatePassed(record RemoteCIRunRecord) error {
	covered := make(map[GateID]struct{}, len(record.ReusedWorkloads)+len(record.CacheMisses))
	for _, workloadID := range append(record.ReusedWorkloads, record.CacheMisses...) {
		if _, exists := index.shardable[workloadID]; !exists {
			return fmt.Errorf("passed remote CI workload %q is absent from its catalog", workloadID)
		}
		covered[workloadID] = struct{}{}
	}
	for workloadID := range index.shardable {
		if _, exists := covered[workloadID]; !exists {
			return fmt.Errorf("passed remote CI run does not cover shardable workload %q", workloadID)
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
