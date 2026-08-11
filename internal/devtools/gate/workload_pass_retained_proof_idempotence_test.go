package gate

import (
	"slices"
	"strings"
	"testing"
)

// TestRecordProvisionalRemoteCIRunRetainedProofIsIdempotent verifies that an
// unchanged all-hit consumer can be persisted twice without rewriting its
// immutable retained proof.
func TestRecordProvisionalRemoteCIRunRetainedProofIsIdempotent(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	consumer := recordReusedWorkloadPassRun(t, store, "retained-proof-idempotent")

	if err := store.RecordProvisionalRemoteCIRun(consumer); err != nil {
		t.Fatalf("record identical retained-proof consumer a second time: %v", err)
	}
	assertRetainedProofCount(t, store, consumer.JobID, 1)
}

// TestRetainedWorkloadPassProofInsertProjectionMatchesSchema dynamically
// compares the proof-table producer with the writer's reflected projection.
func TestRetainedWorkloadPassProofInsertProjectionMatchesSchema(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	rows, err := database.Query(`PRAGMA table_info(ci_retained_workload_pass_proofs)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var schemaColumns []string
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		schemaColumns = append(schemaColumns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	projection, err := retainedWorkloadPassProofColumns()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(projection, schemaColumns) {
		t.Fatalf("retained proof INSERT projection = %v, physical table columns = %v", projection, schemaColumns)
	}
}

// TestRetainedWorkloadPassProofComparisonRejectsEveryPersistedField locks the
// full collision comparison, including ci_runs' consumer generation join.
func TestRetainedWorkloadPassProofComparisonRejectsEveryPersistedField(t *testing.T) {
	expected := retainedWorkloadPassProof{
		ConsumerJobID: "consumer", ConsumerAcceptedGeneration: "2", WorkloadID: "guard:one", IdentityDigest: "identity",
		OriginJobID: "origin", OriginAcceptedGeneration: "1", OriginSourceTreeSHA: "tree", OriginReceiptSetSHA256: "receipt",
		OriginExecutionJSON: "execution", EvidenceSHA256: "evidence",
	}
	mutations := []struct {
		name   string
		mutate func(*retainedWorkloadPassProof)
	}{
		{name: "consumer job", mutate: func(proof *retainedWorkloadPassProof) { proof.ConsumerJobID = "other" }},
		{name: "consumer generation", mutate: func(proof *retainedWorkloadPassProof) { proof.ConsumerAcceptedGeneration = "3" }},
		{name: "workload", mutate: func(proof *retainedWorkloadPassProof) { proof.WorkloadID = "guard:other" }},
		{name: "identity", mutate: func(proof *retainedWorkloadPassProof) { proof.IdentityDigest = "other" }},
		{name: "origin job", mutate: func(proof *retainedWorkloadPassProof) { proof.OriginJobID = "other" }},
		{name: "origin generation", mutate: func(proof *retainedWorkloadPassProof) { proof.OriginAcceptedGeneration = "3" }},
		{name: "origin tree", mutate: func(proof *retainedWorkloadPassProof) { proof.OriginSourceTreeSHA = "other" }},
		{name: "receipt digest", mutate: func(proof *retainedWorkloadPassProof) { proof.OriginReceiptSetSHA256 = "other" }},
		{name: "execution JSON", mutate: func(proof *retainedWorkloadPassProof) { proof.OriginExecutionJSON = "other" }},
		{name: "evidence digest", mutate: func(proof *retainedWorkloadPassProof) { proof.EvidenceSHA256 = "other" }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			collision := expected
			test.mutate(&collision)
			if collision.matches(expected) {
				t.Fatal("drifted retained proof was accepted as idempotent")
			}
		})
	}
}

// TestRecordProvisionalRemoteCIRunRetainedProofCollisionRollsBackBatch proves
// that a conflict on the second workload result rolls back every provisional
// consumer projection written earlier in the same batch.
func TestRecordProvisionalRemoteCIRunRetainedProofCollisionRollsBackBatch(t *testing.T) {
	store, consumer, identity, freshIdentity := retainedProofCollisionFixture(t)
	corruptRetainedProofForCollision(t, store, consumer, identity)
	consumer.WorkloadResults[0], consumer.WorkloadResults[1] = consumer.WorkloadResults[1], consumer.WorkloadResults[0]
	assertRetainedProofCollision(t, store, consumer)
	assertRetainedProofRollback(t, store, consumer, identity, freshIdentity)
}

func retainedProofCollisionFixture(t *testing.T) (*DurationLedgerStore, RemoteCIRunRecord, WorkloadPassIdentity, WorkloadPassIdentity) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "retained-proof-batch-origin", 1, "retained-proof-batch-origin", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	proof := lookupSingleWorkloadPassEvidence(t, store, identity)
	seedAcceptedGenerationForTest(t, store, 2)
	consumer, freshIdentity := recordMixedRetentionConsumer(t, store, origin, proof)
	return store, consumer, identity, freshIdentity
}

func corruptRetainedProofForCollision(t *testing.T, store *DurationLedgerStore, consumer RemoteCIRunRecord, identity WorkloadPassIdentity) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_retained_workload_pass_proofs SET origin_receipt_set_sha256 = ? WHERE consumer_job_id = ? AND workload_id = ?`, digestForWorkloadPass("retained-proof-collision"), consumer.JobID, identity.WorkloadID); err != nil {
		t.Fatal(err)
	}
}

func assertRetainedProofCollision(t *testing.T, store *DurationLedgerStore, consumer RemoteCIRunRecord) {
	t.Helper()
	if err := store.RecordProvisionalRemoteCIRun(consumer); err == nil || !strings.Contains(err.Error(), "retained workload pass proof") {
		t.Fatalf("collision error = %v, want retained-proof conflict", err)
	}
}

func assertRetainedProofRollback(t *testing.T, store *DurationLedgerStore, consumer RemoteCIRunRecord, identity, freshIdentity WorkloadPassIdentity) {
	t.Helper()
	loaded, err := store.LoadRemoteCIRun(consumer.JobID)
	if err != nil {
		t.Fatal(err)
	}
	results := make(map[GateID]RemoteCIWorkloadResult, len(loaded.WorkloadResults))
	for _, result := range loaded.WorkloadResults {
		results[result.Identity.WorkloadID] = result
	}
	if len(results) != 2 || results[identity.WorkloadID].Disposition != WorkloadDispositionReused || results[freshIdentity.WorkloadID].Disposition != WorkloadDispositionExecuted {
		t.Fatalf("consumer workload results changed after rollback: %#v", loaded.WorkloadResults)
	}
	assertRetainedProofCount(t, store, consumer.JobID, 1)
}

func assertRetainedProofCount(t *testing.T, store *DurationLedgerStore, consumerJobID string, want int) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var got int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_retained_workload_pass_proofs WHERE consumer_job_id = ?`, consumerJobID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("retained proof count for %q = %d, want %d", consumerJobID, got, want)
	}
}
