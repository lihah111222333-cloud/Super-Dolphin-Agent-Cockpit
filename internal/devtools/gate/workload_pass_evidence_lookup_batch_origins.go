package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// loadDirectWorkloadPassOriginBatches loads every direct origin projection by
// job-id chunk. It deliberately owns the batch SQL instead of calling the
// single-origin loader used by source replay.
func loadDirectWorkloadPassOriginBatches(tx *sql.Tx, evidence []WorkloadPassEvidence, current uint64, out map[string]workloadPassEvidenceOriginContext, stats *workloadPassEvidenceLookupStats) error {
	ids := uniqueWorkloadPassOriginIDs(evidence)
	for start := 0; start < len(ids); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(ids))
		if err := loadDirectWorkloadPassOriginBatchChunk(tx, ids[start:end], evidence, current, out, stats); err != nil {
			return err
		}
	}
	return nil
}

func uniqueWorkloadPassOriginIDs(evidence []WorkloadPassEvidence) []string {
	seen := make(map[string]struct{}, len(evidence))
	ids := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if _, ok := seen[item.OriginJobID]; !ok {
			seen[item.OriginJobID] = struct{}{}
			ids = append(ids, item.OriginJobID)
		}
	}
	return ids
}

func loadDirectWorkloadPassOriginBatchChunk(tx *sql.Tx, ids []string, evidence []WorkloadPassEvidence, current uint64, out map[string]workloadPassEvidenceOriginContext, stats *workloadPassEvidenceLookupStats) error {
	records, err := queryWorkloadPassOriginRunsBatch(tx, ids, stats)
	if err != nil {
		return err
	}
	if err := queryWorkloadPassOriginExecutionsBatch(tx, ids, records, stats); err != nil {
		return err
	}
	if err := queryWorkloadPassOriginResultsBatch(tx, ids, records, stats); err != nil {
		return err
	}
	receipts, err := queryWorkloadPassOriginReceiptsBatch(tx, ids, stats)
	if err != nil {
		return err
	}
	for id, record := range records {
		if !workloadPassOriginIsRequestedDirect(record, evidence) {
			continue
		}
		digest, err := workloadPassOriginReceiptDigest(record, receipts[id])
		if err != nil {
			return err
		}
		out[id] = workloadPassEvidenceOriginContext{record: record, receiptDigest: digest, currentGeneration: current}
	}
	return nil
}

func queryWorkloadPassOriginRunsBatch(tx *sql.Tx, ids []string, stats *workloadPassEvidenceLookupStats) (map[string]RemoteCIRunRecord, error) {
	rows, err := tx.Query(`SELECT runs.job_id, identities.agent_token_digest, runs.force, runs.entrypoint, runs.profile, runs.plan_digest, runs.catalog_digest, runs.accepted_generation, runs.image_cache_snapshot_id, runs.source_tree_sha, runs.candidate_gate_source_sha256, runs.candidate_gate_toolchain_sha256, runs.runner_image, runs.status, runs.authoritative, runs.started_at_unix_ms, runs.completed_at_unix_ms, runs.cleanup_complete, runs.error_text FROM ci_runs AS runs JOIN ci_run_agent_identities AS identities ON identities.job_id = runs.job_id WHERE runs.job_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, stringsToAny(ids)...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("batch load workload PASS origin runs", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.originRunLoads++
	}
	result := make(map[string]RemoteCIRunRecord, len(ids))
	for rows.Next() {
		record, err := scanWorkloadPassOriginRun(rows)
		if err != nil {
			return nil, err
		}
		result[record.JobID] = record
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate batch workload PASS origin runs", err)
	}
	return result, nil
}

func scanWorkloadPassOriginRun(rows interface{ Scan(...any) error }) (RemoteCIRunRecord, error) {
	var record RemoteCIRunRecord
	var entrypoint, profile, status, generation string
	var force, authoritative, cleanup int
	var started, completed int64
	if err := rows.Scan(&record.JobID, &record.AgentTokenDigest, &force, &entrypoint, &profile, &record.PlanDigest, &record.CatalogDigest, &generation, &record.ImageCacheSnapshotID, &record.SourceTreeSHA, &record.CandidateGateSourceSHA256, &record.CandidateGateToolchainSHA256, &record.RunnerImage, &status, &authoritative, &started, &completed, &cleanup, &record.ErrorText); err != nil {
		return RemoteCIRunRecord{}, mapDurationLedgerSQLiteError("scan batch workload PASS origin run", err)
	}
	value, err := parseWorkloadPassOriginRunEncoding(generation, force, authoritative, cleanup)
	if err != nil {
		return RemoteCIRunRecord{}, errors.New("stored batch workload PASS origin run encoding is invalid")
	}
	record.AcceptedGeneration, record.Entrypoint, record.Profile, record.Status = value, CIEntrypointID(entrypoint), Profile(profile), ResultStatus(status)
	record.Force, record.Authoritative, record.CleanupComplete = force == 1, authoritative == 1, cleanup == 1
	record.StartedAt, record.CompletedAt = time.UnixMilli(started).UTC(), time.UnixMilli(completed).UTC()
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return RemoteCIRunRecord{}, fmt.Errorf("stored batch workload PASS origin token: %w", err)
	}
	return record, nil
}

func parseWorkloadPassOriginRunEncoding(generation string, force, authoritative, cleanup int) (uint64, error) {
	value, err := strconv.ParseUint(generation, 10, 64)
	if err != nil || value == 0 || force < 0 || force > 1 {
		return 0, errors.New("invalid batch workload PASS run encoding")
	}
	if authoritative < 0 || authoritative > 1 || cleanup < 0 || cleanup > 1 {
		return 0, errors.New("invalid batch workload PASS run boolean")
	}
	return value, nil
}

func queryWorkloadPassOriginExecutionsBatch(tx *sql.Tx, ids []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	rows, err := tx.Query(`SELECT job_id, shard_identity, workload_id, status, exit_code, started_at_unix_ms, completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json FROM ci_workload_executions WHERE job_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`) ORDER BY job_id, workload_id`, stringsToAny(ids)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load workload PASS origin executions", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.originExecutionBatchQueries++
	}
	for rows.Next() {
		jobID, execution, err := scanWorkloadPassOriginExecution(rows)
		if err != nil {
			return err
		}
		record, ok := records[jobID]
		if !ok {
			return errors.New("batch workload PASS execution references unknown origin")
		}
		record.WorkloadExecutions = append(record.WorkloadExecutions, execution)
		records[jobID] = record
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate batch workload PASS origin executions", err)
	}
	return nil
}

func scanWorkloadPassOriginExecution(rows interface{ Scan(...any) error }) (string, PlanGateExecution, error) {
	var jobID string
	var execution PlanGateExecution
	var started, completed int64
	var timings, profile string
	if err := rows.Scan(&jobID, &execution.ShardIdentity, &execution.GateID, &execution.Status, &execution.ExitCode, &started, &completed, &execution.ArgvDigest, &execution.LogDigest, &timings, &profile); err != nil {
		return "", PlanGateExecution{}, mapDurationLedgerSQLiteError("scan batch workload PASS origin execution", err)
	}
	var err error
	execution.StartedAt, execution.CompletedAt = time.UnixMilli(started).UTC(), time.UnixMilli(completed).UTC()
	if execution.TestTimings, err = decodeStoredRemoteCIExecutionTestTimings(timings); err != nil {
		return "", PlanGateExecution{}, err
	}
	if execution.ExecutionProfile, err = decodeStoredRemoteCIExecutionProfile(profile); err != nil {
		return "", PlanGateExecution{}, err
	}
	if err := execution.ExecutionProfile.Validate(); err != nil {
		return "", PlanGateExecution{}, errors.New("stored batch workload PASS execution profile is invalid")
	}
	if err := validateWorkloadPassOriginExecutionFlags(execution); err != nil {
		return "", PlanGateExecution{}, err
	}
	return jobID, execution, nil
}

func validateWorkloadPassOriginExecutionFlags(execution PlanGateExecution) error {
	expected, err := WorkloadExecutionGoFlags(string(execution.GateID))
	if err != nil {
		return err
	}
	if execution.ExecutionProfile.GoFlags != expected {
		return fmt.Errorf("stored batch workload %q profile GoFlags drifted", execution.GateID)
	}
	return nil
}

func queryWorkloadPassOriginResultsBatch(tx *sql.Tx, ids []string, records map[string]RemoteCIRunRecord, stats *workloadPassEvidenceLookupStats) error {
	rows, err := tx.Query(`SELECT job_id, workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256 FROM ci_run_workload_results WHERE job_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`) ORDER BY job_id, workload_id`, stringsToAny(ids)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load workload PASS origin results", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.originResultBatchQueries++
	}
	for rows.Next() {
		var jobID, workloadID, generation string
		var result RemoteCIWorkloadResult
		if err := rows.Scan(&jobID, &workloadID, &result.Identity.IdentityDigest, &result.Identity.ExecutionDigest, &result.Identity.InputDigest, &result.Identity.EnvironmentDigest, &result.Disposition, &result.OriginJobID, &generation, &result.EvidenceSHA256); err != nil {
			return mapDurationLedgerSQLiteError("scan batch workload PASS origin result", err)
		}
		record, ok := records[jobID]
		if !ok {
			return errors.New("batch workload PASS result references unknown origin")
		}
		value, err := strconv.ParseUint(generation, 10, 64)
		if err != nil || value == 0 {
			return errors.New("stored batch workload PASS result generation is invalid")
		}
		result.Identity.WorkloadID, result.OriginAcceptedGeneration = GateID(workloadID), value
		record.WorkloadResults = append(record.WorkloadResults, result)
		records[jobID] = record
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate batch workload PASS origin results", err)
	}
	return nil
}

func queryWorkloadPassOriginReceiptsBatch(tx *sql.Tx, ids []string, stats *workloadPassEvidenceLookupStats) (map[string][]CheckReceiptRecord, error) {
	rows, err := tx.Query(`SELECT run_id, job_id, candidate_tree_sha, agent_token_digest, accepted_generation, accepted_snapshot_id, required_check, executed, reused, reuse_proof_sha256, passed, force, started_at_unix_ms, completed_at_unix_ms, duration_ms, receipt_sha256 FROM ci_check_receipts WHERE job_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`) ORDER BY job_id, required_check`, stringsToAny(ids)...)
	if err != nil {
		return nil, mapDurationLedgerSQLiteError("batch load workload PASS origin receipts", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.originReceiptSetValidations++
	}
	result := make(map[string][]CheckReceiptRecord, len(ids))
	for rows.Next() {
		receipt, err := scanWorkloadEvidenceReceipt(rows)
		if err != nil {
			return nil, err
		}
		result[receipt.JobID] = append(result[receipt.JobID], receipt)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate batch workload PASS origin receipts", err)
	}
	return result, nil
}

func workloadPassOriginIsRequestedDirect(record RemoteCIRunRecord, evidence []WorkloadPassEvidence) bool {
	for _, item := range evidence {
		if workloadPassEvidenceMatchesOrigin(record, item) && workloadPassOriginHasDirectResult(record, item) {
			return true
		}
	}
	return false
}

func workloadPassEvidenceMatchesOrigin(record RemoteCIRunRecord, item WorkloadPassEvidence) bool {
	return item.OriginJobID == record.JobID && item.OriginAcceptedGeneration == record.AcceptedGeneration
}

func workloadPassOriginHasDirectResult(record RemoteCIRunRecord, item WorkloadPassEvidence) bool {
	for _, result := range record.WorkloadResults {
		if result.Identity == item.Identity && result.Disposition == WorkloadDispositionExecuted && result.OriginJobID == record.JobID && result.EvidenceSHA256 == "" {
			return true
		}
	}
	return false
}

func workloadPassOriginReceiptDigest(record RemoteCIRunRecord, receipts []CheckReceiptRecord) (string, error) {
	if err := validateStoredWorkloadReceiptCollection(receipts); err != nil {
		return "", err
	}
	if len(receipts) == 0 {
		return "", errors.New("batch workload PASS origin receipts are empty")
	}
	first := receipts[0]
	if first.JobID != record.JobID || first.AgentTokenDigest != record.AgentTokenDigest || first.Force != record.Force || first.CandidateTreeSHA != record.SourceTreeSHA || first.AcceptedGeneration != record.AcceptedGeneration || first.AcceptedSnapshotID != record.ImageCacheSnapshotID {
		return "", errors.New("batch workload PASS receipt set does not bind origin")
	}
	return digestWorkloadReceiptSet(receipts)
}
