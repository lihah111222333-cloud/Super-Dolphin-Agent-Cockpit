package gate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestWorkloadPassEvidenceBatchLookupSharesOriginValidationAtScale guards the
// second-run hot path: thousands of identities from one origin must use bounded
// identity batches while loading and validating that origin exactly once.
func TestWorkloadPassEvidenceBatchLookupSharesOriginValidationAtScale(t *testing.T) {
	const evidenceCount = 4989
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "batch-scale-origin", 1, "batch-scale")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatalf("finalize origin run: %v", err)
	}
	originEvidence := lookupSingleWorkloadPassEvidence(t, store, record.WorkloadResults[0].Identity)

	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	insertBatchScaleEvidence(t, database, originEvidence, evidenceCount)

	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	currentGeneration, err := currentAcceptedBaselineGeneration(transaction)
	if err != nil {
		t.Fatal(err)
	}
	identities := batchScaleIdentities(t, originEvidence, evidenceCount)
	stats := workloadPassEvidenceLookupStats{}
	evidence, err := loadWorkloadPassEvidenceForIdentitiesWithStats(transaction, identities, currentGeneration, &stats)
	if err != nil {
		t.Fatalf("batch-scale lookup: %v", err)
	}
	if len(evidence) != evidenceCount {
		t.Fatalf("batch-scale evidence count = %d, want %d", len(evidence), evidenceCount)
	}
	wantBatches := (evidenceCount + workloadPassEvidenceLookupBatchSize - 1) / workloadPassEvidenceLookupBatchSize
	if stats.identityBatchQueries != wantBatches {
		t.Fatalf("identity batch query count = %d, want %d", stats.identityBatchQueries, wantBatches)
	}
	if stats.originRunLoads != 1 || stats.originReceiptSetValidations != 1 {
		t.Fatalf("origin validation counts = run:%d receipt:%d, want run:1 receipt:1", stats.originRunLoads, stats.originReceiptSetValidations)
	}
}

// TestWorkloadPassEvidenceBatchLookupDistinctOriginsIsConstantQuery exposes
// the pre-batching N+1 origin readback path. It intentionally fails until
// origin run, execution, result, and receipt projections are loaded in fixed
// batches rather than once per direct origin.
func TestWorkloadPassEvidenceBatchLookupDistinctOriginsIsConstantQuery(t *testing.T) {
	const originCount = 64
	store := newWorkloadPassEvidenceStore(t, 1)
	identities := make([]WorkloadPassIdentity, 0, originCount)
	for index := range originCount {
		label := strings.Repeat("x", index+1)
		record, identity, receipts := recordWorkloadPassRun(t, store, fmt.Sprintf("distinct-origin-%s", label), 1, fmt.Sprintf("distinct-origin-workload-%s", label))
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatalf("finalize distinct origin %d: %v", index, err)
		}
		identities = append(identities, identity)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	currentGeneration, err := currentAcceptedBaselineGeneration(transaction)
	if err != nil {
		t.Fatal(err)
	}
	stats := workloadPassEvidenceLookupStats{}
	evidence, err := loadWorkloadPassEvidenceForIdentitiesWithStats(transaction, identities, currentGeneration, &stats)
	if err != nil {
		t.Fatalf("distinct-origin lookup: %v", err)
	}
	if len(evidence) != originCount {
		t.Fatalf("distinct-origin evidence count = %d, want %d", len(evidence), originCount)
	}
	assertDistinctDirectOriginBatchQueries(t, stats)
}

func assertDistinctDirectOriginBatchQueries(t *testing.T, stats workloadPassEvidenceLookupStats) {
	t.Helper()
	if stats.originRunLoads != 1 {
		t.Fatalf("distinct direct run batch queries = %d, want 1", stats.originRunLoads)
	}
	if stats.originExecutionBatchQueries != 1 {
		t.Fatalf("distinct direct execution batch queries = %d, want 1", stats.originExecutionBatchQueries)
	}
	if stats.originResultBatchQueries != 1 {
		t.Fatalf("distinct direct result batch queries = %d, want 1", stats.originResultBatchQueries)
	}
	if stats.originReceiptSetValidations != 1 {
		t.Fatalf("distinct direct receipt batch queries = %d, want 1", stats.originReceiptSetValidations)
	}
}

func TestWorkloadPassEvidenceRetainedConsumersBatchLookupIsConstantQuery(t *testing.T) {
	counts := make(map[int]workloadPassEvidenceLookupStats, 2)
	for _, consumerCount := range []int{1, 64} {
		counts[consumerCount] = retainedConsumerBatchLookupStats(t, consumerCount)
	}
	if counts[1].retainedProofBatchQueries == 0 || counts[1].retainedConsumerBatchQueries == 0 {
		t.Fatalf("retained batch query counters = proof:%d consumer:%d, want both positive", counts[1].retainedProofBatchQueries, counts[1].retainedConsumerBatchQueries)
	}
	if counts[1].retainedProofBatchQueries != counts[64].retainedProofBatchQueries || counts[1].retainedConsumerBatchQueries != counts[64].retainedConsumerBatchQueries {
		t.Fatalf("retained batch query counters drifted: one=%d/%d sixty-four=%d/%d", counts[1].retainedProofBatchQueries, counts[1].retainedConsumerBatchQueries, counts[64].retainedProofBatchQueries, counts[64].retainedConsumerBatchQueries)
	}
}

// retainedConsumerBatchLookupStats 运行固定 consumer 投影，供一与六十四消费者规模比较。
func retainedConsumerBatchLookupStats(t *testing.T, consumerCount int) workloadPassEvidenceLookupStats {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	origins, proofs := recordRetainedBatchOrigins(t, store, consumerCount)
	seedAcceptedGenerationForTest(t, store, 2)
	finalizeRetainedBatchConsumers(t, store, origins, proofs)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stats, loaded := workloadPassEvidenceLookupStats{}, make(map[string]retainedWorkloadPassProofRow)
	if err := loadRetainedWorkloadPassProofBatches(tx, proofs, 2, loaded, &stats); err != nil {
		t.Fatal(err)
	}
	if len(loaded) != consumerCount {
		t.Fatalf("retained proofs = %d, want %d", len(loaded), consumerCount)
	}
	return stats
}

func TestWorkloadPassEvidenceLookupRetainedV16ProofRejectsTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		column string
		value  string
	}{
		{name: "canonical receipt digest", column: "origin_receipt_set_sha256", value: "sha256:" + strings.Repeat("0", 64)},
		{name: "evidence digest", column: "evidence_sha256", value: "sha256:" + strings.Repeat("1", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, identity := retainedV16LookupFixture(t)
			assertRetainedV16LookupHit(t, store, identity)
			database := openWorkloadPassDatabase(t, store)
			if _, err := database.Exec(`UPDATE ci_retained_workload_pass_proofs SET `+test.column+` = ? WHERE identity_digest = ?`, test.value, identity.IdentityDigest); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			assertRetainedV16LookupFails(t, store, identity, "tampered retained v16 proof")
		})
	}
}

// TestWorkloadPassEvidenceLookupRetainedV16BindingsFailFast makes an evicted
// direct origin observable through its retained consumer. No retained binding
// may be filtered into a successful zero-hit lookup.
func TestWorkloadPassEvidenceLookupRetainedV16BindingsFailFast(t *testing.T) {
	for _, test := range []struct {
		name     string
		mutation string
	}{
		{name: "consumer authoritative", mutation: `UPDATE ci_runs SET authoritative = 0 WHERE job_id = 'mixed-consumer'`},
		{name: "consumer status", mutation: `UPDATE ci_runs SET status = 'failed' WHERE job_id = 'mixed-consumer'`},
		{name: "consumer cleanup", mutation: `UPDATE ci_runs SET cleanup_complete = 0 WHERE job_id = 'mixed-consumer'`},
		{name: "result identity", mutation: `UPDATE ci_run_workload_results SET identity_digest = 'sha256:tampered' WHERE job_id = 'mixed-consumer'`},
		{name: "result disposition", mutation: `UPDATE ci_run_workload_results SET disposition = 'executed' WHERE job_id = 'mixed-consumer'`},
		{name: "result origin", mutation: `UPDATE ci_run_workload_results SET origin_job_id = 'tampered-origin' WHERE job_id = 'mixed-consumer'`},
		{name: "result origin generation", mutation: `UPDATE ci_run_workload_results SET origin_accepted_generation = '2' WHERE job_id = 'mixed-consumer'`},
		{name: "result evidence", mutation: `UPDATE ci_run_workload_results SET evidence_sha256 = 'sha256:tampered' WHERE job_id = 'mixed-consumer'`},
		{name: "proof workload", mutation: `UPDATE ci_retained_workload_pass_proofs SET workload_id = 'tampered-workload' WHERE consumer_job_id = 'mixed-consumer'`},
		{name: "proof origin", mutation: `UPDATE ci_retained_workload_pass_proofs SET origin_job_id = 'tampered-origin' WHERE consumer_job_id = 'mixed-consumer'`},
		{name: "proof origin generation", mutation: `UPDATE ci_retained_workload_pass_proofs SET origin_accepted_generation = '2' WHERE consumer_job_id = 'mixed-consumer'`},
		{name: "proof source tree", mutation: `UPDATE ci_retained_workload_pass_proofs SET origin_source_tree_sha = '' WHERE consumer_job_id = 'mixed-consumer'`},
		{name: "proof receipt", mutation: `UPDATE ci_retained_workload_pass_proofs SET origin_receipt_set_sha256 = '' WHERE consumer_job_id = 'mixed-consumer'`},
		{name: "proof execution", mutation: `UPDATE ci_retained_workload_pass_proofs SET origin_execution_json = '' WHERE consumer_job_id = 'mixed-consumer'`},
		{name: "proof evidence", mutation: `UPDATE ci_retained_workload_pass_proofs SET evidence_sha256 = '' WHERE consumer_job_id = 'mixed-consumer'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, identity := retainedV16LookupFixture(t)
			database := openWorkloadPassDatabase(t, store)
			if _, err := database.Exec(test.mutation); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			assertRetainedV16LookupFails(t, store, identity, test.name)
		})
	}
}

// TestWorkloadPassEvidenceLookupRetainedV16MissingBindingsFailFast ensures a
// retained proof cannot turn a missing consumer or result projection into MISS.
func TestWorkloadPassEvidenceLookupRetainedV16MissingBindingsFailFast(t *testing.T) {
	for _, test := range []struct {
		name       string
		statements []string
	}{
		{name: "consumer", statements: []string{
			`PRAGMA foreign_keys = OFF`,
			`DELETE FROM ci_runs WHERE job_id = 'mixed-consumer'`,
		}},
		{name: "result", statements: []string{
			`DELETE FROM ci_run_workload_results WHERE job_id = 'mixed-consumer'`,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, identity := retainedV16LookupFixture(t)
			database := openWorkloadPassDatabase(t, store)
			for _, statement := range test.statements {
				if _, err := database.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			assertRetainedV16LookupFails(t, store, identity, "missing retained "+test.name)
		})
	}
}

// TestRetainedBatchRejectsInvalidConsumerAggregate proves that the production
// batch path keeps the retained consumer as the authoritative aggregate rather
// than trusting its proof-row SQL projection.
func TestRetainedBatchRejectsInvalidConsumerAggregate(t *testing.T) {
	store, identity := retainedV16LookupFixture(t)
	assertRetainedV16LookupHit(t, store, identity)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_runs
		SET runner_image = ''
		WHERE job_id = (
			SELECT consumer_job_id
			FROM ci_retained_workload_pass_proofs
			WHERE identity_digest = ?
		)`, identity.IdentityDigest); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	assertRetainedV16LookupFails(t, store, identity, "retained consumer aggregate with missing runner image")
}

func TestRetainedBatchRejectsUnknownTerminalEvidence(t *testing.T) {
	store, identity := retainedV16LookupFixture(t)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_shard_terminal_events(job_id, shard_identity, ordinal, type, reason, message, count, last_timestamp) VALUES ('mixed-consumer', 'unknown-shard', 0, 'Warning', 'BackOff', 'unknown shard', 1, '2026-08-03T14:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	assertRetainedV16LookupFails(t, store, identity, "unknown retained terminal shard")
}

func TestRetainedBatchRejectsTamperedTerminalEvidence(t *testing.T) {
	store, identity := retainedV16LookupFixture(t)
	database := openWorkloadPassDatabase(t, store)
	var shardID string
	if err := database.QueryRow(`SELECT shard_identity FROM ci_shards WHERE job_id = 'mixed-consumer'`).Scan(&shardID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_shard_terminal_containers(job_id, shard_identity, container_kind, ordinal, name, state, reason, message) VALUES ('mixed-consumer', ?, 'container', 0, 'worker', 'tampered', 'BackOff', 'invalid terminal state')`, shardID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	assertRetainedV16LookupFails(t, store, identity, "tampered retained terminal evidence")
}

func TestRetainedBatchRejectsConsumerReceiptAndCatalogDrift(t *testing.T) {
	for _, test := range []struct {
		name       string
		statements []string
	}{
		{name: "receipt digest", statements: []string{`UPDATE ci_check_receipts SET receipt_sha256 = 'sha256:tampered' WHERE job_id = 'mixed-consumer'`}},
		{name: "catalog", statements: []string{`UPDATE ci_runs SET catalog_digest = 'sha256:tampered' WHERE job_id = 'mixed-consumer'`}},
		{name: "receipt job", statements: []string{`PRAGMA foreign_keys = OFF`, `UPDATE ci_check_receipts SET job_id = 'tampered-consumer' WHERE job_id = 'mixed-consumer'`}},
		{name: "receipt agent", statements: []string{`UPDATE ci_check_receipts SET agent_token_digest = 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE job_id = 'mixed-consumer'`}},
		{name: "receipt tree", statements: []string{`UPDATE ci_check_receipts SET candidate_tree_sha = 'cccccccccccccccccccccccccccccccccccccccc' WHERE job_id = 'mixed-consumer'`}},
		{name: "receipt force", statements: []string{`UPDATE ci_check_receipts SET force = CASE force WHEN 1 THEN 0 ELSE 1 END WHERE job_id = 'mixed-consumer'`}},
		{name: "receipt generation", statements: []string{`UPDATE ci_check_receipts SET accepted_generation = '1' WHERE job_id = 'mixed-consumer'`}},
		{name: "receipt snapshot", statements: []string{`UPDATE ci_check_receipts SET accepted_snapshot_id = 'snapshot-tampered' WHERE job_id = 'mixed-consumer'`}},
		{name: "missing receipt", statements: []string{`DELETE FROM ci_check_receipts WHERE job_id = 'mixed-consumer'`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, identity := retainedV16LookupFixture(t)
			database := openWorkloadPassDatabase(t, store)
			for _, statement := range test.statements {
				if _, err := database.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			assertRetainedV16LookupFails(t, store, identity, test.name)
		})
	}
}

// TestRetainedBatchRejectsConsumerReceiptsFromAnotherRunWithValidDigests proves
// that a mutually consistent forged receipt set cannot substitute another run.
func TestRetainedBatchRejectsConsumerReceiptsFromAnotherRunWithValidDigests(t *testing.T) {
	store, identity := retainedV16LookupFixture(t)
	database := openWorkloadPassDatabase(t, store)
	receipts := loadCheckReceiptsForEvidenceForTest(t, database, "mixed-consumer")
	for index := range receipts {
		receipts[index].RunID = "other-legitimate-run"
		var err error
		receipts[index].ReceiptSHA256, err = CheckReceiptSHA256(receipts[index])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE ci_check_receipts SET run_id = ?, receipt_sha256 = ? WHERE job_id = ? AND required_check = ?`, receipts[index].RunID, receipts[index].ReceiptSHA256, "mixed-consumer", receipts[index].RequiredCheck); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	assertRetainedV16LookupFails(t, store, identity, "rehashed retained receipts from another run")
}

func loadCheckReceiptsForEvidenceForTest(t *testing.T, database *sql.DB, jobID string) []CheckReceiptRecord {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	receipts, err := loadCheckReceiptsForEvidence(transaction, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) == 0 {
		t.Fatal("test fixture has no check receipts")
	}
	return receipts
}

func TestValidateCheckReceiptsAgainstRemoteRunRejectsEachRunBinding(t *testing.T) {
	record := RemoteCIRunRecord{
		JobID:                "receipt-binding-job",
		AgentTokenDigest:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		Force:                false,
		SourceTreeSHA:        "2222222222222222222222222222222222222222",
		AcceptedGeneration:   2,
		ImageCacheSnapshotID: "snapshot-2",
	}
	receipts := []CheckReceiptRecord{
		{
			RunID:              record.JobID,
			JobID:              record.JobID,
			AgentTokenDigest:   record.AgentTokenDigest,
			Force:              record.Force,
			CandidateTreeSHA:   record.SourceTreeSHA,
			AcceptedGeneration: record.AcceptedGeneration,
			AcceptedSnapshotID: record.ImageCacheSnapshotID,
		},
		{
			RunID:              record.JobID,
			JobID:              record.JobID,
			AgentTokenDigest:   record.AgentTokenDigest,
			Force:              record.Force,
			CandidateTreeSHA:   record.SourceTreeSHA,
			AcceptedGeneration: record.AcceptedGeneration,
			AcceptedSnapshotID: record.ImageCacheSnapshotID,
		},
	}
	if err := validateCheckReceiptsAgainstRemoteRun(record, receipts); err != nil {
		t.Fatalf("valid receipt binding: %v", err)
	}
	if err := validateCheckReceiptsAgainstRemoteRun(record, nil); err == nil {
		t.Fatal("missing receipt binding was accepted")
	}
	for _, test := range []struct {
		name   string
		mutate func(*CheckReceiptRecord)
	}{
		{name: "job", mutate: func(receipt *CheckReceiptRecord) { receipt.JobID = "other-job" }},
		{name: "run", mutate: func(receipt *CheckReceiptRecord) { receipt.RunID = "other-legitimate-run" }},
		{name: "agent", mutate: func(receipt *CheckReceiptRecord) {
			receipt.AgentTokenDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}},
		{name: "tree", mutate: func(receipt *CheckReceiptRecord) {
			receipt.CandidateTreeSHA = "3333333333333333333333333333333333333333"
		}},
		{name: "force", mutate: func(receipt *CheckReceiptRecord) { receipt.Force = true }},
		{name: "generation", mutate: func(receipt *CheckReceiptRecord) { receipt.AcceptedGeneration = 1 }},
		{name: "snapshot", mutate: func(receipt *CheckReceiptRecord) { receipt.AcceptedSnapshotID = "snapshot-other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			tampered := append([]CheckReceiptRecord(nil), receipts...)
			test.mutate(&tampered[1])
			if err := validateCheckReceiptsAgainstRemoteRun(record, tampered); err == nil {
				t.Fatal("tampered receipt binding was accepted")
			}
		})
	}
}

func TestWorkloadPassEvidenceLookupRetainedV16ProofRejectsUnknownJSON(t *testing.T) {
	store, identity := retainedV16LookupFixture(t)
	assertRetainedV16LookupHit(t, store, identity)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_retained_workload_pass_proofs SET origin_execution_json = substr(origin_execution_json, 1, length(origin_execution_json) - 1) || ',"unexpected":true}' WHERE identity_digest = ?`, identity.IdentityDigest); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	assertRetainedV16LookupFails(t, store, identity, "unknown retained v16 proof JSON")
}

func assertRetainedV16LookupHit(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	t.Helper()
	publicHits, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatal(err)
	}
	if len(publicHits) != 1 {
		t.Fatalf("public retained v16 lookup hits = %d, want 1", len(publicHits))
	}
	stats := workloadPassEvidenceLookupStats{}
	hits, err := store.lookupWorkloadPassEvidenceWithStats([]WorkloadPassIdentity{identity}, nil, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("retained v16 lookup hits = %d, want 1", len(hits))
	}
	if stats.selectedRetainedSources != 1 || stats.selectedDirectSources != 0 {
		t.Fatalf("retained v16 selected sources = retained:%d direct:%d, want 1/0", stats.selectedRetainedSources, stats.selectedDirectSources)
	}
	if stats.retainedProofBatchQueries != 1 {
		t.Fatalf("retained v16 proof batch queries = %d, want 1", stats.retainedProofBatchQueries)
	}
}

func assertRetainedV16LookupFails(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity, label string) {
	t.Helper()
	if hits, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil {
		t.Fatalf("public %s was accepted as %d hits", label, len(hits))
	}
	stats := workloadPassEvidenceLookupStats{}
	if hits, err := store.lookupWorkloadPassEvidenceWithStats([]WorkloadPassIdentity{identity}, nil, &stats); err == nil {
		t.Fatalf("%s was accepted as %d hits", label, len(hits))
	}
	if stats.selectedDirectSources != 0 {
		t.Fatalf("%s selected direct source = %d, want 0", label, stats.selectedDirectSources)
	}
}

func retainedV16LookupFixture(t *testing.T) (*DurationLedgerStore, WorkloadPassIdentity) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "retained-v16-origin", 1, "mixed-origin", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	proof := lookupSingleWorkloadPassEvidence(t, store, identity)
	seedAcceptedGenerationForTest(t, store, 2)
	consumer, freshIdentity := recordMixedRetentionConsumer(t, store, origin, proof)
	finalizeMixedRetentionConsumer(t, store, consumer, freshIdentity)
	seedAcceptedGenerationForTest(t, store, 4)
	recordMixedRetentionTriggers(t, store, 3, 4)
	assertRetentionRunDeleted(t, store, origin.JobID, "direct origin")
	assertStaleGenerationAbsentFromSevenRetentionRoots(t, store, 1)
	assertMixedRetentionConsumerKept(t, store, consumer, freshIdentity, identity)
	return store, identity
}

func recordRetainedBatchOrigins(t *testing.T, store *DurationLedgerStore, count int) ([]RemoteCIRunRecord, []WorkloadPassEvidence) {
	t.Helper()
	origins, proofs := make([]RemoteCIRunRecord, 0, count), make([]WorkloadPassEvidence, 0, count)
	for index := range count {
		material := strings.Repeat("x", index+1)
		origin, identity, receipts := recordWorkloadPassRun(t, store, "retained-origin-"+material, 1, "retained-workload-"+material)
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
			t.Fatal(err)
		}
		origins, proofs = append(origins, origin), append(proofs, lookupSingleWorkloadPassEvidence(t, store, identity))
	}
	return origins, proofs
}

func finalizeRetainedBatchConsumers(t *testing.T, store *DurationLedgerStore, origins []RemoteCIRunRecord, proofs []WorkloadPassEvidence) {
	t.Helper()
	for index, origin := range origins {
		consumer := retainedBatchConsumer(origin, proofs[index], index)
		recordRetainedConsumerCatalogObservation(t, store, consumer)
		if err := store.RecordProvisionalRemoteCIRun(consumer); err != nil {
			t.Fatal(err)
		}
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(consumer), completeWorkloadPassReceipts(t, consumer), nil, false); err != nil {
			t.Fatal(err)
		}
	}
}

func recordRetainedConsumerCatalogObservation(t *testing.T, store *DurationLedgerStore, consumer RemoteCIRunRecord) {
	t.Helper()
	record, err := store.LoadWorkloadCatalogRecord(consumer.CatalogDigest)
	if err != nil {
		t.Fatal(err)
	}
	observation := WorkloadCatalogObservation{SourceTreeSHA: consumer.SourceTreeSHA, Entrypoint: consumer.Entrypoint, Profile: consumer.Profile, AcceptedGeneration: consumer.AcceptedGeneration, ObservedAt: consumer.StartedAt}
	if err := store.RecordWorkloadCatalog(record.Catalog, observation); err != nil {
		t.Fatal(err)
	}
}

func retainedBatchConsumer(origin RemoteCIRunRecord, proof WorkloadPassEvidence, index int) RemoteCIRunRecord {
	consumer := origin
	consumer.JobID, consumer.AgentTokenDigest = fmt.Sprintf("retained-consumer-%03d", index), digestForWorkloadPass(fmt.Sprintf("retained-consumer-agent-%03d", index))
	consumer.AcceptedGeneration, consumer.ImageCacheSnapshotID, consumer.Authoritative = 2, "snapshot-2", false
	consumer.StartedAt, consumer.CompletedAt = origin.StartedAt.Add(time.Duration(index+1)*time.Hour), origin.StartedAt.Add(time.Duration(index+1)*time.Hour+time.Second)
	consumer.Shards, consumer.WorkloadExecutions, consumer.TimingObservations = nil, nil, nil
	consumer.CandidateGateSourceSHA256, consumer.CandidateGateToolchainSHA256 = "", ""
	consumer.WorkloadResults = []RemoteCIWorkloadResult{{Identity: proof.Identity, Disposition: WorkloadDispositionReused, OriginJobID: proof.OriginJobID, OriginAcceptedGeneration: proof.OriginAcceptedGeneration, EvidenceSHA256: proof.EvidenceSHA256}}
	return consumer
}

// TestLocalWorkloadPassLookupDistinctOriginsIsConstantQuery proves that
// local PASS must not turn a batched identity query into one origin load per hit.
func TestLocalWorkloadPassLookupDistinctOriginsIsConstantQuery(t *testing.T) {
	const originCount = 64
	store, batch, first := localPassAuthorityFixture(t)
	identities := make([]WorkloadPassIdentity, 0, originCount)
	identities = append(identities, first)
	for index := 1; index < originCount; index++ {
		candidate := batch
		candidate.Origin.RunID = fmt.Sprintf("local-distinct-origin-%s", strings.Repeat("x", index+1))
		candidate.Origin.StartedAt = candidate.Origin.StartedAt.Add(time.Duration(index) * time.Hour)
		candidate.Origin.CompletedAt = candidate.Origin.CompletedAt.Add(time.Duration(index) * time.Hour)
		entry := candidate.Entries[0]
		entry.Identity.InputDigest = digestForWorkloadPass(strings.Repeat("local-input-", index+1))
		entry.Identity.IdentityDigest = workloadPassIdentityDigest(t, entry.Identity)
		candidate.Entries = []LocalWorkloadPassEntry{entry}
		projection, err := LocalWorkloadPassProjectionDigest(candidate.Origin, candidate.Entries)
		if err != nil {
			t.Fatal(err)
		}
		candidate.Origin.ProjectionDigest = projection
		if err := store.RecordLocalWorkloadPassBatch(candidate); err != nil {
			t.Fatalf("record local distinct origin %d: %v", index, err)
		}
		identities = append(identities, entry.Identity)
	}
	stats := localWorkloadPassLookupStats{}
	hits, err := store.lookupLocalWorkloadPassEvidenceWithStats(identities, &stats)
	if err != nil {
		t.Fatalf("local distinct-origin lookup: %v", err)
	}
	if len(hits) != originCount {
		t.Fatalf("local distinct-origin hits = %d, want %d", len(hits), originCount)
	}
	if stats.originBatchQueries > 2 {
		t.Fatalf("local origin projection query count = %d for %d distinct origins; want at most 2", stats.originBatchQueries, originCount)
	}
}

func batchScaleIdentities(t *testing.T, origin WorkloadPassEvidence, count int) []WorkloadPassIdentity {
	t.Helper()
	identities := make([]WorkloadPassIdentity, count)
	identities[0] = origin.Identity
	for index := 1; index < count; index++ {
		workloadID := GateID(fmt.Sprintf("batch-scale-workload-%04d", index))
		identity := WorkloadPassIdentity{
			WorkloadID:        workloadID,
			ExecutionDigest:   digestForWorkloadPass(fmt.Sprintf("execution-%d", index)),
			InputDigest:       digestForWorkloadPass(fmt.Sprintf("input-%d", index)),
			EnvironmentDigest: digestForWorkloadPass(fmt.Sprintf("environment-%d", index)),
		}
		var err error
		identity.IdentityDigest, err = WorkloadPassIdentitySHA256(identity)
		if err != nil {
			t.Fatal(err)
		}
		identities[index] = identity
	}
	return identities
}

func insertBatchScaleEvidence(t *testing.T, database *sql.DB, origin WorkloadPassEvidence, count int) {
	t.Helper()
	identities := batchScaleIdentities(t, origin, count)
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	for index, identity := range identities {
		if index == 0 {
			continue
		}
		execution := origin.OriginExecution
		execution.GateID = identity.WorkloadID
		execution.ExecutionProfile.GoFlags = ""
		evidence := WorkloadPassEvidence{
			Identity:                 identity,
			OriginJobID:              origin.OriginJobID,
			OriginAcceptedGeneration: origin.OriginAcceptedGeneration,
			OriginSourceTreeSHA:      origin.OriginSourceTreeSHA,
			OriginReceiptSetSHA256:   origin.OriginReceiptSetSHA256,
			OriginExecution:          execution,
		}
		evidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(evidence)
		if err != nil {
			t.Fatal(err)
		}
		encodedExecution, err := json.Marshal(execution)
		if err != nil {
			t.Fatal(err)
		}
		insertBatchScaleOriginExecution(t, transaction, origin.OriginJobID, execution)
		if _, err := transaction.Exec(`INSERT INTO ci_run_workload_results (job_id, workload_id, identity_digest, execution_digest, input_digest, environment_digest, disposition, origin_job_id, origin_accepted_generation, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, 'executed', ?, ?, '')`, origin.OriginJobID, identity.WorkloadID, identity.IdentityDigest, identity.ExecutionDigest, identity.InputDigest, identity.EnvironmentDigest, origin.OriginJobID, fmt.Sprintf("%d", evidence.OriginAcceptedGeneration)); err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.Exec(`INSERT INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, identity.IdentityDigest, fmt.Sprintf("%d", evidence.OriginAcceptedGeneration), identity.WorkloadID, identity.ExecutionDigest, identity.InputDigest, identity.EnvironmentDigest, evidence.OriginJobID, evidence.OriginSourceTreeSHA, evidence.OriginReceiptSetSHA256, string(encodedExecution), evidence.EvidenceSHA256); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

// insertBatchScaleOriginExecution 为 synthetic proof 写入严格对应的 origin execution projection。
func insertBatchScaleOriginExecution(t *testing.T, transaction *sql.Tx, jobID string, execution PlanGateExecution) {
	t.Helper()
	testTimings, err := json.Marshal(execution.TestTimings)
	if err != nil {
		t.Fatal(err)
	}
	encodedProfile, err := json.Marshal(execution.ExecutionProfile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(`INSERT INTO ci_workload_executions (job_id, shard_identity, workload_id, status, exit_code, started_at_unix_ms, completed_at_unix_ms, argv_digest, log_digest, test_timings_json, execution_profile_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, jobID, execution.ShardIdentity, execution.GateID, string(execution.Status), execution.ExitCode, execution.StartedAt.UTC().UnixMilli(), execution.CompletedAt.UTC().UnixMilli(), execution.ArgvDigest, execution.LogDigest, string(testTimings), string(encodedProfile)); err != nil {
		t.Fatal(err)
	}
}
