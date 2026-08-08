package gate

import "testing"

// TestWorkloadPassEvidencePromotesPassingWorkloadFromFailedRun verifies that a
// cleaned failed run remains non-authoritative while its complete passing
// workload execution is independently reusable.
func TestWorkloadPassEvidencePromotesPassingWorkloadFromFailedRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-partial-pass", 1, "failed-partial-pass-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned failed run: %v", err)
	}
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("load failed run: %v", err)
	}
	if loaded.Authoritative || loaded.Status != ResultStatusFailed || !loaded.CleanupComplete {
		t.Fatalf("failed run authority projection = %#v", loaded)
	}
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup failed-run workload evidence: %v", err)
	}
	if len(evidence) != 1 || evidence[0].OriginJobID != record.JobID || evidence[0].OriginExecution.Status != ResultStatusPassed {
		t.Fatalf("failed-run workload evidence = %#v", evidence)
	}
}

func TestWorkloadPassEvidenceDoesNotPromoteWhenCleanupIsIncomplete(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, _ := recordWorkloadPassRun(t, store, "failed-dirty", 1, "failed-dirty-workload")
	record.Status = ResultStatusFailed
	record.Authoritative = false
	record.CleanupComplete = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record dirty failed run: %v", err)
	}
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup dirty failed run: %v", err)
	}
	if len(evidence) != 0 {
		t.Fatalf("dirty failed-run evidence = %#v, want miss", evidence)
	}
}
