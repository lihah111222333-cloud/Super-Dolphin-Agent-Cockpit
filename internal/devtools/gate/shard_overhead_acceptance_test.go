package gate

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestFinalizeRemoteCIRunAuthorityWithShardOverheadRollsBackAuthorityAndOverhead 验证 authority 与 overhead 可原子回滚。
// 验证 overhead 写入故障发生在 authority CAS 之后时，单一事务仍完整回滚。
func TestFinalizeRemoteCIRunAuthorityWithShardOverheadRollsBackAuthorityAndOverhead(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordProvisionalWorkloadPassRun(t, store, "shard-overhead-rollback", 1, "shard-overhead-workload")
	evidence := testShardOverheadEvidence(record)
	installFinalizeFailure(t, store, durationLedgerFinalizeStepShardOverhead)
	if err := store.FinalizeRemoteCIRunAuthorityWithShardOverhead(remoteCIRunAuthorityIdentity(record), receipts, nil, true, evidence); err == nil {
		t.Fatal("finalize with injected shard overhead failure unexpectedly succeeded")
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
	assertShardOverheadRowCount(t, store, 0)
}

func TestValidateShardOverheadSourceRunRejectsNormalExecutionMode(t *testing.T) {
	record := RemoteCIRunRecord{Authoritative: true, Status: ResultStatusPassed, CleanupComplete: true, AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1", ExecutionMode: DurationExecutionModeNormal, Platform: "linux/amd64", RunnerIdentityDigest: "sha256:" + strings.Repeat("r", 64), ToolchainDigest: "sha256:" + strings.Repeat("t", 64)}
	context := PlanningContext{Platform: "linux/amd64", Runner: record.RunnerIdentityDigest, Toolchain: record.ToolchainDigest, Calibration: true, CalibrationResourceClassID: "fixed", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8, AcceptedSnapshotID: record.ImageCacheSnapshotID}
	if err := validateShardOverheadSourceRun(record, context); err == nil {
		t.Fatal("normal execution mode was accepted as shard overhead calibration source")
	}
}

func TestFinalizeRemoteCIRunAuthorityWithShardOverheadRejectsNormalModeBeforeCAS(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordProvisionalWorkloadPassRun(t, store, "shard-overhead-normal-mode", 1, "shard-overhead-workload")
	evidence := testShardOverheadEvidence(record)
	evidence.Overhead.Runner = record.RunnerIdentityDigest
	evidence.Overhead.Toolchain = record.ToolchainDigest
	if err := store.FinalizeRemoteCIRunAuthorityWithShardOverhead(remoteCIRunAuthorityIdentity(record), receipts, nil, true, evidence); err == nil {
		t.Fatal("normal execution mode was accepted for shard overhead authority finalization")
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertShardOverheadRowCount(t, store, 0)
}

func TestDeriveShardOverheadEvidenceRejectsUnknownShardResourceJoin(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	resources := RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}
	observations := normalReserveTimingObservations("job-resource-join", "sample-shard", base, resources)
	for index := range observations {
		if observations[index].Scope == cicontract.TimingScopeCompileGroup {
			observations[index].CompileResourceClassID = "medium"
		}
	}
	context := PlanningContext{Platform: "linux/amd64", Runner: "sha256:" + strings.Repeat("r", 64), Toolchain: "sha256:" + strings.Repeat("t", 64), Calibration: true, CalibrationResourceClassID: resources.ClassID, CalibrationResourceCPU: resources.CPU, CalibrationResourceMemoryGiB: resources.MemoryGiB, AcceptedSnapshotID: "snapshot-resource-join"}
	shards := []RemoteCIShardRecord{{ShardIdentity: "different-shard", Resources: resources}}
	if _, err := DeriveShardOrchestrationOverheadEvidence("job-resource-join", 1, context, resources.ClassID, resources.CPU, resources.MemoryGiB, context.AcceptedSnapshotID, observations, shards); err == nil {
		t.Fatal("overhead evidence accepted timing sample without matching ci_shards resource row")
	}
}

func testShardOverheadEvidence(record RemoteCIRunRecord) ShardOrchestrationOverheadEvidence {
	digest := "sha256:" + strings.Repeat("a", 64)
	start := record.StartedAt.UTC()
	sample := ShardOrchestrationOverheadSample{
		AcceptedGeneration:     record.AcceptedGeneration,
		ProvenanceDigest:       digest,
		JobID:                  record.JobID,
		ShardIdentity:          "shard-overhead-1",
		TotalStartedAt:         start,
		TotalCompletedAt:       start.Add(2 * time.Second),
		WorkloadEnvelopeStart:  start.Add(500 * time.Millisecond),
		WorkloadEnvelopeEnd:    start.Add(1500 * time.Millisecond),
		AccountedDurationMS:    1000,
		AccountedIntervalCount: 1,
		OverheadMS:             1000,
	}
	overhead := ShardOrchestrationOverhead{
		SchemaVersion:                ShardOrchestrationOverheadSchemaVersion,
		PolicyVersion:                ShardOverheadPolicyVersion,
		Platform:                     "linux/amd64",
		Runner:                       record.CandidateGateSourceSHA256,
		Toolchain:                    record.CandidateGateToolchainSHA256,
		CalibrationResourceClassID:   "calibration",
		CalibrationResourceCPU:       4,
		CalibrationResourceMemoryGiB: 8,
		P95MS:                        1000,
		SampleCount:                  1,
		ProvenanceDigest:             digest,
		AcceptedGeneration:           record.AcceptedGeneration,
		AcceptedSnapshotID:           record.ImageCacheSnapshotID,
	}
	return ShardOrchestrationOverheadEvidence{Overhead: overhead, Samples: []ShardOrchestrationOverheadSample{sample}}
}

func assertShardOverheadRowCount(t *testing.T, store *DurationLedgerStore, want int) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var aggregate, samples int
	if err := database.QueryRow(`SELECT COUNT(*) FROM duration_shard_overheads`).Scan(&aggregate); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM duration_shard_overhead_samples`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if aggregate != want || samples != want {
		t.Fatalf("overhead rows aggregate=%d samples=%d, want %d each", aggregate, samples, want)
	}
}
