package main

import (
	"database/sql"
	"encoding/json"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// TestFinalizeRemoteRunEvidenceAcceptsCalibrationShardOverhead 验证校准 run
// 在 authoritative finalizer 后立即写入 overhead，下一规划读取同一快照。
func TestFinalizeRemoteRunEvidenceAcceptsCalibrationShardOverhead(t *testing.T) {
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	result := remoteRunReceiptTestResult(t, plan, catalog)
	store := remoteRunReceiptTestAuthority(t, catalog, result)
	input := remoteRunReceiptTestInput(plan, store)
	input.Calibration = true
	input.Platform = "linux/amd64"
	input.RunnerIdentityDigest = "sha256:runner"
	input.ToolchainDigest = "sha256:toolchain"
	input.CalibrationResource = calibrationShardOverheadTestClass()
	setCalibrationShardOverheadTestResources(t, store, result.JobID)

	observations := remoteRunReceiptTestObservations(t, plan, catalog, input, result)
	receipts := remoteRunReceiptTestReceipts(t, result, observations)
	evidence, err := prepareRemoteRunShardOverhead(input, result)
	if err != nil {
		t.Fatalf("prepareRemoteRunShardOverhead() error = %v", err)
	}
	if err := finalizeRemoteRunReceiptAuthorityWithShardOverhead(input, result, receipts, nil, nil, evidence); err != nil {
		t.Fatalf("finalizeRemoteRunReceiptAuthorityWithShardOverhead() error = %v", err)
	}
	finalized, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() after finalization: %v", err)
	}
	if !finalized.Authoritative {
		t.Fatal("calibration result was not promoted to authoritative")
	}
	context := gatecontract.PlanningContext{
		Platform: "linux/amd64", Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
		TargetDurationMS: gatecontract.FullCITargetDurationMS, AcceptedSnapshotID: input.ImageCacheSnapshotID,
	}
	snapshot, err := store.LoadPlanning(context)
	if err != nil {
		t.Fatalf("LoadPlanning() after calibration finalizer: %v", err)
	}
	if snapshot.Ledger.ShardOverhead == nil || snapshot.Ledger.ShardOverhead.SampleCount != 1 {
		t.Fatalf("accepted shard overhead = %#v, want one persisted sample", snapshot.Ledger.ShardOverhead)
	}
}

// TestFinalizeRemoteRunEvidenceSkipsCalibrationShardOverheadForAllReuse 验证
// calibration 全复用只提升回执权威，不伪造或派生零分片 overhead/timing。
func TestFinalizeRemoteRunEvidenceSkipsCalibrationShardOverheadForAllReuse(t *testing.T) {
	input, result, store := remoteRunAllReuseCalibrationFixture(t)
	if err := finalizeRemoteRunEvidence(input, &result, nil); err != nil {
		t.Fatalf("finalizeRemoteRunEvidence() all-reuse calibration: %v", err)
	}
	assertRemoteRunAllReuseFinalized(t, store, result)
}

// remoteRunAllReuseCalibrationFixture 先提升真实 origin PASS，再记录全复用候选运行。
func remoteRunAllReuseCalibrationFixture(t *testing.T) (remoteci.RunInput, remoteci.RunResult, *gatecontract.DurationLedgerStore) {
	t.Helper()
	plan, catalog := remoteRunReceiptTestPlanAndCatalog(t)
	origin := remoteRunReceiptTestResult(t, plan, catalog)
	store := remoteRunReceiptTestAuthority(t, catalog, origin)
	remoteRunReceiptTestComplete(t, plan, catalog, store, origin)
	input := remoteRunReceiptTestInput(plan, store)
	input.Calibration = true
	input.Platform = "linux/amd64"
	input.RunnerIdentityDigest = "sha256:runner"
	input.ToolchainDigest = "sha256:toolchain"
	input.CalibrationResource = calibrationShardOverheadTestClass()
	input.CandidateGateSourceSHA256 = ""
	input.CandidateGateToolchainSHA256 = ""

	originRecord, reused := remoteRunPromotedReuseEvidence(t, store, origin.JobID)
	result := origin
	result.JobID = "remote-reuse-job"
	result.Authoritative = false
	result.CandidateGateSourceSHA256 = ""
	result.CandidateGateToolchainSHA256 = ""
	result.FreshWorkloadExecutions = nil
	result.ReusedWorkloads = reused
	recordRemoteRunAllReuseFixture(t, store, originRecord, result)
	return input, result, store
}

// remoteRunPromotedReuseEvidence 从刚升权的 origin 读取精确 workload PASS 证据。
func remoteRunPromotedReuseEvidence(t *testing.T, store *gatecontract.DurationLedgerStore, jobID string) (gatecontract.RemoteCIRunRecord, []gatecontract.WorkloadPassEvidence) {
	t.Helper()
	originRecord, err := store.LoadRemoteCIRun(jobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() after origin promotion: %v", err)
	}
	identities := make([]gatecontract.WorkloadPassIdentity, 0, len(originRecord.WorkloadResults))
	for _, workloadResult := range originRecord.WorkloadResults {
		identities = append(identities, workloadResult.Identity)
	}
	reused, err := store.LookupWorkloadPassEvidence(identities)
	if err != nil {
		t.Fatalf("LookupWorkloadPassEvidence() after origin promotion: %v", err)
	}
	return originRecord, reused
}

// recordRemoteRunAllReuseFixture 持久化零分片、零 fresh timing 的全复用候选。
func recordRemoteRunAllReuseFixture(t *testing.T, store *gatecontract.DurationLedgerStore, originRecord gatecontract.RemoteCIRunRecord, result remoteci.RunResult) {
	t.Helper()
	record := originRecord
	record.JobID = result.JobID
	record.Authoritative = false
	record.CandidateGateSourceSHA256 = ""
	record.CandidateGateToolchainSHA256 = ""
	record.Executions = append([]gatecontract.PlanGateExecution(nil), result.GateExecutions...)
	record.Shards = nil
	record.WorkloadExecutions = nil
	record.TimingObservations = nil
	record.WorkloadResults = remoteRunReceiptTestReusedResults(result)
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("RecordProvisionalRemoteCIRun() all-reuse replay: %v", err)
	}
}

// assertRemoteRunAllReuseFinalized 验证全复用升权且未伪造 fresh 执行证据。
func assertRemoteRunAllReuseFinalized(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult) {
	t.Helper()
	if !result.Authoritative {
		t.Fatal("all-reuse calibration result was not promoted to authoritative")
	}
	finalized, err := store.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatalf("LoadRemoteCIRun() after all-reuse finalization: %v", err)
	}
	if len(finalized.Shards) != 0 || len(finalized.WorkloadExecutions) != 0 || len(finalized.TimingObservations) != 0 {
		t.Fatalf("all-reuse calibration persisted fresh timing: shards=%d workloads=%d timings=%d", len(finalized.Shards), len(finalized.WorkloadExecutions), len(finalized.TimingObservations))
	}
}

func remoteRunReceiptTestReusedResults(result remoteci.RunResult) []gatecontract.RemoteCIWorkloadResult {
	results := make([]gatecontract.RemoteCIWorkloadResult, 0, len(result.ReusedWorkloads))
	for _, evidence := range result.ReusedWorkloads {
		results = append(results, gatecontract.RemoteCIWorkloadResult{
			Identity: evidence.Identity, Disposition: gatecontract.WorkloadDispositionReused,
			OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration,
			EvidenceSHA256: evidence.EvidenceSHA256,
		})
	}
	return results
}

func calibrationShardOverheadTestClass() shardresource.Class {
	return shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8}
}

func calibrationShardOverheadTestResource() gatecontract.RemoteCIShardResources {
	class := calibrationShardOverheadTestClass()
	return gatecontract.RemoteCIShardResources{ClassID: class.ID, CPU: float64(class.VCPU), MemoryGiB: float64(class.MemoryGiB)}
}

func setCalibrationShardOverheadTestResources(t *testing.T, store *gatecontract.DurationLedgerStore, jobID string) {
	t.Helper()
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	resources, err := json.Marshal(calibrationShardOverheadTestResource())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE ci_shards SET resources_json = ? WHERE job_id = ?", resources, jobID); err != nil {
		t.Fatalf("update calibration shard resource fixture: %v", err)
	}
}
