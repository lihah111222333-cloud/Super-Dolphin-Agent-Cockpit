package gate

import (
	"strings"
	"testing"
)

// TestWorkloadPassSourceReplayProofBindsCurrentIdentity 锁定来源树只属于 proof，目标身份仍是当前四段 PassKey。
func TestWorkloadPassSourceReplayProofBindsCurrentIdentity(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "source-replay-origin", 1, "source-replay", GateIDWhitespaceCheck)
	receipts := completeRetentionReceiptsForWorkloadID(t, origin, GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	target := source.Identity
	target.InputDigest = digestForWorkloadPass("source-replay-current-input")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)

	proof, err := WorkloadPassSourceReplaySHA256(target, source)
	if err != nil {
		t.Fatal(err)
	}
	result := RemoteCIWorkloadResult{
		Identity: target, Disposition: WorkloadDispositionReused,
		OriginJobID: source.OriginJobID, OriginAcceptedGeneration: source.OriginAcceptedGeneration,
		EvidenceSHA256: proof,
	}
	if err := validateReusableWorkloadEvidenceBinding(source, result); err != nil {
		t.Fatalf("validate current identity source replay: %v", err)
	}
	if result.Identity == source.Identity || result.Identity.InputDigest == source.Identity.InputDigest {
		t.Fatal("source replay consumer retained the historical input identity")
	}
}

// TestWorkloadPassSourceReplayProofRejectsIdentityDrift 拒绝把来源 PASS 投影到不同 workload、执行或环境。
func TestWorkloadPassSourceReplayProofRejectsIdentityDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "source-replay-drift-origin", 1, "source-replay", GateIDWhitespaceCheck)
	receipts := completeRetentionReceiptsForWorkloadID(t, origin, GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	base := source.Identity
	base.InputDigest = digestForWorkloadPass("source-replay-drift-input")
	base.IdentityDigest = workloadPassIdentityDigest(t, base)

	for _, test := range []struct {
		name   string
		mutate func(*WorkloadPassIdentity)
	}{
		{name: "workload", mutate: func(value *WorkloadPassIdentity) { value.WorkloadID = GateIDBackendTestWithGuard }},
		{name: "execution", mutate: func(value *WorkloadPassIdentity) {
			value.ExecutionDigest = digestForWorkloadPass("source-replay-drift-execution")
		}},
		{name: "environment", mutate: func(value *WorkloadPassIdentity) {
			value.EnvironmentDigest = digestForWorkloadPass("source-replay-drift-environment")
		}},
		{name: "same input", mutate: func(value *WorkloadPassIdentity) { value.InputDigest = source.Identity.InputDigest }},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := base
			test.mutate(&target)
			target.IdentityDigest = workloadPassIdentityDigest(t, target)
			if _, err := WorkloadPassSourceReplaySHA256(target, source); err == nil {
				t.Fatal("source replay accepted identity drift")
			}
		})
	}
	proof, err := WorkloadPassSourceReplaySHA256(base, source)
	if err != nil {
		t.Fatal(err)
	}
	result := RemoteCIWorkloadResult{Identity: base, Disposition: WorkloadDispositionReused, OriginJobID: source.OriginJobID, OriginAcceptedGeneration: source.OriginAcceptedGeneration, EvidenceSHA256: proof}
	result.EvidenceSHA256 = digestForWorkloadPass("source-replay-tampered-proof")
	if err := validateReusableWorkloadEvidenceBinding(source, result); err == nil {
		t.Fatal("source replay accepted a tampered proof digest")
	}
}

func TestLookupWorkloadPassSourceReplayCandidatesRejectsUnknownExecutionJSON(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "source-replay-unknown-json", 1, "source-replay-unknown-json", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_execution_json = substr(origin_execution_json, 1, length(origin_execution_json) - 1) || ',"unexpected":true}' WHERE identity_digest = ? AND accepted_generation = ?`, source.Identity.IdentityDigest, source.OriginAcceptedGeneration); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	target := source.Identity
	target.InputDigest = digestForWorkloadPass("source-replay-unknown-json-target")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	if _, err := store.LookupWorkloadPassSourceReplayCandidates([]WorkloadPassIdentity{target}); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("source replay unknown execution JSON error = %v, want strict decode rejection", err)
	}
}
