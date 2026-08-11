package gate

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// loadRetainedWorkloadPassProofBatches reads only v16 consumer-owned proof
// rows. It never falls back to the retired v15 evidence projection.
func loadRetainedWorkloadPassProofBatches(tx *sql.Tx, evidence []WorkloadPassEvidence, current uint64, out map[string]retainedWorkloadPassProofRow, stats *workloadPassEvidenceLookupStats) error {
	identities := make([]string, 0, len(evidence))
	seen := make(map[string]struct{}, len(evidence))
	for _, item := range evidence {
		if _, ok := seen[item.Identity.IdentityDigest]; !ok {
			seen[item.Identity.IdentityDigest] = struct{}{}
			identities = append(identities, item.Identity.IdentityDigest)
		}
	}
	retained := retainedWorkloadPassGenerations(current)
	for start := 0; start < len(identities); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(identities))
		if err := loadRetainedWorkloadPassProofBatchChunk(tx, identities[start:end], retained, current, out, stats); err != nil {
			return err
		}
	}
	return validateRetainedBatchConsumers(tx, out, stats)
}

// validateRetainedBatchConsumers validates the independent consumer aggregate
// after the proof rows have been selected. A proof is never a second authority.
func validateRetainedBatchConsumers(tx *sql.Tx, proofs map[string]retainedWorkloadPassProofRow, stats *workloadPassEvidenceLookupStats) error {
	consumerIDs := make([]string, 0, len(proofs))
	seen := make(map[string]struct{}, len(proofs))
	for _, proof := range proofs {
		if _, exists := seen[proof.consumerID]; !exists {
			seen[proof.consumerID] = struct{}{}
			consumerIDs = append(consumerIDs, proof.consumerID)
		}
	}
	consumers, err := loadRetainedConsumerRecords(tx, consumerIDs, stats)
	if err != nil {
		return err
	}
	for _, proof := range proofs {
		consumer, ok := consumers[proof.consumerID]
		if !ok || !consumer.Authoritative || consumer.Status != ResultStatusPassed || !consumer.CleanupComplete || proof.consumerGeneration != strconv.FormatUint(consumer.AcceptedGeneration, 10) {
			return errors.New("retained workload pass proof consumer is not an authoritative cleaned PASS")
		}
	}
	return nil
}

func loadRetainedWorkloadPassProofBatchChunk(tx *sql.Tx, identities []string, retained [3]string, current uint64, out map[string]retainedWorkloadPassProofRow, stats *workloadPassEvidenceLookupStats) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(identities)), ",")
	args := append(stringsToAny(identities), retained[0], retained[1], retained[2])
	rows, err := tx.Query(`SELECT proof.consumer_job_id, COALESCE(consumer.accepted_generation, ''), COALESCE(result.workload_id, ''), COALESCE(result.identity_digest, ''), COALESCE(result.execution_digest, ''), COALESCE(result.input_digest, ''), COALESCE(result.environment_digest, ''), COALESCE(result.disposition, ''), COALESCE(result.origin_job_id, ''), COALESCE(result.origin_accepted_generation, ''), COALESCE(result.evidence_sha256, ''), proof.origin_job_id, proof.origin_accepted_generation, proof.origin_source_tree_sha, proof.origin_receipt_set_sha256, proof.origin_execution_json, proof.evidence_sha256 FROM ci_retained_workload_pass_proofs AS proof LEFT JOIN ci_runs AS consumer ON consumer.job_id = proof.consumer_job_id LEFT JOIN ci_run_workload_results AS result ON result.job_id = proof.consumer_job_id AND result.workload_id = proof.workload_id WHERE proof.identity_digest IN (`+placeholders+`) AND (consumer.accepted_generation IN (?, ?, ?) OR consumer.job_id IS NULL) ORDER BY proof.identity_digest, consumer.accepted_generation DESC`, args...)
	if err != nil {
		return mapDurationLedgerSQLiteError("batch load retained workload PASS proofs", err)
	}
	defer rows.Close()
	if stats != nil {
		stats.retainedProofBatchQueries++
	}
	for rows.Next() {
		var row retainedWorkloadPassProofRow
		if err := rows.Scan(&row.consumerID, &row.consumerGeneration, &row.workloadID, &row.identityDigest, &row.executionDigest, &row.inputDigest, &row.environmentDigest, &row.disposition, &row.originID, &row.originGeneration, &row.resultDigest, &row.proofOriginID, &row.proofOriginGeneration, &row.sourceTreeSHA, &row.receiptSHA, &row.executionJSON, &row.proofDigest); err != nil {
			return mapDurationLedgerSQLiteError("scan batch retained workload PASS proof", err)
		}
		if _, already := out[row.identityDigest]; already {
			continue
		}
		if err := row.validateConsumerGeneration(current); err != nil {
			return err
		}
		if _, err := strconv.ParseUint(row.originGeneration, 10, 64); err != nil || row.originGeneration == "0" {
			return errors.New("batch retained workload PASS proof origin generation is invalid")
		}
		out[row.identityDigest] = row
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate batch retained workload PASS proofs", err)
	}
	return nil
}
