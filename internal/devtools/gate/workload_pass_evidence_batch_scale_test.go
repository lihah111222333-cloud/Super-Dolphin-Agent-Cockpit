package gate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
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
		if _, err := transaction.Exec(`INSERT INTO ci_workload_pass_evidence (identity_digest, accepted_generation, workload_id, execution_digest, input_digest, environment_digest, origin_job_id, origin_source_tree_sha, origin_receipt_set_sha256, origin_execution_json, evidence_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, identity.IdentityDigest, fmt.Sprintf("%d", evidence.OriginAcceptedGeneration), identity.WorkloadID, identity.ExecutionDigest, identity.InputDigest, identity.EnvironmentDigest, evidence.OriginJobID, evidence.OriginSourceTreeSHA, evidence.OriginReceiptSetSHA256, string(encodedExecution), evidence.EvidenceSHA256); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}
