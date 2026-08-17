package gate

import "time"

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
		execution.ExecutionProfile, err = decodeStoredRemoteCIAggregateExecutionProfile(profileJSON)
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
