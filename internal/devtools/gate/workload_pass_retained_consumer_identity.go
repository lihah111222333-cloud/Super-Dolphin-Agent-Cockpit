package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// loadRetainedConsumerExecutionIdentities 批量读取 retained consumer 的不可变执行身份。
func loadRetainedConsumerExecutionIdentities(tx *sql.Tx, jobIDs []string, records map[string]RemoteCIRunRecord, states map[string]remoteCIExecutionIdentityState, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(jobIDs)), ",")
	rows, err := tx.Query(`SELECT job_id, platform, runner_identity_digest, toolchain_digest, execution_mode FROM ci_remote_run_execution_identities WHERE job_id IN (`+placeholders+") ORDER BY job_id", stringsToAny(jobIDs)...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained consumer execution identities", err)
	}
	defer rows.Close()
	incrementRetainedConsumerBatchQueries(stats)
	loaded := make(map[string]struct{}, len(jobIDs))
	for rows.Next() {
		jobID, err := scanAndBindRetainedConsumerExecutionIdentity(rows, records)
		if err != nil {
			return err
		}
		if _, duplicate := loaded[jobID]; duplicate {
			return fmt.Errorf("retained consumer execution identity %q is duplicated", jobID)
		}
		loaded[jobID] = struct{}{}
		states[jobID] = remoteCIExecutionIdentityStatePresent
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate retained consumer execution identities", err)
	}
	for _, jobID := range jobIDs {
		if _, ok := loaded[jobID]; !ok {
			states[jobID] = remoteCIExecutionIdentityStateMissing
		}
	}
	return validateRetainedConsumerExecutionIdentityPresenceWithStates(jobIDs, records, loaded, states)
}

func scanAndBindRetainedConsumerExecutionIdentity(rows interface{ Scan(...any) error }, records map[string]RemoteCIRunRecord) (string, error) {
	var jobID string
	var identity remoteCIRunExecutionIdentity
	if err := rows.Scan(&jobID, &identity.Platform, &identity.RunnerIdentityDigest, &identity.ToolchainDigest, &identity.ExecutionMode); err != nil {
		return "", mapDurationLedgerSQLiteError("scan retained consumer execution identity", err)
	}
	record, ok := records[jobID]
	if !ok {
		return "", errors.New("retained consumer execution identity references an unknown run")
	}
	identityRecord := RemoteCIRunRecord{Platform: identity.Platform, RunnerIdentityDigest: identity.RunnerIdentityDigest, ToolchainDigest: identity.ToolchainDigest, ExecutionMode: identity.ExecutionMode}
	if err := validateRemoteCIRunExecutionIdentity(identityRecord); err != nil {
		return "", fmt.Errorf("stored retained consumer execution identity %q: %w", jobID, err)
	}
	record.Platform, record.RunnerIdentityDigest = identity.Platform, identity.RunnerIdentityDigest
	record.ToolchainDigest, record.ExecutionMode = identity.ToolchainDigest, identity.ExecutionMode
	records[jobID] = record
	return jobID, nil
}

func validateRetainedConsumerExecutionIdentityPresence(jobIDs []string, records map[string]RemoteCIRunRecord, loaded map[string]struct{}) error {
	return validateRetainedConsumerExecutionIdentityPresenceWithStates(jobIDs, records, loaded, nil)
}

func validateRetainedConsumerExecutionIdentityPresenceWithStates(jobIDs []string, records map[string]RemoteCIRunRecord, loaded map[string]struct{}, states map[string]remoteCIExecutionIdentityState) error {
	for _, jobID := range jobIDs {
		if _, ok := loaded[jobID]; ok {
			continue
		}
		record, ok := records[jobID]
		if !ok {
			return errors.New("retained consumer execution identity references an unknown run")
		}
		if !retainedConsumerLegacyExecutionIdentityRead(record, states[jobID]) {
			return fmt.Errorf("retained consumer execution identity %q is missing", jobID)
		}
	}
	return nil
}

// retainedConsumerLegacyExecutionIdentityRead 只接受 SQL 明确缺行且记录未携带 partial identity 的 legacy PASS。
func retainedConsumerLegacyExecutionIdentityRead(record RemoteCIRunRecord, state remoteCIExecutionIdentityState) bool {
	return record.Authoritative && record.Status == ResultStatusPassed && state == remoteCIExecutionIdentityStateMissing &&
		strings.TrimSpace(record.Platform) == "" && strings.TrimSpace(record.RunnerIdentityDigest) == "" &&
		strings.TrimSpace(record.ToolchainDigest) == "" && strings.TrimSpace(record.ExecutionMode) == ""
}
