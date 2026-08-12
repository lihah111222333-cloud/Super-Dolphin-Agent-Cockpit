package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRetentionKeepsConsumerOwnedProofUntilConsumerEviction 验证 generation=1
// 的 direct PASS 被 generation=2 mixed consumer 复用后，generation=3/4 写入仍使
// 七个历史根严格只保留 2/3/4；consumer 在保留窗口内通过自己的不可变 direct proof
// 命中，不能把第一代 run/evidence 作为 retention dependency tail 留下。
func TestRetentionKeepsConsumerOwnedProofUntilConsumerEviction(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, staleIdentity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "mixed-origin", 1, "mixed-origin", GateIDWhitespaceCheck)
	originReceipts := completeRetentionReceiptsForWorkloadID(t, origin, GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), originReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	originEvidence := lookupSingleWorkloadPassEvidence(t, store, staleIdentity)
	seedAcceptedGenerationForTest(t, store, 2)
	mixed, freshIdentity := recordMixedRetentionConsumer(t, store, origin, originEvidence)
	finalizeMixedRetentionConsumer(t, store, mixed, freshIdentity)
	assertStaleRetentionResultRemainsConsumable(t, store, mixed.WorkloadResults[0])
	seedAcceptedGenerationForTest(t, store, 4)
	recordMixedRetentionTriggers(t, store, 3, 4)
	assertMixedRetentionConsumerKept(t, store, mixed, freshIdentity, staleIdentity)
	assertRetentionRunDeleted(t, store, origin.JobID, "direct origin")
	assertStaleGenerationAbsentFromSevenRetentionRoots(t, store, 1)
	seedAcceptedGenerationForTest(t, store, 5)
	recordMixedRetentionTriggers(t, store, 5, 5)
	assertRetentionRunDeleted(t, store, mixed.JobID, "consumer")
	assertRetentionRunDeleted(t, store, origin.JobID, "origin")
	assertWorkloadPassLookupMiss(t, store, staleIdentity)
}

// TestRetentionTreatsLegacyExecutionProfileDependencyAsMiss 验证历史 execution profile
// 只会严格退出复用，而不会阻塞后续任务写入与 retention。
func TestRetentionTreatsLegacyExecutionProfileDependencyAsMiss(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "legacy-retention-origin", 1, "mixed-origin", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, identity)
	seedAcceptedGenerationForTest(t, store, 2)
	consumer, freshIdentity := recordMixedRetentionConsumer(t, store, origin, evidence)
	finalizeMixedRetentionConsumer(t, store, consumer, freshIdentity)
	rewriteRetentionOriginAsLegacy(t, store, origin, evidence)
	assertWorkloadPassLookupMiss(t, store, identity)
	_, _, _ = recordWorkloadPassRun(t, store, "legacy-retention-trigger", 2, "legacy-retention-trigger")
	if _, err := store.LoadRemoteCIRun(consumer.JobID); err != nil {
		t.Fatalf("legacy dependency blocked retained consumer: %v", err)
	}
}

// TestRetentionTreatsLegacyIdentityDomainDependencyAsMiss 验证旧无域 proof 只退出复用，
// 不会阻塞后续任务写入或被映射成 v2 PASS。
func TestRetentionTreatsLegacyIdentityDomainDependencyAsMiss(t *testing.T) {
	store, origin, consumer, identity, evidence := retentionIdentityMigrationFixture(t, "legacy-domain")
	legacyDigest := legacyWorkloadPassIdentityDigestForTest(t, identity)
	rewriteRetentionIdentityProof(t, store, origin, consumer, evidence, legacyDigest)
	assertWorkloadPassLookupMiss(t, store, identity)
	_, _, _ = recordWorkloadPassRun(t, store, "legacy-domain-trigger", 2, "legacy-domain-trigger")
	if _, err := store.LoadRemoteCIRun(consumer.JobID); err != nil {
		t.Fatalf("legacy identity dependency blocked retained consumer: %v", err)
	}
}

// TestRetentionRejectsUnknownIdentityDigestDependency 验证非历史算法产生的 digest 漂移仍会阻断 retention。
func TestRetentionRejectsUnknownIdentityDigestDependency(t *testing.T) {
	store, origin, consumer, _, evidence := retentionIdentityMigrationFixture(t, "unknown-domain")
	unknownDigest := digestForWorkloadPass("unknown-pass-identity-domain")
	rewriteRetentionIdentityProof(t, store, origin, consumer, evidence, unknownDigest)
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	err = compactDurationLedgerAuthority(transaction)
	if err == nil || !strings.Contains(err.Error(), "identity digest does not match content") {
		t.Fatalf("retention unknown identity digest error = %v", err)
	}
}

// TestRetentionReusesOriginContextAcrossConsumerProofs 验证同一 origin 的完整 run 与 receipt 只解码一次。
func TestRetentionReusesOriginContextAcrossConsumerProofs(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, "retention-origin-cache", 1, "retention-origin-cache", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, identity)
	result := RemoteCIWorkloadResult{Identity: evidence.Identity, Disposition: WorkloadDispositionReused, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration, EvidenceSHA256: evidence.EvidenceSHA256}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	stats := workloadPassEvidenceLookupStats{}
	cache := retentionOriginCache{stats: &stats}
	for range 2 {
		if err := validateRetentionReusedProof(transaction, result, &cache); err != nil {
			t.Fatal(err)
		}
	}
	if stats.originRunLoads != 1 || stats.originReceiptSetValidations != 1 {
		t.Fatalf("retention origin loads = run:%d receipts:%d, want 1/1", stats.originRunLoads, stats.originReceiptSetValidations)
	}
}

// retentionIdentityMigrationFixture 构造一条被窗口内 mixed consumer 引用的完整 origin proof。
func retentionIdentityMigrationFixture(t *testing.T, label string) (*DurationLedgerStore, RemoteCIRunRecord, RemoteCIRunRecord, WorkloadPassIdentity, WorkloadPassEvidence) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, identity, receipts := recordWorkloadPassRunAtForRetentionID(t, store, label+"-origin", 1, "mixed-origin", GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, identity)
	seedAcceptedGenerationForTest(t, store, 2)
	consumer, freshIdentity := recordMixedRetentionConsumer(t, store, origin, evidence)
	finalizeMixedRetentionConsumer(t, store, consumer, freshIdentity)
	return store, origin, consumer, identity, evidence
}

// rewriteRetentionIdentityProof 将完整 proof 改写为指定 identity digest，保持其余内容摘要自洽。
func rewriteRetentionIdentityProof(t *testing.T, store *DurationLedgerStore, origin, consumer RemoteCIRunRecord, evidence WorkloadPassEvidence, identityDigest string) {
	t.Helper()
	rewritten := evidence
	rewritten.Identity.IdentityDigest = identityDigest
	var err error
	rewritten.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`UPDATE ci_workload_pass_evidence
		SET identity_digest = ?, evidence_sha256 = ?
		WHERE identity_digest = ? AND accepted_generation = ?`,
		identityDigest, rewritten.EvidenceSHA256, evidence.Identity.IdentityDigest, evidence.OriginAcceptedGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(`UPDATE ci_run_workload_results SET identity_digest = ?
		WHERE job_id = ? AND workload_id = ?`, identityDigest, origin.JobID, string(evidence.Identity.WorkloadID)); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(`UPDATE ci_run_workload_results SET identity_digest = ?, evidence_sha256 = ?
		WHERE job_id = ? AND workload_id = ?`, identityDigest, rewritten.EvidenceSHA256, consumer.JobID, string(evidence.Identity.WorkloadID)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

// finalizeMixedRetentionConsumer 写入 mixed consumer 的 fresh execution、时序与样本证据。
func finalizeMixedRetentionConsumer(t *testing.T, store *DurationLedgerStore, mixed RemoteCIRunRecord, freshIdentity WorkloadPassIdentity) {
	t.Helper()
	sample := DurationSample{
		Bucket: DurationBucket{
			WorkloadID: string(freshIdentity.WorkloadID), CommandDigest: strings.Repeat("b", 64), InputDigest: freshIdentity.InputDigest,
			Platform: "linux/amd64", Runner: "eci", Toolchain: RequiredGoToolchain, ExecutionMode: DurationExecutionModeNormal,
			ResourceClassID: "fixed", ResourceCPU: 4, ResourceMemoryGiB: 8,
		},
		Succeeded: true, DurationMS: 7,
	}
	receipts := completeWorkloadPassReceiptsForCatalog(t, mixed, mixedRetentionCatalog(t))
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(mixed), receipts, []DurationSample{sample}, true); err != nil {
		t.Fatal(err)
	}
}

// recordMixedRetentionTriggers 写入连续 generation 的淘汰触发运行。
func recordMixedRetentionTriggers(t *testing.T, store *DurationLedgerStore, first, last uint64) {
	t.Helper()
	for generation := first; generation <= last; generation++ {
		jobID := fmt.Sprintf("mixed-retention-trigger-%d", generation)
		_, _, _ = recordWorkloadPassRun(t, store, jobID, generation, jobID)
	}
}

// assertMixedRetentionConsumerKept 验证 mixed consumer 的 fresh 根完整保留，并由
// consumer-owned proof 安全投影已物理删除 direct origin 的 stale identity。
func assertMixedRetentionConsumerKept(t *testing.T, store *DurationLedgerStore, mixed RemoteCIRunRecord, freshIdentity, staleIdentity WorkloadPassIdentity) {
	t.Helper()
	retainedMixed, err := store.LoadRemoteCIRun(mixed.JobID)
	if err != nil {
		t.Fatalf("load retained mixed consumer: %v", err)
	}
	if len(retainedMixed.WorkloadResults) != 2 || len(retainedMixed.WorkloadExecutions) != 1 || len(retainedMixed.TimingObservations) == 0 {
		t.Fatalf("retained mixed fresh projection = results:%d executions:%d timings:%d, want 2/1/>0", len(retainedMixed.WorkloadResults), len(retainedMixed.WorkloadExecutions), len(retainedMixed.TimingObservations))
	}
	freshEvidence := lookupSingleWorkloadPassEvidence(t, store, freshIdentity)
	if freshEvidence.OriginJobID != mixed.JobID {
		t.Fatalf("retained fresh evidence origin = %q, want %q", freshEvidence.OriginJobID, mixed.JobID)
	}
	staleEvidence := lookupSingleWorkloadPassEvidence(t, store, staleIdentity)
	if staleEvidence.OriginJobID == mixed.JobID {
		t.Fatal("retained stale evidence was rewritten as a consumer origin")
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var proofs int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_retained_workload_pass_proofs WHERE consumer_job_id = ? AND identity_digest = ? AND origin_job_id = ? AND evidence_sha256 = ?`, mixed.JobID, staleIdentity.IdentityDigest, staleEvidence.OriginJobID, staleEvidence.EvidenceSHA256).Scan(&proofs); err != nil {
		t.Fatal(err)
	}
	if proofs != 1 {
		t.Fatalf("retained consumer proof count = %d, want 1", proofs)
	}
	assertRetentionSampleCount(t, store, "2", 1)
}

// assertStaleRetentionResultRemainsConsumable 验证 consumer-owned proof 在该
// consumer 自身离开三代保留窗口前，仍能消费其 direct-origin evidence。
func assertStaleRetentionResultRemainsConsumable(t *testing.T, store *DurationLedgerStore, staleResult RemoteCIWorkloadResult) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if err := verifySQLiteReusableWorkloadEvidence(transaction, staleResult); err != nil {
		t.Fatalf("live retained consumer reused result is not consumable: %v", err)
	}
}

// assertRetentionRunDeleted 验证离开窗口的 consumer 或 origin 已被删除。
func assertRetentionRunDeleted(t *testing.T, store *DurationLedgerStore, jobID, label string) {
	t.Helper()
	if _, err := store.LoadRemoteCIRun(jobID); !errors.Is(err, ErrRemoteCIRunNotFound) {
		t.Fatalf("expired mixed %s load error = %v, want deletion", label, err)
	}
}

// assertStaleGenerationAbsentFromSevenRetentionRoots ensures the published
// seven-root retention union contains no row from an expired generation.
func assertStaleGenerationAbsentFromSevenRetentionRoots(t *testing.T, store *DurationLedgerStore, generation uint64) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	for _, binding := range cicontract.RetentionRootBindings() {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", binding.Table, binding.GenerationColumn)
		if err := database.QueryRow(query, fmt.Sprintf("%d", generation)).Scan(&count); err != nil {
			t.Fatalf("count stale generation in %s: %v", binding.Table, err)
		}
		if count != 0 {
			t.Fatalf("stale generation %d retained in root %s: rows=%d", generation, binding.Table, count)
		}
	}
}

func recordMixedRetentionConsumer(t *testing.T, store *DurationLedgerStore, origin RemoteCIRunRecord, originEvidence WorkloadPassEvidence) (RemoteCIRunRecord, WorkloadPassIdentity) {
	t.Helper()
	catalog := mixedRetentionCatalog(t)
	now := time.Date(2026, time.August, 3, 14, 0, 0, 0, time.UTC)
	treeSHA := strings.Repeat("2", 40)
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: treeSHA, Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: 2, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	freshWorkload := catalog.Workloads[1]
	freshIdentity := WorkloadPassIdentity{WorkloadID: GateID(freshWorkload.ID), ExecutionDigest: WorkloadPassExecutionDigest(freshWorkload), InputDigest: freshWorkload.InputDigest, EnvironmentDigest: digestForWorkloadPass("mixed-consumer-environment")}
	freshIdentity.IdentityDigest = workloadPassIdentityDigest(t, freshIdentity)
	shardIdentity := digestForWorkloadPass("mixed-consumer-shard")
	shard := RemoteCIShardRecord{ShardIdentity: shardIdentity, ContainerGroup: "eci-mixed-consumer", ContainerStatus: "Succeeded", Workloads: []GateID{freshIdentity.WorkloadID}, MaterializationTiming: measuredShardMaterializationTiming(shardIdentity), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}}
	goFlags, err := WorkloadExecutionGoFlags(string(freshIdentity.WorkloadID))
	if err != nil {
		t.Fatal(err)
	}
	execution := PlanGateExecution{ShardIdentity: shardIdentity, GateID: freshIdentity.WorkloadID, Status: ResultStatusPassed, StartedAt: now.Add(3 * time.Millisecond), CompletedAt: now.Add(10 * time.Millisecond), ExecutionProfile: ExecutionProfile{GoFlags: goFlags, CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
	reused := RemoteCIWorkloadResult{Identity: originEvidence.Identity, Disposition: WorkloadDispositionReused, OriginJobID: origin.JobID, OriginAcceptedGeneration: origin.AcceptedGeneration, EvidenceSHA256: originEvidence.EvidenceSHA256}
	record := RemoteCIRunRecord{JobID: "mixed-consumer", AgentTokenDigest: digestForWorkloadPass("mixed-consumer-agent"), Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: 2, ImageCacheSnapshotID: "snapshot-2", PlanDigest: "sha256:mixed-plan", CatalogDigest: catalogDigest, SourceTreeSHA: treeSHA, CandidateGateSourceSHA256: digestForWorkloadPass("mixed-consumer-gate"), CandidateGateToolchainSHA256: digestForWorkloadPass("mixed-consumer-toolchain"), RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, StartedAt: now, CompletedAt: now.Add(time.Second), CleanupComplete: true, Shards: []RemoteCIShardRecord{shard}, WorkloadExecutions: []PlanGateExecution{execution}, WorkloadResults: []RemoteCIWorkloadResult{reused, {Identity: freshIdentity, Disposition: WorkloadDispositionExecuted, OriginJobID: "mixed-consumer", OriginAcceptedGeneration: 2}}, TimingObservations: authoritativeTimingObservationsForTest("mixed-consumer", execution)}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatal(err)
	}
	return record, freshIdentity
}

func mixedRetentionCatalog(t *testing.T) WorkloadCatalog {
	t.Helper()
	return WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{
		{ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: digestForWorkloadPass("input-mixed-origin"), BootstrapEstimateMS: 1, Shardable: true},
		{ID: string(GateIDBackendTestWithGuard), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("b", 64), InputDigest: digestForWorkloadPass("input-mixed-fresh"), BootstrapEstimateMS: 1, Shardable: true},
	}}
}

func recordWorkloadPassRunAtForRetentionID(t *testing.T, store *DurationLedgerStore, jobID string, generation uint64, workload string, workloadID GateID) (RemoteCIRunRecord, WorkloadPassIdentity, []CheckReceiptRecord) {
	t.Helper()
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC).Add(time.Duration(generation) * time.Hour)
	treeSHA := strings.Repeat(fmt.Sprintf("%x", generation), 40)
	catalogWorkload := Workload{ID: string(workloadID), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: digestForWorkloadPass("input-" + workload), BootstrapEstimateMS: 1, Shardable: true}
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{catalogWorkload}}
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: treeSHA, Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: generation, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	identity := WorkloadPassIdentity{WorkloadID: workloadID, ExecutionDigest: WorkloadPassExecutionDigest(catalogWorkload), InputDigest: catalogWorkload.InputDigest, EnvironmentDigest: digestForWorkloadPass("environment-" + workload)}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	shardIdentity := digestForWorkloadPass("shard-" + jobID)
	shard := RemoteCIShardRecord{ShardIdentity: shardIdentity, ContainerGroup: "eci-" + jobID, ContainerStatus: "Succeeded", Workloads: []GateID{workloadID}, MaterializationTiming: measuredShardMaterializationTiming(shardIdentity), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}}
	goFlags, err := WorkloadExecutionGoFlags(string(workloadID))
	if err != nil {
		t.Fatal(err)
	}
	execution := PlanGateExecution{ShardIdentity: shardIdentity, GateID: workloadID, Status: ResultStatusPassed, StartedAt: now.Add(3 * time.Millisecond), CompletedAt: now.Add(10 * time.Millisecond), ExecutionProfile: ExecutionProfile{GoFlags: goFlags, CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
	record := RemoteCIRunRecord{JobID: jobID, AgentTokenDigest: digestForWorkloadPass("agent-" + jobID), Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: generation, ImageCacheSnapshotID: fmt.Sprintf("snapshot-%d", generation), PlanDigest: "sha256:plan", CatalogDigest: catalogDigest, SourceTreeSHA: treeSHA, CandidateGateSourceSHA256: digestForWorkloadPass("gate-source-" + jobID), CandidateGateToolchainSHA256: digestForWorkloadPass("gate-toolchain-" + jobID), RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: false, StartedAt: now, CompletedAt: now.Add(time.Second), CleanupComplete: true, Shards: []RemoteCIShardRecord{shard}, WorkloadExecutions: []PlanGateExecution{execution}, WorkloadResults: []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionExecuted, OriginJobID: jobID, OriginAcceptedGeneration: generation}}, TimingObservations: authoritativeTimingObservationsForTest(jobID, execution)}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatal(err)
	}
	return record, identity, completeRetentionReceiptsForWorkloadID(t, record, workloadID)
}

func completeRetentionReceiptsForWorkloadID(t *testing.T, record RemoteCIRunRecord, workloadID GateID) []CheckReceiptRecord {
	t.Helper()
	check, err := RequiredCheckForWorkloadID(string(workloadID))
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range testCompleteCheckReceipts(record.JobID, record.SourceTreeSHA, record.StartedAt) {
		if receipt.RequiredCheck != check {
			continue
		}
		receipt.RunID, receipt.JobID = record.JobID, record.JobID
		receipt.CandidateTreeSHA, receipt.AgentTokenDigest = record.SourceTreeSHA, record.AgentTokenDigest
		receipt.AcceptedGeneration, receipt.AcceptedSnapshotID = record.AcceptedGeneration, record.ImageCacheSnapshotID
		receipt.ReceiptSHA256, err = CheckReceiptSHA256(receipt)
		if err != nil {
			t.Fatal(err)
		}
		return []CheckReceiptRecord{receipt}
	}
	t.Fatalf("required check %q receipt fixture is missing", check)
	return nil
}

// rewriteRetentionOriginAsLegacy 将 origin 与 evidence 改写为缺少 go_flags 的历史编码。
func rewriteRetentionOriginAsLegacy(t *testing.T, store *DurationLedgerStore, origin RemoteCIRunRecord, evidence WorkloadPassEvidence) {
	t.Helper()
	profileJSON, executionJSON := retentionLegacyExecutionJSON(t, origin.WorkloadExecutions[0])
	legacyEvidenceSHA256, err := legacyWorkloadPassEvidenceSHA256(evidence, string(executionJSON))
	if err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_workload_executions SET execution_profile_json = ? WHERE job_id = ? AND workload_id = ?`, string(profileJSON), origin.JobID, string(evidence.Identity.WorkloadID)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_execution_json = ?, evidence_sha256 = ? WHERE identity_digest = ? AND accepted_generation = ?`, string(executionJSON), legacyEvidenceSHA256, evidence.Identity.IdentityDigest, evidence.OriginAcceptedGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE ci_retained_workload_pass_proofs SET origin_execution_json = ?, evidence_sha256 = ? WHERE identity_digest = ? AND origin_accepted_generation = ?`, string(executionJSON), legacyEvidenceSHA256, evidence.Identity.IdentityDigest, evidence.OriginAcceptedGeneration); err != nil {
		t.Fatal(err)
	}
}

// retentionLegacyExecutionJSON 返回缺少 go_flags 的历史 profile 与 execution JSON。
func retentionLegacyExecutionJSON(t *testing.T, execution PlanGateExecution) ([]byte, []byte) {
	t.Helper()
	profileJSON, err := json.Marshal(execution.ExecutionProfile)
	if err != nil {
		t.Fatal(err)
	}
	var profileFields map[string]json.RawMessage
	if err := json.Unmarshal(profileJSON, &profileFields); err != nil {
		t.Fatal(err)
	}
	delete(profileFields, "go_flags")
	profileJSON, err = json.Marshal(profileFields)
	if err != nil {
		t.Fatal(err)
	}
	executionJSON, err := json.Marshal(execution)
	if err != nil {
		t.Fatal(err)
	}
	var executionFields map[string]json.RawMessage
	if err := json.Unmarshal(executionJSON, &executionFields); err != nil {
		t.Fatal(err)
	}
	executionFields["execution_profile"] = profileJSON
	executionJSON, err = json.Marshal(executionFields)
	if err != nil {
		t.Fatal(err)
	}
	return profileJSON, executionJSON
}

func assertRetentionSampleCount(t *testing.T, store *DurationLedgerStore, generation string, want int) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var got int
	if err := database.QueryRow(`SELECT COUNT(*) FROM duration_samples WHERE accepted_generation = ?`, generation).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("duration sample count generation %s = %d, want %d", generation, got, want)
	}
}
