package gate

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorkloadPassSourceReplayCandidatesShareOriginValidation 锁定大批 source
// replay 候选只批量读取一次共同 direct origin，不得逐候选重载 run/timing。
func TestWorkloadPassSourceReplayCandidatesShareOriginValidation(t *testing.T) {
	const candidateCount = 64
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "source-replay-batch-origin", 1, "source-replay-batch-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	origin := lookupSingleWorkloadPassEvidence(t, store, record.WorkloadResults[0].Identity)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	insertBatchScaleEvidence(t, database, origin, candidateCount)
	targets := sourceReplayBatchTargets(t, batchScaleIdentities(t, origin, candidateCount))
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	stats := workloadPassEvidenceLookupStats{}
	candidates, err := loadWorkloadPassSourceReplayCandidatesWithStats(transaction, targets, 1, &stats)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != candidateCount || stats.originRunLoads != 1 || stats.originReceiptSetValidations != 1 {
		t.Fatalf("source replay batch = candidates:%d runs:%d receipts:%d, want %d/1/1", len(candidates), stats.originRunLoads, stats.originReceiptSetValidations, candidateCount)
	}
}

func sourceReplayBatchTargets(t *testing.T, sources []WorkloadPassIdentity) []WorkloadPassIdentity {
	t.Helper()
	targets := append([]WorkloadPassIdentity(nil), sources...)
	for index := range targets {
		targets[index].InputDigest = digestForWorkloadPass("source-replay-target-" + strings.Repeat("x", index+1))
		targets[index].IdentityDigest = workloadPassIdentityDigest(t, targets[index])
	}
	return targets
}

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

// TestRetainedWorkloadPassSourceReplayPreservesOriginIdentity 锁定 retained proof
// 同时保存来源身份和当前 replay 身份，且旧 execution-only replay 行必须 fail-fast。
func TestRetainedWorkloadPassSourceReplayPreservesOriginIdentity(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "retained-source-replay-origin", 1, "retained-source-replay", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	source := lookupSingleWorkloadPassEvidence(t, store, identity)
	target := source.Identity
	target.InputDigest = digestForWorkloadPass("retained-source-replay-target")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	proofDigest, err := WorkloadPassSourceReplaySHA256(target, source)
	if err != nil {
		t.Fatal(err)
	}
	result := RemoteCIWorkloadResult{Identity: target, Disposition: WorkloadDispositionReused, OriginJobID: source.OriginJobID, OriginAcceptedGeneration: source.OriginAcceptedGeneration, EvidenceSHA256: proofDigest}
	proof := retainedSourceReplayProof(t, "retained-source-replay-consumer", source)
	if err := proof.validate(result); err != nil {
		t.Fatalf("validate retained source replay proof: %v", err)
	}
	legacyExecution, err := json.Marshal(source.OriginExecution)
	if err != nil {
		t.Fatal(err)
	}
	proof.OriginExecutionJSON = string(legacyExecution)
	if err := proof.validate(result); err == nil {
		t.Fatal("legacy execution-only retained source replay proof was accepted")
	}
}

func retainedSourceReplayProof(t *testing.T, consumerJobID string, source WorkloadPassEvidence) retainedWorkloadPassProof {
	t.Helper()
	payload, err := encodeRetainedWorkloadPassOriginJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	return retainedWorkloadPassProof{ConsumerJobID: consumerJobID, ConsumerAcceptedGeneration: "1", WorkloadID: string(source.Identity.WorkloadID), IdentityDigest: source.Identity.IdentityDigest, OriginJobID: source.OriginJobID, OriginAcceptedGeneration: "1", OriginSourceTreeSHA: source.OriginSourceTreeSHA, OriginReceiptSetSHA256: source.OriginReceiptSetSHA256, OriginExecutionJSON: string(payload), EvidenceSHA256: source.EvidenceSHA256}
}

// TestRetainedWorkloadPassSourceReplayRoundTripsOriginIdentity 通过真实 SQLite
// provisional 路径验证 replay consumer 能回读并校验完整来源 proof。
func TestRetainedWorkloadPassSourceReplayRoundTripsOriginIdentity(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	directConsumer := recordReusedWorkloadPassRun(t, store, "retained-source-replay-direct-consumer")
	source := lookupSingleWorkloadPassEvidence(t, store, directConsumer.WorkloadResults[0].Identity)
	target := source.Identity
	target.InputDigest = digestForWorkloadPass("retained-source-replay-round-trip")
	target.IdentityDigest = workloadPassIdentityDigest(t, target)
	proofDigest, err := WorkloadPassSourceReplaySHA256(target, source)
	if err != nil {
		t.Fatal(err)
	}
	consumer := directConsumer
	consumer.JobID = "retained-source-replay-round-trip-consumer"
	consumer.AgentTokenDigest = digestForWorkloadPass(consumer.JobID + "-agent")
	consumer.WorkloadResults = []RemoteCIWorkloadResult{{Identity: target, Disposition: WorkloadDispositionReused, OriginJobID: source.OriginJobID, OriginAcceptedGeneration: source.OriginAcceptedGeneration, EvidenceSHA256: proofDigest}}
	if err := store.RecordProvisionalRemoteCIRun(consumer); err != nil {
		t.Fatal(err)
	}
	assertRetainedSourceReplayProofRoundTrip(t, store, consumer, source)
}

func assertRetainedSourceReplayProofRoundTrip(t *testing.T, store *DurationLedgerStore, consumer RemoteCIRunRecord, source WorkloadPassEvidence) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	loaded, err := loadSQLiteRetainedWorkloadPassProofs(transaction, consumer.JobID, consumer.WorkloadResults)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Identity != source.Identity {
		t.Fatalf("retained source replay proof = %#v, want source identity %#v", loaded, source.Identity)
	}
}

// TestWorkloadPassLookupIgnoresIncompleteProvisionalConsumer 区分正常的
// 无回执 provisional 行和权威 consumer 单字段篡改，前者不能污染下一次 prepare。
func TestWorkloadPassLookupIgnoresIncompleteProvisionalConsumer(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	consumer := recordReusedWorkloadPassRun(t, store, "incomplete-provisional-consumer")
	identity := consumer.WorkloadResults[0].Identity
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`DELETE FROM ci_workload_pass_evidence WHERE identity_digest = ?`, identity.IdentityDigest); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	found, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup with incomplete provisional consumer: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("incomplete provisional consumer produced %d PASS hits, want 0", len(found))
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
