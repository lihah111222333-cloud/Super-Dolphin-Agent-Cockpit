package gate

import (
	"strings"
	"testing"
	"time"
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
