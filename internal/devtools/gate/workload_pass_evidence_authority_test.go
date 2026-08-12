package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// TestWorkloadPassEvidenceAcceptsEquivalentSuccessfulOrigin 验证同代重复成功执行
// 保留首个已验证 canonical proof，同时允许后续独立 run 完成权威提升。
func TestWorkloadPassEvidenceAcceptsEquivalentSuccessfulOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first, identity, firstReceipts := recordWorkloadPassRunAt(t, store, "conflict-origin-first", 1, "conflict-origin", time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC))
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(first), firstReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	second, secondIdentity, secondReceipts := recordWorkloadPassRunAt(t, store, "conflict-origin-second", 1, "conflict-origin", time.Date(2026, time.August, 3, 12, 1, 0, 0, time.UTC))
	if secondIdentity != identity {
		t.Fatalf("same workload produced different identity: first=%#v second=%#v", identity, secondIdentity)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(second), secondReceipts, nil, true); err != nil {
		t.Fatalf("equivalent origin finalization error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, second.JobID, true)
	if got := lookupSingleWorkloadPassEvidence(t, store, identity); got.OriginJobID != first.JobID {
		t.Fatalf("equivalent origin replaced canonical proof with %q, want %q", got.OriginJobID, first.JobID)
	}
}

// TestWorkloadPassEvidenceRejectsCorruptCanonicalOrigin 验证重复成功不能把损坏
// canonical 行当成幂等命中，也不能借后续 run 覆盖它。
func TestWorkloadPassEvidenceRejectsCorruptCanonicalOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first, identity, firstReceipts := recordWorkloadPassRun(t, store, "corrupt-canonical-first", 1, "corrupt-canonical")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(first), firstReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	second, secondIdentity, secondReceipts := recordWorkloadPassRun(t, store, "corrupt-canonical-second", 1, "corrupt-canonical")
	if secondIdentity != identity {
		t.Fatalf("same workload produced different identity: first=%#v second=%#v", identity, secondIdentity)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET evidence_sha256 = ? WHERE identity_digest = ? AND accepted_generation = '1'`, "sha256:"+strings.Repeat("f", 64), identity.IdentityDigest); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(second), secondReceipts, nil, true); err == nil || !strings.Contains(err.Error(), "validate canonical current workload pass evidence") {
		t.Fatalf("corrupt canonical origin finalization error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, second.JobID, false)
}

// TestWorkloadPassEvidenceAtomicReexecutionRetainsCanonicalOrigin 验证包原子降级后的
// fresh execution 仍消费准备阶段验证过的旧 proof，不把同 identity 重铸为冲突来源。
func TestWorkloadPassEvidenceAtomicReexecutionRetainsCanonicalOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first, identity, firstReceipts := recordWorkloadPassRunAt(t, store, "atomic-origin-first", 1, "atomic-origin", time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC))
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(first), firstReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	canonical := lookupSingleWorkloadPassEvidence(t, store, identity)
	second, secondIdentity, secondReceipts := recordWorkloadPassRunAt(t, store, "atomic-origin-second", 1, "atomic-origin", time.Date(2026, time.August, 3, 12, 1, 0, 0, time.UTC))
	if secondIdentity != identity {
		t.Fatalf("atomic reexecution identity = %#v, want %#v", secondIdentity, identity)
	}
	second.WorkloadResults[0] = RemoteCIWorkloadResult{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: canonical.OriginJobID, OriginAcceptedGeneration: canonical.OriginAcceptedGeneration, EvidenceSHA256: canonical.EvidenceSHA256}
	if err := store.RecordProvisionalRemoteCIRun(second); err != nil {
		t.Fatalf("record atomic reexecution: %v", err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(second), secondReceipts, nil, true); err != nil {
		t.Fatalf("finalize atomic reexecution: %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, second.JobID, true)
	if got := lookupSingleWorkloadPassEvidence(t, store, identity); got.OriginJobID != first.JobID {
		t.Fatalf("atomic reexecution proof origin = %q, want canonical %q", got.OriginJobID, first.JobID)
	}
}

// TestWorkloadPassEvidenceAcceptsIdempotentFullProof 验证 plain INSERT 的唯一键
// 冲突只有在全部规范 proof 列逐字节相同时才收敛为幂等成功。
func TestWorkloadPassEvidenceAcceptsIdempotentFullProof(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "idempotent-proof", 1, "idempotent-proof-workload")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := loadRemoteCIRunRow(tx, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadRemoteCIRunDetails(tx, record.JobID, &stored); err != nil {
		t.Fatal(err)
	}
	receiptDigest, err := workloadReceiptSetSHA256(tx, stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.WorkloadExecutions) != 1 {
		t.Fatalf("stored workload executions = %d, want 1", len(stored.WorkloadExecutions))
	}
	if err := insertWorkloadPassEvidence(tx, stored, receiptDigest, identity, stored.WorkloadExecutions[0]); err != nil {
		t.Fatalf("idempotent full proof insert: %v", err)
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ? AND accepted_generation = '1'`, identity.IdentityDigest).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idempotent full proof row count = %d, want 1", count)
	}
}

// TestWorkloadPassEvidenceConstraintClassifierFailsClosed 验证非 SQLite constraint 错误不会进入幂等重载路径。
func TestWorkloadPassEvidenceConstraintClassifierFailsClosed(t *testing.T) {
	if isSQLiteConstraintError(errors.New("busy or I/O failure")) {
		t.Fatal("generic SQLite write failure was misclassified as constraint collision")
	}
}

// TestWorkloadPassEvidenceConcurrentEquivalentOrigins 验证同一 identity 的并发成功
// 来源都能提交独立 run，而 canonical proof 仍只有一个且不可覆盖。
func TestWorkloadPassEvidenceConcurrentEquivalentOrigins(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first, identity, firstReceipts := recordWorkloadPassRunAt(t, store, "concurrent-conflict-first", 1, "concurrent-conflict", time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC))
	second, secondIdentity, secondReceipts := recordWorkloadPassRunAt(t, store, "concurrent-conflict-second", 1, "concurrent-conflict", time.Date(2026, time.August, 3, 12, 1, 0, 0, time.UTC))
	if secondIdentity != identity {
		t.Fatalf("same workload produced different identity: first=%#v second=%#v", identity, secondIdentity)
	}
	runs := []conflictingOriginRun{{first, firstReceipts}, {second, secondReceipts}}
	errs := finalizeConflictingOriginsConcurrently(store, runs)
	assertEquivalentOriginOutcomes(t, store, runs, errs)
	canonical := lookupSingleWorkloadPassEvidence(t, store, identity)
	if canonical.OriginJobID != first.JobID && canonical.OriginJobID != second.JobID {
		t.Fatalf("concurrent canonical origin = %q, want one contender", canonical.OriginJobID)
	}
}

type conflictingOriginRun struct {
	record   RemoteCIRunRecord
	receipts []CheckReceiptRecord
}

func finalizeConflictingOriginsConcurrently(store *DurationLedgerStore, runs []conflictingOriginRun) []error {
	start := make(chan struct{})
	errs := make([]error, len(runs))
	var group errgroup.Group
	for index, run := range runs {
		group.Go(func() error {
			<-start
			errs[index] = store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(run.record), run.receipts, nil, true)
			return nil
		})
	}
	close(start)
	_ = group.Wait()
	return errs
}

func assertEquivalentOriginOutcomes(t *testing.T, store *DurationLedgerStore, runs []conflictingOriginRun, errs []error) {
	t.Helper()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent equivalent origin[%d] error = %v", index, err)
		}
		assertRemoteCIRunAuthoritative(t, store, runs[index].record.JobID, true)
	}
}

// TestFinalizeRemoteCIRunAuthorityRejectsDriftedReusedOrigin 验证 CAS 重验 origin execution JSON，不信任旧 reused 行。
func TestFinalizeRemoteCIRunAuthorityRejectsDriftedReusedOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, receipts := recordWorkloadPassRun(t, store, "drifted-origin", 1, "drifted-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	original := lookupSingleWorkloadPassEvidence(t, store, identity)
	reused := fresh
	reused.JobID = "drifted-origin-consumer"
	reused.StartedAt = fresh.StartedAt.Add(time.Hour)
	reused.CompletedAt = reused.StartedAt.Add(time.Second)
	reused.Shards, reused.WorkloadExecutions, reused.TimingObservations = nil, nil, nil
	reused.CandidateGateSourceSHA256, reused.CandidateGateToolchainSHA256 = "", ""
	reused.WorkloadResults = []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: fresh.JobID, OriginAcceptedGeneration: fresh.AcceptedGeneration, EvidenceSHA256: original.EvidenceSHA256}}
	if err := store.RecordProvisionalRemoteCIRun(reused); err != nil {
		t.Fatal(err)
	}
	driftedProfile := fresh.WorkloadExecutions[0].ExecutionProfile
	driftedProfile.CacheMissCount++
	driftedProfileJSON, err := json.Marshal(driftedProfile)
	if err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_executions SET execution_profile_json = ? WHERE job_id = ?`, string(driftedProfileJSON), fresh.JobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(reused), completeWorkloadPassReceipts(t, reused), nil, false); err == nil || !strings.Contains(err.Error(), "origin proof") {
		t.Fatalf("drifted reused origin finalization error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, reused.JobID, false)
}

// TestRetentionDeletesConsumersWithStaleOrigins 验证 origin 代际过期后 compaction 原子删除 all-hit consumer。
func TestRetentionDeletesConsumersWithStaleOrigins(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, receipts := recordWorkloadPassRun(t, store, "retention-origin", 1, "retention-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	original := lookupSingleWorkloadPassEvidence(t, store, identity)
	consumer := fresh
	consumer.JobID = "retention-consumer"
	consumer.StartedAt = fresh.StartedAt.Add(time.Hour)
	consumer.CompletedAt = consumer.StartedAt.Add(time.Second)
	consumer.Shards, consumer.WorkloadExecutions, consumer.TimingObservations = nil, nil, nil
	consumer.CandidateGateSourceSHA256, consumer.CandidateGateToolchainSHA256 = "", ""
	consumer.WorkloadResults = []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: fresh.JobID, OriginAcceptedGeneration: fresh.AcceptedGeneration, EvidenceSHA256: original.EvidenceSHA256}}
	if err := store.RecordProvisionalRemoteCIRun(consumer); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 4)
	for generation := uint64(2); generation <= 4; generation++ {
		_, _, _ = recordWorkloadPassRun(t, store, fmt.Sprintf("retention-trigger-%d", generation), generation, fmt.Sprintf("retention-trigger-%d", generation))
	}
	if _, err := store.LoadRemoteCIRun(consumer.JobID); !errors.Is(err, ErrRemoteCIRunNotFound) {
		t.Fatalf("stale consumer load error = %v, want deletion", err)
	}
}

// TestRetentionRejectsMissingRetainedConsumerProof 验证保留窗口内缺失 proof 会回滚 compaction，不能静默删除 consumer。
func TestRetentionRejectsMissingRetainedConsumerProof(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, receipts := recordWorkloadPassRun(t, store, "retained-proof-origin", 1, "retained-proof")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	original := lookupSingleWorkloadPassEvidence(t, store, identity)
	consumer := fresh
	consumer.JobID = "retained-proof-consumer"
	consumer.StartedAt = fresh.StartedAt.Add(time.Hour)
	consumer.CompletedAt = consumer.StartedAt.Add(time.Second)
	consumer.Shards, consumer.WorkloadExecutions, consumer.TimingObservations = nil, nil, nil
	consumer.CandidateGateSourceSHA256, consumer.CandidateGateToolchainSHA256 = "", ""
	consumer.WorkloadResults = []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: fresh.JobID, OriginAcceptedGeneration: fresh.AcceptedGeneration, EvidenceSHA256: original.EvidenceSHA256}}
	if err := store.RecordProvisionalRemoteCIRun(consumer); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM ci_workload_pass_evidence WHERE identity_digest = ? AND accepted_generation = ?`, identity.IdentityDigest, fresh.AcceptedGeneration); err != nil {
		t.Fatal(err)
	}
	err := withSQLiteWriteTransaction(database, "retained proof compaction", compactDurationLedgerAuthority)
	if err == nil || !strings.Contains(err.Error(), "has no promoted evidence") {
		t.Fatalf("retained missing proof compaction error = %v", err)
	}
	var consumerCount int
	if err := database.QueryRow(`SELECT count(*) FROM ci_runs WHERE job_id = ?`, consumer.JobID).Scan(&consumerCount); err != nil {
		t.Fatal(err)
	}
	if consumerCount != 1 {
		t.Fatalf("retained consumer count = %d, want 1 after failed compaction", consumerCount)
	}
}

// TestLookupWorkloadPassEvidenceRejectsOriginTreeDrift 验证候选查询不能把已存在的漂移 proof 隐藏成普通 MISS。
func TestLookupWorkloadPassEvidenceRejectsOriginTreeDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, receipts := recordWorkloadPassRun(t, store, "lookup-tree-drift-origin", 1, "lookup-tree-drift")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_runs SET source_tree_sha = ? WHERE job_id = ?`, strings.Repeat("f", 40), fresh.JobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil {
		t.Fatalf("drifted origin tree lookup error = %v", err)
	}
}

// TestLookupWorkloadPassEvidenceRejectsReuseChainOrigin verifies a promoted
// evidence row can only point at a direct executed origin result, never at a
// reused projection masquerading as the source execution.
func TestLookupWorkloadPassEvidenceRejectsReuseChainOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, receipts := recordWorkloadPassRun(t, store, "lookup-reuse-chain-origin", 1, "lookup-reuse-chain-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_run_workload_results SET disposition = 'reused' WHERE job_id = ? AND workload_id = ?`, fresh.JobID, string(identity.WorkloadID)); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), "origin") {
		t.Fatalf("reuse-chain origin lookup error = %v, want origin proof failure", err)
	}
}

// TestRecordProvisionalRemoteCIRunRejectsCandidateGateIdentityForAllHit 验证 all-hit 仅允许省略候选 Gate 编译身份。
func TestRecordProvisionalRemoteCIRunRejectsCandidateGateIdentityForAllHit(t *testing.T) {
	for _, test := range []struct {
		name      string
		source    string
		toolchain string
	}{
		{name: "source only", source: digestForWorkloadPass("all-hit-source")},
		{name: "toolchain only", toolchain: digestForWorkloadPass("all-hit-toolchain")},
		{name: "both", source: digestForWorkloadPass("all-hit-source"), toolchain: digestForWorkloadPass("all-hit-toolchain")},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newWorkloadPassEvidenceStore(t, 1)
			reused := recordReusedWorkloadPassRun(t, store, "record-all-hit-"+strings.ReplaceAll(test.name, " ", "-"))
			reused.CandidateGateSourceSHA256 = test.source
			reused.CandidateGateToolchainSHA256 = test.toolchain
			err := store.RecordProvisionalRemoteCIRun(reused)
			if err == nil || !strings.Contains(err.Error(), "all-hit") {
				t.Fatalf("all-hit candidate identity error = %v", err)
			}
		})
	}
}

// TestRecordProvisionalRemoteCIRunRejectsCandidateGateIdentityForFreshRun 验证 fresh workload 缺少候选 Gate 编译身份时拒绝写入。
func TestRecordProvisionalRemoteCIRunRejectsCandidateGateIdentityForFreshRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, _, _ := recordWorkloadPassRun(t, store, "record-fresh-empty", 1, "record-fresh-empty")
	fresh.CandidateGateSourceSHA256 = ""
	fresh.CandidateGateToolchainSHA256 = ""
	if err := store.RecordProvisionalRemoteCIRun(fresh); err == nil || !strings.Contains(err.Error(), "candidate gate compile identity") {
		t.Fatalf("fresh candidate identity error = %v", err)
	}
}

// TestFinalizeRemoteCIRunAuthorityRejectsCandidateGateIdentityForAllHit 验证 authority CAS 重新拒绝 all-hit 的非空候选身份。
func TestFinalizeRemoteCIRunAuthorityRejectsCandidateGateIdentityForAllHit(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	reused := recordReusedWorkloadPassRun(t, store, "finalize-all-hit-nonempty")
	reused.CandidateGateSourceSHA256 = digestForWorkloadPass("finalize-all-hit-source")
	reused.CandidateGateToolchainSHA256 = digestForWorkloadPass("finalize-all-hit-toolchain")
	updateRemoteCIRunCandidateGateIdentity(t, store, reused.JobID, reused.CandidateGateSourceSHA256, reused.CandidateGateToolchainSHA256)
	err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(reused), completeWorkloadPassReceipts(t, reused), nil, false)
	if err == nil || !strings.Contains(err.Error(), "all-hit") {
		t.Fatalf("all-hit authority candidate identity error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, reused.JobID, false)
}

// TestFinalizeRemoteCIRunAuthorityRejectsCandidateGateIdentityForFreshRun 验证 authority CAS 重新拒绝 fresh 缺失候选身份。
func TestFinalizeRemoteCIRunAuthorityRejectsCandidateGateIdentityForFreshRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, _, receipts := recordWorkloadPassRun(t, store, "finalize-fresh-empty", 1, "finalize-fresh-empty")
	fresh.CandidateGateSourceSHA256 = ""
	fresh.CandidateGateToolchainSHA256 = ""
	updateRemoteCIRunCandidateGateIdentity(t, store, fresh.JobID, "", "")
	err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, false)
	if err == nil || !strings.Contains(err.Error(), "candidate gate compile identity") {
		t.Fatalf("fresh authority candidate identity error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, fresh.JobID, false)
}

// recordReusedWorkloadPassRun 创建一个已验证 origin 的 all-hit provisional run。
func recordReusedWorkloadPassRun(t *testing.T, store *DurationLedgerStore, jobID string) RemoteCIRunRecord {
	t.Helper()
	fresh, identity, receipts := recordWorkloadPassRun(t, store, jobID+"-origin", 1, jobID+"-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	original := lookupSingleWorkloadPassEvidence(t, store, identity)
	reused := fresh
	reused.JobID = jobID
	reused.Authoritative = false
	reused.StartedAt = fresh.StartedAt.Add(time.Hour)
	reused.CompletedAt = reused.StartedAt.Add(time.Second)
	reused.Shards, reused.WorkloadExecutions, reused.TimingObservations = nil, nil, nil
	reused.CandidateGateSourceSHA256, reused.CandidateGateToolchainSHA256 = "", ""
	reused.WorkloadResults = []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: fresh.JobID, OriginAcceptedGeneration: fresh.AcceptedGeneration, EvidenceSHA256: original.EvidenceSHA256}}
	if err := store.RecordProvisionalRemoteCIRun(reused); err != nil {
		t.Fatal(err)
	}
	return reused
}

// updateRemoteCIRunCandidateGateIdentity 直接模拟持久化 candidate Gate 身份漂移，验证最终化重读守卫。
func updateRemoteCIRunCandidateGateIdentity(t *testing.T, store *DurationLedgerStore, jobID, source, toolchain string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_runs SET candidate_gate_source_sha256 = ?, candidate_gate_toolchain_sha256 = ? WHERE job_id = ?`, source, toolchain, jobID); err != nil {
		t.Fatalf("update candidate Gate identity: %v", err)
	}
}
