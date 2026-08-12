package gate

import (
	"strings"
	"testing"
)

func TestWorkloadPassEnvironmentReplayProofFixedVector(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "environment-replay-origin", 1, "environment-replay", GateIDWhitespaceCheck)
	receipts := completeRetentionReceiptsForWorkloadID(t, origin, GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	target := source.Identity
	target.EnvironmentDigest = digestForWorkloadPass("environment-replay-current")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	proof, err := WorkloadPassEnvironmentReplaySHA256(target, source)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:c1039d17d2a2f3c6093cb9168d7ef5db1c85c96dff861814ad078b03ceab3940"
	if proof != want {
		t.Fatalf("environment replay proof = %q, want fixed vector %q", proof, want)
	}
	result := RemoteCIWorkloadResult{
		Identity: target, Disposition: WorkloadDispositionReused,
		OriginJobID: source.OriginJobID, OriginAcceptedGeneration: source.OriginAcceptedGeneration,
		EvidenceSHA256: proof,
	}
	if err := validateReusableWorkloadEvidenceBinding(source, result); err != nil {
		t.Fatalf("validate environment replay proof: %v", err)
	}
}

func TestLookupWorkloadPassEnvironmentReplayHintsAreLazyAndCurrentGeneration(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "environment-replay-lookup-origin", 1, "environment-replay-lookup", GateIDWhitespaceCheck)
	origin.Status = ResultStatusFailed
	origin.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(origin); err != nil {
		t.Fatalf("record cleaned failure: %v", err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	target := source.Identity
	target.EnvironmentDigest = digestForWorkloadPass("environment-replay-lookup-current")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	stats := workloadPassEvidenceLookupStats{}
	hint := requireLazyWorkloadPassEnvironmentReplayHint(t, store, target, source, &stats)
	validatedStats := workloadPassEvidenceLookupStats{}
	requireValidatedWorkloadPassEnvironmentReplayHint(t, store, hint, source, &validatedStats)
}

func requireLazyWorkloadPassEnvironmentReplayHint(t *testing.T, store *DurationLedgerStore, target WorkloadPassIdentity, source WorkloadPassEvidence, stats *workloadPassEvidenceLookupStats) WorkloadPassEnvironmentReplayHint {
	t.Helper()
	hints, err := store.lookupWorkloadPassEnvironmentReplayHintsWithStats([]WorkloadPassIdentity{target}, stats)
	if err != nil {
		t.Fatalf("LookupWorkloadPassEnvironmentReplayHints() error = %v", err)
	}
	got := hints[target.WorkloadID]
	if len(got) != 1 || got[0].UntrustedCandidate().Identity != source.Identity {
		t.Fatalf("environment replay lookup = %#v, want source identity %#v", got, source.Identity)
	}
	if stats.originRunLoads != 0 || stats.originReceiptSetValidations != 0 || stats.loadedProjectionDigests != 0 {
		t.Fatalf("hint lookup loaded origin authority: %#v", stats)
	}
	return got[0]
}

func requireValidatedWorkloadPassEnvironmentReplayHint(t *testing.T, store *DurationLedgerStore, hint WorkloadPassEnvironmentReplayHint, source WorkloadPassEvidence, stats *workloadPassEvidenceLookupStats) {
	t.Helper()
	validated, err := store.validateWorkloadPassEnvironmentReplayHintWithStats(hint, stats)
	if err != nil {
		t.Fatalf("ValidateWorkloadPassEnvironmentReplayHint() error = %v", err)
	}
	if validated.EvidenceSHA256 != source.EvidenceSHA256 {
		t.Fatalf("validated evidence = %#v, want %#v", validated, source)
	}
	if stats.originRunLoads != 1 || stats.originReceiptSetValidations != 1 || stats.loadedProjectionDigests != 1 {
		t.Fatalf("selected hint authority loads = %#v, want one complete origin validation", stats)
	}
}

// TestLookupWorkloadPassEnvironmentReplayHintsPreferExactInput 锁定环境迁移先验证
// 当前 exact input，再尝试语义不同的历史来源，避免 identity-hash 随机顺序。
func TestLookupWorkloadPassEnvironmentReplayHintsPreferExactInput(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	firstRun, firstIdentity, firstReceipts := recordWorkloadPassRunAtForRetentionID(t, store, "environment-order-first", 1, "environment-order-first", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(firstRun), firstReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	secondRun, secondIdentity, secondReceipts := recordWorkloadPassRunAtForRetentionID(t, store, "environment-order-second", 1, "environment-order-second", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(secondRun), secondReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	if firstIdentity.ExecutionDigest != secondIdentity.ExecutionDigest || firstIdentity.InputDigest == secondIdentity.InputDigest {
		t.Fatal("environment replay ordering fixture is invalid")
	}
	target := secondIdentity
	target.EnvironmentDigest = digestForWorkloadPass("environment-order-current")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	hints, err := store.LookupWorkloadPassEnvironmentReplayHints([]WorkloadPassIdentity{target})
	if err != nil {
		t.Fatal(err)
	}
	got := hints[target.WorkloadID]
	if len(got) != 2 || got[0].UntrustedCandidate().Identity.InputDigest != target.InputDigest {
		t.Fatalf("environment replay hint order = %#v, want exact input first", got)
	}
}

func TestValidateWorkloadPassEnvironmentReplayHintRejectsCanonicalDrift(t *testing.T) {
	store, hint := workloadPassEnvironmentReplayHintFixture(t, "environment-replay-hint-drift")
	tampered := hint
	tampered.untrustedCandidate.OriginSourceTreeSHA = strings.Repeat("f", 40)
	var err error
	tampered.untrustedCandidate.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(tampered.untrustedCandidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateWorkloadPassEnvironmentReplayHint(tampered); err == nil || !strings.Contains(err.Error(), "no longer matches SQLite authority") {
		t.Fatalf("self-consistent tampered hint error = %v, want canonical authority mismatch", err)
	}
}

func TestValidateWorkloadPassEnvironmentReplayHintRejectsOriginRowDrift(t *testing.T) {
	store, hint := workloadPassEnvironmentReplayHintFixture(t, "environment-replay-row-drift")
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_runs SET error_text = error_text || '; drift' WHERE job_id = ?`, hint.untrustedCandidate.OriginJobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateWorkloadPassEnvironmentReplayHint(hint); err == nil || !strings.Contains(err.Error(), "receipt set is missing or tampered") {
		t.Fatalf("tampered origin row error = %v, want authority rejection", err)
	}
}

func TestLookupWorkloadPassEnvironmentReplayHintsRejectsUnknownExecutionJSON(t *testing.T) {
	store, hint := workloadPassEnvironmentReplayHintFixture(t, "environment-replay-unknown-json")
	source := hint.UntrustedCandidate()
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_execution_json = substr(origin_execution_json, 1, length(origin_execution_json) - 1) || ',"unexpected":true}' WHERE identity_digest = ? AND accepted_generation = ?`, source.Identity.IdentityDigest, source.OriginAcceptedGeneration); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	target := source.Identity
	target.EnvironmentDigest = digestForWorkloadPass("environment-replay-unknown-json-target")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	if _, err := store.LookupWorkloadPassEnvironmentReplayHints([]WorkloadPassIdentity{target}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown execution JSON error = %v, want strict decode rejection", err)
	}
}

func TestValidateWorkloadPassEnvironmentReplayHintRejectsExecutionJSONDrift(t *testing.T) {
	store, hint := workloadPassEnvironmentReplayHintFixture(t, "environment-replay-stage2-unknown-json")
	source := hint.UntrustedCandidate()
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_execution_json = substr(origin_execution_json, 1, length(origin_execution_json) - 1) || ',"unexpected":true}' WHERE identity_digest = ? AND accepted_generation = ?`, source.Identity.IdentityDigest, source.OriginAcceptedGeneration); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ValidateWorkloadPassEnvironmentReplayHint(hint); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("stage2 execution JSON drift error = %v, want strict decode rejection", err)
	}
}

func TestLoadSQLiteReusableWorkloadEvidenceRejectsUnknownExecutionJSON(t *testing.T) {
	store, hint := workloadPassEnvironmentReplayHintFixture(t, "reusable-evidence-unknown-json")
	source := hint.UntrustedCandidate()
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_execution_json = substr(origin_execution_json, 1, length(origin_execution_json) - 1) || ',"unexpected":true}' WHERE identity_digest = ? AND accepted_generation = ?`, source.Identity.IdentityDigest, source.OriginAcceptedGeneration); err != nil {
		database.Close()
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	result := RemoteCIWorkloadResult{Identity: source.Identity, OriginJobID: source.OriginJobID, OriginAcceptedGeneration: source.OriginAcceptedGeneration}
	_, loadErr := loadSQLiteReusableWorkloadEvidence(tx, result)
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if loadErr == nil || !strings.Contains(loadErr.Error(), "unknown field") {
		t.Fatalf("reusable evidence unknown execution JSON error = %v, want strict decode rejection", loadErr)
	}
}

func TestLookupWorkloadPassEnvironmentReplayHintsTreatsLegacyProfileAsMiss(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "environment-replay-legacy-profile", 1, "environment-replay-legacy-profile", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	rewriteRetentionOriginAsLegacy(t, store, origin, source)
	target := source.Identity
	target.EnvironmentDigest = digestForWorkloadPass("environment-replay-legacy-profile-target")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	hints, err := store.LookupWorkloadPassEnvironmentReplayHints([]WorkloadPassIdentity{target})
	if err != nil {
		t.Fatalf("legacy environment replay hint lookup: %v", err)
	}
	if len(hints[target.WorkloadID]) != 0 {
		t.Fatalf("legacy environment replay hints = %#v, want natural miss", hints)
	}
}

func TestValidateWorkloadPassEnvironmentReplayHintsBatchesSelectedOrigins(t *testing.T) {
	store, targets := workloadPassEnvironmentReplayBatchTargets(t)
	allHints := requireWorkloadPassEnvironmentReplayBatchHints(t, store, targets)
	selected := allHints[:15]
	validated := requireBatchedWorkloadPassEnvironmentReplayAuthority(t, store, selected)
	if validated[len(validated)-1].Identity.WorkloadID == allHints[len(allHints)-1].UntrustedCandidate().Identity.WorkloadID {
		t.Fatal("unselected hint became validated authority")
	}
	requireTamperedWorkloadPassEnvironmentReplayBatchRejected(t, store, selected)
}

func workloadPassEnvironmentReplayBatchTargets(t *testing.T) (*DurationLedgerStore, []WorkloadPassIdentity) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	record := recordBulkFailedRun(t, store, "environment-replay-batch-origin", strings.Repeat("a", 40), 67)
	sources := bulkRunIdentities(record)[:16]
	targets := make([]WorkloadPassIdentity, len(sources))
	for index, source := range sources {
		targets[index] = source
		targets[index].EnvironmentDigest = bulkDigest(uint64(30000 + index))
		targets[index].IdentityDigest = workloadPassIdentityDigest(t, targets[index])
	}
	return store, targets
}

func requireWorkloadPassEnvironmentReplayBatchHints(t *testing.T, store *DurationLedgerStore, targets []WorkloadPassIdentity) []WorkloadPassEnvironmentReplayHint {
	t.Helper()
	hintStats := workloadPassEvidenceLookupStats{}
	byWorkload, err := store.lookupWorkloadPassEnvironmentReplayHintsWithStats(targets, &hintStats)
	if err != nil {
		t.Fatal(err)
	}
	allHints := make([]WorkloadPassEnvironmentReplayHint, 0, len(targets))
	for _, target := range targets {
		if len(byWorkload[target.WorkloadID]) != 1 {
			t.Fatalf("environment replay hints for %q = %d, want 1", target.WorkloadID, len(byWorkload[target.WorkloadID]))
		}
		allHints = append(allHints, byWorkload[target.WorkloadID][0])
	}
	if hintStats.originRunLoads != 0 || hintStats.originReceiptSetValidations != 0 {
		t.Fatalf("batch hint scan loaded authority: %#v", hintStats)
	}
	return allHints
}

func requireBatchedWorkloadPassEnvironmentReplayAuthority(t *testing.T, store *DurationLedgerStore, selected []WorkloadPassEnvironmentReplayHint) []WorkloadPassEvidence {
	t.Helper()
	stats := workloadPassEvidenceLookupStats{}
	validated, err := store.validateWorkloadPassEnvironmentReplayHintsWithStats(selected, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if len(validated) != len(selected) {
		t.Fatalf("validated selected hints = %d, want %d", len(validated), len(selected))
	}
	if stats.authorityTransactions != 1 || stats.identityBatchQueries != 1 || stats.originRunLoads != 1 || stats.originReceiptSetValidations != 1 || stats.loadedProjectionDigests != 1 {
		t.Fatalf("batch authority counts = %#v, want one tx/query/origin validation", stats)
	}
	return validated
}

func requireTamperedWorkloadPassEnvironmentReplayBatchRejected(t *testing.T, store *DurationLedgerStore, selected []WorkloadPassEnvironmentReplayHint) {
	t.Helper()
	tampered := append([]WorkloadPassEnvironmentReplayHint(nil), selected...)
	tampered[7].untrustedCandidate.OriginSourceTreeSHA = strings.Repeat("f", 40)
	var err error
	tampered[7].untrustedCandidate.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(tampered[7].untrustedCandidate)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := store.ValidateWorkloadPassEnvironmentReplayHints(tampered)
	if err == nil || !strings.Contains(err.Error(), "no longer matches SQLite authority") {
		t.Fatalf("tampered batch error = %v, want canonical authority mismatch", err)
	}
	if partial != nil {
		t.Fatalf("tampered batch returned partial authority: %#v", partial)
	}
}

func workloadPassEnvironmentReplayHintFixture(t *testing.T, jobID string) (*DurationLedgerStore, WorkloadPassEnvironmentReplayHint) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, _ := recordWorkloadPassRunAtForRetentionID(t, store, jobID, 1, jobID, GateIDWhitespaceCheck)
	origin.Status = ResultStatusFailed
	origin.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(origin); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	target := source.Identity
	target.EnvironmentDigest = digestForWorkloadPass(jobID + "-target")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	hints, err := store.LookupWorkloadPassEnvironmentReplayHints([]WorkloadPassIdentity{target})
	if err != nil {
		t.Fatal(err)
	}
	if len(hints[target.WorkloadID]) != 1 {
		t.Fatalf("environment replay hints = %#v, want one", hints)
	}
	return store, hints[target.WorkloadID][0]
}

func TestWorkloadPassEnvironmentReplayProofRejectsUnsafePairs(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "environment-replay-reject-origin", 1, "environment-replay-reject", GateIDWhitespaceCheck)
	receipts := completeRetentionReceiptsForWorkloadID(t, origin, GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	tests := []struct {
		name   string
		mutate func(*WorkloadPassIdentity)
	}{
		{name: "same environment", mutate: func(target *WorkloadPassIdentity) {}},
		{name: "workload drift", mutate: func(target *WorkloadPassIdentity) { target.WorkloadID = GateIDBackendTestWithGuard }},
		{name: "execution drift", mutate: func(target *WorkloadPassIdentity) {
			target.ExecutionDigest = digestForWorkloadPass("environment-replay-execution")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := source.Identity
			if test.name != "same environment" {
				target.EnvironmentDigest = digestForWorkloadPass("environment-replay-current")
			}
			test.mutate(&target)
			target.IdentityDigest = workloadPassIdentityDigest(t, target)
			if _, err := WorkloadPassEnvironmentReplaySHA256(target, source); err == nil {
				t.Fatal("environment replay accepted unsafe identity pair")
			}
		})
	}
}
