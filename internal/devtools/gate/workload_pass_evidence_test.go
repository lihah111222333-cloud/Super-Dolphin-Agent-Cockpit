package gate

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

// TestFinalizeRemoteCIRunAuthorityFreshPromotionAtomicallyAuthorizesAndReuses 验证 fresh run 只在完整事务提交后同时变为权威且可复用。
func TestFinalizeRemoteCIRunAuthorityFreshPromotionAtomicallyAuthorizesAndReuses(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordProvisionalWorkloadPassRun(t, store, "finalize-fresh", 1, "workload-finalize-fresh")

	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatalf("finalize fresh remote CI run authority: %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, true)
	if got := lookupSingleWorkloadPassEvidence(t, store, identity); got.OriginJobID != record.JobID {
		t.Fatalf("fresh finalization evidence origin = %q, want %q", got.OriginJobID, record.JobID)
	}
}

// TestFinalizeRemoteCIRunAuthorityAllHitDoesNotPromote 验证 all-hit run 可原子升权，但不会把 reused 结果提升为新的来源。
func TestFinalizeRemoteCIRunAuthorityAllHitDoesNotPromote(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, freshReceipts := recordWorkloadPassRun(t, store, "finalize-all-hit-origin", 1, "workload-finalize-all-hit")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), freshReceipts, nil, true); err != nil {
		t.Fatalf("finalize all-hit origin: %v", err)
	}
	original := lookupSingleWorkloadPassEvidence(t, store, identity)

	reused := fresh
	reused.JobID = "finalize-all-hit-consumer"
	reused.Authoritative = false
	reused.StartedAt = fresh.StartedAt.Add(time.Hour)
	reused.CompletedAt = reused.StartedAt.Add(time.Second)
	reused.Shards = nil
	reused.WorkloadExecutions = nil
	reused.TimingObservations = nil
	reused.WorkloadResults = []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: fresh.JobID, OriginAcceptedGeneration: fresh.AcceptedGeneration, EvidenceSHA256: original.EvidenceSHA256}}
	if err := store.RecordProvisionalRemoteCIRun(reused); err != nil {
		t.Fatalf("record provisional all-hit run: %v", err)
	}

	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(reused), completeWorkloadPassReceipts(t, reused), nil, false); err != nil {
		t.Fatalf("finalize all-hit remote CI run authority: %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, reused.JobID, true)
	if got := lookupSingleWorkloadPassEvidence(t, store, identity); !reflect.DeepEqual(got, original) {
		t.Fatalf("all-hit finalization changed promoted evidence: got %#v want %#v", got, original)
	}
	if countWorkloadPassEvidence(t, store, identity) != 1 {
		t.Fatal("all-hit finalization promoted a second evidence origin")
	}
}

// TestFinalizeRemoteCIRunAuthorityRollsBackEveryFailurePoint 验证任一最终化步骤失败都不会留下权威 run、回执或可复用证据。
func TestFinalizeRemoteCIRunAuthorityRollsBackEveryFailurePoint(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *DurationLedgerStore, RemoteCIRunRecord)
		mutate  func(RemoteCIRunAuthorityIdentity) RemoteCIRunAuthorityIdentity
		promote bool
	}{
		{
			name: "receipt append",
			prepare: func(t *testing.T, store *DurationLedgerStore, _ RemoteCIRunRecord) {
				installFinalizeFailure(t, store, durationLedgerFinalizeStepAppendReceipts)
			},
			promote: true,
		},
		{
			name: "receipt reload",
			prepare: func(t *testing.T, store *DurationLedgerStore, _ RemoteCIRunRecord) {
				installFinalizeFailure(t, store, durationLedgerFinalizeStepReloadReceipts)
			},
			promote: true,
		},
		{
			name: "identity",
			mutate: func(identity RemoteCIRunAuthorityIdentity) RemoteCIRunAuthorityIdentity {
				identity.PlanDigest = "sha256:forged-plan"
				return identity
			},
			promote: true,
		},
		{
			name: "CAS",
			prepare: func(t *testing.T, store *DurationLedgerStore, _ RemoteCIRunRecord) {
				installFinalizeFailure(t, store, durationLedgerFinalizeStepCAS)
			},
			promote: true,
		},
		{
			name: "promotion",
			prepare: func(t *testing.T, store *DurationLedgerStore, _ RemoteCIRunRecord) {
				installFinalizeFailure(t, store, durationLedgerFinalizeStepPromotion)
			},
			promote: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newWorkloadPassEvidenceStore(t, 1)
			record, workloadIdentity, receipts := recordProvisionalWorkloadPassRun(t, store, "finalize-rollback-"+strings.ReplaceAll(test.name, " ", "-"), 1, "workload-finalize-rollback-"+strings.ReplaceAll(test.name, " ", "-"))
			if test.prepare != nil {
				test.prepare(t, store, record)
			}
			identity := remoteCIRunAuthorityIdentity(record)
			if test.mutate != nil {
				identity = test.mutate(identity)
			}
			if err := store.FinalizeRemoteCIRunAuthorityWithSamples(identity, receipts, nil, test.promote); err == nil {
				t.Fatal("finalize remote CI run authority unexpectedly succeeded")
			}
			assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
			assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
			assertWorkloadPassLookupMiss(t, store, workloadIdentity)
		})
	}
}

// TestFinalizeRemoteCIRunAuthorityRejectsForgedRunSnapshot 确保回执快照绑定不可变 ci_runs 行，
// 而非调用方输入。
func TestFinalizeRemoteCIRunAuthorityRejectsForgedRunSnapshot(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, workloadIdentity, receipts := recordProvisionalWorkloadPassRun(t, store, "forged-run-snapshot", 1, "workload-forged-run-snapshot")
	identity := remoteCIRunAuthorityIdentity(record)
	identity.ImageCacheSnapshotID = "forged-snapshot"
	for index := range receipts {
		receipts[index].AcceptedSnapshotID = identity.ImageCacheSnapshotID
		digest, err := CheckReceiptSHA256(receipts[index])
		if err != nil {
			t.Fatal(err)
		}
		receipts[index].ReceiptSHA256 = digest
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(identity, receipts, nil, true); err == nil || !strings.Contains(err.Error(), "immutable run identity") {
		t.Fatalf("FinalizeRemoteCIRunAuthorityWithSamples() error = %v, want immutable snapshot rejection", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
	assertWorkloadPassLookupMiss(t, store, workloadIdentity)
}

// TestRecordProvisionalRemoteCIRunRejectsImageCacheSnapshotIdentityDrift 阻止重试
// 静默将 job 重绑到另一已接受 ECI 快照。
func TestRecordProvisionalRemoteCIRunRejectsImageCacheSnapshotIdentityDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, _ := recordProvisionalWorkloadPassRun(t, store, "run-snapshot-drift", 1, "workload-run-snapshot-drift")
	record.ImageCacheSnapshotID = "snapshot-drifted"
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "immutable run identity") {
		t.Fatalf("RecordProvisionalRemoteCIRun() error = %v, want snapshot identity drift rejection", err)
	}
}

// TestWorkloadPassEvidenceRequiresCompleteReceipts 拒绝缺少任一必需检查回执的晋级。
func TestWorkloadPassEvidenceRequiresCompleteReceipts(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, _ := recordWorkloadPassRun(t, store, "missing-receipts", 1, "workload-missing")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), nil, nil, true); err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatalf("promotion without receipts error = %v", err)
	}
}

// TestWorkloadPassEvidencePromotesAndLooksUpAuthoritativeRun 验证完整权威执行可提升并精确查回。
func TestWorkloadPassEvidencePromotesAndLooksUpAuthoritativeRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "fresh-authoritative", 1, "workload-fresh")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatalf("finalize fresh authoritative run: %v", err)
	}
	evidence := lookupSingleWorkloadPassEvidence(t, store, identity)
	if evidence.OriginJobID != record.JobID || evidence.OriginAcceptedGeneration != record.AcceptedGeneration {
		t.Fatalf("promoted evidence origin = %#v, want job %q generation %d", evidence, record.JobID, record.AcceptedGeneration)
	}
	if evidence.OriginExecution.GateID != identity.WorkloadID || evidence.OriginExecution.Status != ResultStatusPassed {
		t.Fatalf("promoted evidence execution = %#v", evidence.OriginExecution)
	}
}

// TestWorkloadPassEvidenceLookupRejectsReceiptMutation 拒绝删除或篡改已提升证据依赖的回执。
func TestWorkloadPassEvidenceLookupRejectsReceiptMutation(t *testing.T) {
	for _, mutation := range []string{"delete", "tamper"} {
		t.Run(mutation, func(t *testing.T) {
			store := newWorkloadPassEvidenceStore(t, 1)
			record, identity, receipts := recordWorkloadPassRun(t, store, "receipt-"+mutation, 1, "workload-"+mutation)
			if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
				t.Fatal(err)
			}
			mutateWorkloadPassReceipt(t, store, record.JobID, mutation)
			if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil || !strings.Contains(err.Error(), "receipt") {
				t.Fatalf("lookup after %s receipt error = %v", mutation, err)
			}
		})
	}
}

// TestWorkloadPassEvidenceDoesNotPromoteReusedResult 验证 reused 结果只引用旧来源而不会变成新来源。
func TestWorkloadPassEvidenceDoesNotPromoteReusedResult(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fresh, identity, receipts := recordWorkloadPassRun(t, store, "fresh-origin", 1, "workload-reused")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fresh), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	original := lookupSingleWorkloadPassEvidence(t, store, identity)
	reused := fresh
	reused.JobID = "reused-consumer"
	reused.StartedAt = fresh.StartedAt.Add(time.Hour)
	reused.CompletedAt = reused.StartedAt.Add(time.Second)
	reused.Shards = nil
	reused.WorkloadExecutions = nil
	reused.TimingObservations = nil
	reused.WorkloadResults[0] = RemoteCIWorkloadResult{Identity: identity, Disposition: WorkloadDispositionReused, OriginJobID: fresh.JobID, OriginAcceptedGeneration: fresh.AcceptedGeneration, EvidenceSHA256: original.EvidenceSHA256}
	if err := store.RecordProvisionalRemoteCIRun(reused); err != nil {
		t.Fatalf("record reused run: %v", err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(reused), completeWorkloadPassReceipts(t, reused), nil, false); err != nil {
		t.Fatalf("finalize reused run: %v", err)
	}
	got := lookupSingleWorkloadPassEvidence(t, store, identity)
	if got.OriginJobID != fresh.JobID || !reflect.DeepEqual(got, original) {
		t.Fatalf("reused run changed promoted origin: got %#v want %#v", got, original)
	}
	if countWorkloadPassEvidence(t, store, identity) != 1 {
		t.Fatal("reused run created a second promoted origin")
	}
}

// TestRemoteCIRunCatalogIndexRequiresExactWorkloadResultFreshPartition 拒绝把 reused 混入本次 fresh 分片或执行。
func TestRemoteCIRunCatalogIndexRequiresExactWorkloadResultFreshPartition(t *testing.T) {
	workloadID := GateID("workload-partition")
	index, err := newRemoteCIRunCatalogIndex(WorkloadCatalog{Workloads: []Workload{{ID: string(workloadID), Shardable: true}}})
	if err != nil {
		t.Fatal(err)
	}
	executed := RemoteCIWorkloadResult{Identity: WorkloadPassIdentity{WorkloadID: workloadID}, Disposition: WorkloadDispositionExecuted}
	reused := executed
	reused.Disposition = WorkloadDispositionReused
	shard := RemoteCIShardRecord{Workloads: []GateID{workloadID}}
	execution := PlanGateExecution{GateID: workloadID}
	for _, test := range []struct {
		name   string
		record RemoteCIRunRecord
		want   string
	}{
		{name: "all reused has no fresh records", record: RemoteCIRunRecord{WorkloadResults: []RemoteCIWorkloadResult{reused}}},
		{name: "executed has matching fresh records", record: RemoteCIRunRecord{WorkloadResults: []RemoteCIWorkloadResult{executed}, Shards: []RemoteCIShardRecord{shard}, WorkloadExecutions: []PlanGateExecution{execution}}},
		{name: "missing result", record: RemoteCIRunRecord{}, want: "does not cover"},
		{name: "executed missing shard", record: RemoteCIRunRecord{WorkloadResults: []RemoteCIWorkloadResult{executed}, WorkloadExecutions: []PlanGateExecution{execution}}, want: "missing from fresh shard"},
		{name: "executed missing execution", record: RemoteCIRunRecord{WorkloadResults: []RemoteCIWorkloadResult{executed}, Shards: []RemoteCIShardRecord{shard}}, want: "missing from fresh execution"},
		{name: "reused in fresh shard", record: RemoteCIRunRecord{WorkloadResults: []RemoteCIWorkloadResult{reused}, Shards: []RemoteCIShardRecord{shard}}, want: "is not executed"},
		{name: "reused in fresh execution", record: RemoteCIRunRecord{WorkloadResults: []RemoteCIWorkloadResult{reused}, WorkloadExecutions: []PlanGateExecution{execution}}, want: "is not executed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := index.validatePassed(test.record)
			if test.want == "" && err != nil {
				t.Fatalf("validate passed run: %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validate passed run error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestRemoteCIRunCatalogIndexAcceptsOwnerExecutionFromCatalog 保留 owner-only 证明并与 shardable 覆盖分离。
func TestRemoteCIRunCatalogIndexAcceptsOwnerExecutionFromCatalog(t *testing.T) {
	workloadID := GateID("workload-partition")
	ownerID := GateIDReleaseLayeredCheck
	if _, err := newRemoteCIRunCatalogIndex(WorkloadCatalog{Workloads: []Workload{{ID: "non-owner", Shardable: false}}}); err == nil || !strings.Contains(err.Error(), "is not the release owner") {
		t.Fatalf("non-owner catalog error = %v", err)
	}
	index, err := newRemoteCIRunCatalogIndex(WorkloadCatalog{Workloads: []Workload{
		{ID: string(workloadID), Shardable: true},
		{ID: string(ownerID), Shardable: false},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := index.shardable[ownerID]; ok {
		t.Fatal("owner-only catalog entry was indexed as shardable")
	}
	if _, ok := index.executions[ownerID]; !ok {
		t.Fatal("owner-only catalog entry was not indexed as a direct execution")
	}
	record := RemoteCIRunRecord{
		Status:     ResultStatusPassed,
		Executions: []PlanGateExecution{{GateID: ownerID}},
	}
	if err := index.validateRecorded(record, []GateID{workloadID}); err != nil {
		t.Fatalf("validate recorded owner execution: %v", err)
	}
	record.Executions = append(record.Executions, PlanGateExecution{GateID: GateID("release:missing-owner")})
	if err := index.validateRecorded(record, nil); err == nil || !strings.Contains(err.Error(), "absent from its catalog") {
		t.Fatalf("validate recorded unknown owner error = %v", err)
	}
}

// TestWorkloadPassEvidenceConcurrentPromotionKeepsJobsIsolated 验证两个 fresh job 并发提升不会串写来源。
func TestWorkloadPassEvidenceConcurrentPromotionKeepsJobsIsolated(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	first, firstIdentity, firstReceipts := recordWorkloadPassRun(t, store, "concurrent-first", 1, "workload-first")
	second, secondIdentity, secondReceipts := recordWorkloadPassRun(t, store, "concurrent-second", 1, "workload-second")
	start := make(chan struct{})
	var group errgroup.Group
	for _, run := range []struct {
		record   RemoteCIRunRecord
		receipts []CheckReceiptRecord
	}{{first, firstReceipts}, {second, secondReceipts}} {
		group.Go(func() error {
			<-start
			return store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(run.record), run.receipts, nil, true)
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent promotion: %v", err)
	}
	if got := lookupSingleWorkloadPassEvidence(t, store, firstIdentity); got.OriginJobID != first.JobID {
		t.Fatalf("first concurrent evidence origin = %q", got.OriginJobID)
	}
	if got := lookupSingleWorkloadPassEvidence(t, store, secondIdentity); got.OriginJobID != second.JobID {
		t.Fatalf("second concurrent evidence origin = %q", got.OriginJobID)
	}
}

// TestWorkloadPassEvidenceLookupRestrictsAcceptedGeneration 验证 lookup 只复用当前代向前最近三代的真实证据。
func TestWorkloadPassEvidenceLookupRestrictsAcceptedGeneration(t *testing.T) {
	t.Run("generation one expires when accepted singleton is four", func(t *testing.T) {
		store := newWorkloadPassEvidenceStore(t, 4)
		record, identity, receipts := recordWorkloadPassRun(t, store, "expired-generation-one", 1, "workload-expired")
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatal(err)
		}
		assertWorkloadPassLookupMiss(t, store, identity)
	})
	t.Run("generation two remains reusable", func(t *testing.T) {
		store := newWorkloadPassEvidenceStore(t, 4)
		record, identity, receipts := recordWorkloadPassRun(t, store, "retained-generation-two", 2, "workload-retained")
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatal(err)
		}
		if evidence := lookupSingleWorkloadPassEvidence(t, store, identity); evidence.OriginAcceptedGeneration != 2 {
			t.Fatalf("retained evidence generation = %d, want 2", evidence.OriginAcceptedGeneration)
		}
	})
	t.Run("very old completion remains reusable within generation window", func(t *testing.T) {
		store := newWorkloadPassEvidenceStore(t, 4)
		completedAt := time.Date(2001, time.January, 2, 3, 4, 5, 0, time.UTC)
		record, identity, receipts := recordWorkloadPassRunAt(t, store, "old-completion-generation-two", 2, "workload-old-completion", completedAt)
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatal(err)
		}
		if evidence := lookupSingleWorkloadPassEvidence(t, store, identity); evidence.OriginAcceptedGeneration != 2 {
			t.Fatalf("old retained evidence generation = %d, want 2", evidence.OriginAcceptedGeneration)
		}
	})
	t.Run("future generation is rejected", func(t *testing.T) {
		store := newWorkloadPassEvidenceStore(t, 4)
		record, identity, receipts := recordWorkloadPassRun(t, store, "forged-future-generation", 2, "workload-future")
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatal(err)
		}
		forged := lookupSingleWorkloadPassEvidence(t, store, identity)
		forged.OriginAcceptedGeneration = 5
		digest, err := WorkloadPassEvidenceSHA256(forged)
		if err != nil {
			t.Fatal(err)
		}
		database := openWorkloadPassDatabase(t, store)
		defer database.Close()
		if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET accepted_generation = ?, evidence_sha256 = ? WHERE identity_digest = ?`, "5", digest, identity.IdentityDigest); err != nil {
			t.Fatal(err)
		}
		assertWorkloadPassLookupMiss(t, store, identity)
	})
}

func TestFinalizeRemoteCIRunAuthorityPromotesOnlyCompleteProvisionalRun(t *testing.T) {
	t.Run("complete provisional run is promoted by CAS", testFinalizeCompleteProvisionalRun)
	t.Run("duration sample failure rolls back authority and receipts", testFinalizeDurationSampleFailureRollback)
	t.Run("incomplete provisional timing cannot be promoted", testFinalizeIncompleteProvisionalTiming)
}

// testFinalizeCompleteProvisionalRun 验证完整 provisional run 只有在 CAS 成功后才携带样本成为权威记录。
func testFinalizeCompleteProvisionalRun(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "finalize-complete-provisional", 1, "workload-finalize-complete")
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("persist provisional remote CI run: %v", err)
	}
	samples := []DurationSample{testDurationSample("finalize-complete", testWorkloadDigest, true, 1)}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, samples, true); err != nil {
		t.Fatalf("finalize complete provisional remote CI run: %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, true)
	assertDurationSampleCount(t, store, 1)
}

// testFinalizeDurationSampleFailureRollback 验证样本写入失败会回滚 CAS、回执和样本，避免留下部分权威状态。
func testFinalizeDurationSampleFailureRollback(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "finalize-sample-rollback", 1, "workload-finalize-sample-rollback")
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("persist provisional remote CI run: %v", err)
	}
	installFinalizeFailure(t, store, durationLedgerFinalizeStepAppendSamples)
	err := store.FinalizeRemoteCIRunAuthorityWithSamples(
		remoteCIRunAuthorityIdentity(record),
		receipts,
		[]DurationSample{testDurationSample("finalize-sample-rollback", testWorkloadDigest, true, 1)},
		true,
	)
	if err == nil {
		t.Fatal("finalize with failing duration sample unexpectedly succeeded")
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
	assertDurationSampleCount(t, store, 0)
}

// testFinalizeIncompleteProvisionalTiming 验证缺失 timing observations 的 provisional run 不能被最终化。
func testFinalizeIncompleteProvisionalTiming(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "finalize-incomplete-provisional", 1, "workload-finalize-incomplete")
	clearPersistedTimingObservationsForTest(t, store, record.JobID)
	err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true)
	if err == nil || !strings.Contains(err.Error(), "requires complete timing observations") {
		t.Fatalf("finalize incomplete provisional remote CI run error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
}

// assertDurationSampleCount 读取持久化 ledger，确认成功和失败最终化不会混淆样本可见性。
func assertDurationSampleCount(t *testing.T, store *DurationLedgerStore, want int) {
	t.Helper()
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snapshot.Ledger.Samples); got != want {
		t.Fatalf("persisted duration sample count = %d, want %d", got, want)
	}
}

// TestWorkloadPassEvidenceLookupRejectsRecomputedOriginBindingTampering 验证重算摘要不能伪造 evidence 与 origin run 的 generation 或 tree 绑定。
func TestWorkloadPassEvidenceLookupRejectsRecomputedOriginBindingTampering(t *testing.T) {
	for _, test := range []struct {
		name              string
		generation        uint64
		advanceGeneration uint64
		mutate            func(*WorkloadPassEvidence)
		update            func(*sql.DB, WorkloadPassEvidence, WorkloadPassIdentity) error
	}{
		{
			name:              "old generation moved into retained window",
			generation:        1,
			advanceGeneration: 4,
			mutate: func(evidence *WorkloadPassEvidence) {
				evidence.OriginAcceptedGeneration = 2
			},
			update: func(database *sql.DB, evidence WorkloadPassEvidence, identity WorkloadPassIdentity) error {
				_, err := database.Exec(`UPDATE ci_workload_pass_evidence SET accepted_generation = ?, evidence_sha256 = ? WHERE identity_digest = ?`, fmt.Sprintf("%d", evidence.OriginAcceptedGeneration), evidence.EvidenceSHA256, identity.IdentityDigest)
				return err
			},
		},
		{
			name:       "source tree changed",
			generation: 1,
			mutate: func(evidence *WorkloadPassEvidence) {
				evidence.OriginSourceTreeSHA = strings.Repeat("f", 40)
			},
			update: func(database *sql.DB, evidence WorkloadPassEvidence, identity WorkloadPassIdentity) error {
				_, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_source_tree_sha = ?, evidence_sha256 = ? WHERE identity_digest = ?`, evidence.OriginSourceTreeSHA, evidence.EvidenceSHA256, identity.IdentityDigest)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newWorkloadPassEvidenceStore(t, test.generation)
			record, identity, receipts := recordWorkloadPassRun(t, store, "recomputed-origin-"+test.name, 1, "workload-recomputed-origin")
			if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
				t.Fatal(err)
			}
			forged := lookupSingleWorkloadPassEvidence(t, store, identity)
			if test.advanceGeneration != 0 {
				seedAcceptedGenerationForTest(t, store, test.advanceGeneration)
			}
			test.mutate(&forged)
			var err error
			forged.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(forged)
			if err != nil {
				t.Fatal(err)
			}
			database := openWorkloadPassDatabase(t, store)
			defer database.Close()
			if err := test.update(database, forged, identity); err != nil {
				t.Fatal(err)
			}
			assertWorkloadPassLookupMiss(t, store, identity)
		})
	}
}

// TestWorkloadPassEvidenceLookupRejectsRecomputedOriginAgentTampering 验证重算回执摘要也不能将另一 agent 的回执伪装为 origin run。
func TestWorkloadPassEvidenceLookupRejectsRecomputedOriginAgentTampering(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "recomputed-origin-agent", 1, "workload-recomputed-origin-agent")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	forgedDigest := digestForWorkloadPass("forged-agent")
	for index := range receipts {
		receipts[index].AgentTokenDigest = forgedDigest
		var err error
		receipts[index].ReceiptSHA256, err = CheckReceiptSHA256(receipts[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	for _, receipt := range receipts {
		if _, err := database.Exec(`UPDATE ci_check_receipts SET agent_token_digest = ?, receipt_sha256 = ? WHERE job_id = ? AND required_check = ?`, receipt.AgentTokenDigest, receipt.ReceiptSHA256, record.JobID, receipt.RequiredCheck); err != nil {
			t.Fatal(err)
		}
	}
	assertWorkloadPassLookupMiss(t, store, identity)
}

// TestWorkloadPassEvidenceRetainsThreeLatestGenerations 验证第四个 accepted generation 同事务淘汰最早证据。
func TestWorkloadPassEvidenceRetainsThreeLatestGenerations(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 4)
	identity := WorkloadPassIdentity{}
	for generation := uint64(1); generation <= 4; generation++ {
		record, current, receipts := recordWorkloadPassRun(t, store, fmt.Sprintf("retention-%d", generation), generation, "workload-retention")
		if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
			t.Fatalf("finalize generation %d: %v", generation, err)
		}
		identity = current
	}
	if countWorkloadPassEvidence(t, store, identity) != 3 {
		t.Fatalf("retained workload pass generations = %d, want 3", countWorkloadPassEvidence(t, store, identity))
	}
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err != nil {
		t.Fatalf("lookup retained latest evidence: %v", err)
	}
	assertWorkloadPassGenerationAbsent(t, store, 1)
}

// TestWorkloadPassEvidenceRejectsForgedIdentityAndOrigin 验证查找严格匹配所有身份分量和 fresh execution 来源。
func TestWorkloadPassEvidenceRejectsForgedIdentityAndOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "identity-origin", 1, "workload-identity")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*WorkloadPassIdentity){
		func(value *WorkloadPassIdentity) { value.ExecutionDigest = digestForWorkloadPass("forged-execution") },
		func(value *WorkloadPassIdentity) { value.InputDigest = digestForWorkloadPass("forged-input") },
		func(value *WorkloadPassIdentity) {
			value.EnvironmentDigest = digestForWorkloadPass("forged-environment")
		},
	} {
		forged := identity
		mutate(&forged)
		forged.IdentityDigest = workloadPassIdentityDigest(t, forged)
		if evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{forged}); err != nil || len(evidence) != 0 {
			t.Fatalf("forged identity lookup = %#v, %v", evidence, err)
		}
	}
	other, _, otherReceipts := recordWorkloadPassRun(t, store, "other-origin", 1, "workload-other-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(other), otherReceipts, nil, true); err != nil {
		t.Fatalf("finalize other origin: %v", err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_job_id = ? WHERE identity_digest = ?`, other.JobID, identity.IdentityDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity}); err == nil {
		t.Fatal("lookup accepted forged origin")
	}
}

// TestFinalizeRemoteCIRunAuthorityRejectsCatalogIdentityDrift 验证最终化不能
// 提升与 canonical workload catalog 不一致的 executed identity。
func TestFinalizeRemoteCIRunAuthorityRejectsCatalogIdentityDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*WorkloadPassIdentity)
	}{
		{name: "execution_digest", mutate: func(identity *WorkloadPassIdentity) {
			identity.ExecutionDigest = digestForWorkloadPass("forged-execution")
		}},
		{name: "input_digest", mutate: func(identity *WorkloadPassIdentity) {
			identity.InputDigest = digestForWorkloadPass("forged-input")
		}},
		{name: "environment_digest", mutate: func(identity *WorkloadPassIdentity) {
			identity.EnvironmentDigest = digestForWorkloadPass("forged-environment")
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertFinalizationRejectsPersistedWorkloadIdentityDrift(t, testCase.mutate)
		})
	}
}

// assertFinalizationRejectsPersistedWorkloadIdentityDrift 篡改单个持久化身份字段并验证最终化 fail-fast。
func assertFinalizationRejectsPersistedWorkloadIdentityDrift(t *testing.T, mutate func(*WorkloadPassIdentity)) {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "catalog-identity-drift", 1, "workload-catalog-identity-drift")
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	forged := record.WorkloadResults[0].Identity
	mutate(&forged)
	forged.IdentityDigest = workloadPassIdentityDigest(t, forged)
	if _, err := database.Exec(`UPDATE ci_run_workload_results SET identity_digest = ?, execution_digest = ?, input_digest = ?, environment_digest = ? WHERE job_id = ? AND workload_id = ?`, forged.IdentityDigest, forged.ExecutionDigest, forged.InputDigest, forged.EnvironmentDigest, record.JobID, string(forged.WorkloadID)); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err == nil {
		t.Fatal("finalization accepted workload identity drift from canonical catalog")
	}
}

// recordProvisionalWorkloadPassRun 构造等待最终化的 fresh run，保留全部可提升输入但不预先授予 authority。
func recordProvisionalWorkloadPassRun(t *testing.T, store *DurationLedgerStore, jobID string, generation uint64, workload string) (RemoteCIRunRecord, WorkloadPassIdentity, []CheckReceiptRecord) {
	t.Helper()
	record, identity, receipts := recordWorkloadPassRun(t, store, jobID, generation, workload)
	record.Authoritative = false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record provisional workload pass run: %v", err)
	}
	return record, identity, receipts
}

// remoteCIRunAuthorityIdentity 从持久化前的 immutable run 字段构造最终化 CAS 身份。
func remoteCIRunAuthorityIdentity(record RemoteCIRunRecord) RemoteCIRunAuthorityIdentity {
	identities := make([]WorkloadPassIdentity, 0, len(record.WorkloadResults))
	for _, result := range record.WorkloadResults {
		identities = append(identities, result.Identity)
	}
	return RemoteCIRunAuthorityIdentity{JobID: record.JobID, AgentTokenDigest: record.AgentTokenDigest, Force: record.Force, Entrypoint: record.Entrypoint, Profile: record.Profile, PlanDigest: record.PlanDigest, CatalogDigest: record.CatalogDigest, AcceptedGeneration: record.AcceptedGeneration, ImageCacheSnapshotID: record.ImageCacheSnapshotID, SourceTreeSHA: record.SourceTreeSHA, CandidateGateSourceSHA256: record.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: record.CandidateGateToolchainSHA256, RunnerImage: record.RunnerImage, StartedAt: record.StartedAt, WorkloadPassIdentities: identities}
}

// installFinalizeFailure 在 store 私有最终化 hook 上注入一个精确失败点，不改变 SQLite schema。
func installFinalizeFailure(t *testing.T, store *DurationLedgerStore, step durationLedgerFinalizeStep) {
	t.Helper()
	store.finalizeFault = func(got durationLedgerFinalizeStep) error {
		if got != step {
			return nil
		}
		return fmt.Errorf("injected %s failure", step)
	}
}

// clearPersistedTimingObservationsForTest 先写入 canonical provisional fixture，再仅删除观测行，
// 以验证 finalizer 对“已持久化但不完整”状态的拒绝而不放宽 Record 校验。
func clearPersistedTimingObservationsForTest(t *testing.T, store *DurationLedgerStore, jobID string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	result, err := database.Exec(`DELETE FROM ci_timing_observations WHERE job_id = ?`, jobID)
	if err != nil {
		t.Fatalf("clear persisted timing observations: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		t.Fatalf("read cleared timing observation count: %v", err)
	} else if affected == 0 {
		t.Fatalf("clear persisted timing observations affected %d rows, want at least one", affected)
	}
}

// assertRemoteCIRunAuthoritative 直接检查事务可见的 run authority 标记。
func assertRemoteCIRunAuthoritative(t *testing.T, store *DurationLedgerStore, jobID string, want bool) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var got int
	if err := database.QueryRow(`SELECT authoritative FROM ci_runs WHERE job_id = ?`, jobID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if (got == 1) != want {
		t.Fatalf("run %q authoritative = %d, want %t", jobID, got, want)
	}
}

// assertRemoteCIRunReceiptCount 确认回滚没有残留本次最终化写入的回执。
func assertRemoteCIRunReceiptCount(t *testing.T, store *DurationLedgerStore, jobID string, want int) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var got int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_check_receipts WHERE job_id = ?`, jobID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("run %q receipt count = %d, want %d", jobID, got, want)
	}
}

// newWorkloadPassEvidenceStore 创建绑定到指定 accepted generation 的真实 SQLite fixture。
func newWorkloadPassEvidenceStore(t *testing.T, generation uint64) *DurationLedgerStore {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, generation)
	return store
}

// recordWorkloadPassRun 写入一个有完整 executed workload 的 provisional run，但不写检查回执。
func recordWorkloadPassRun(t *testing.T, store *DurationLedgerStore, jobID string, generation uint64, workload string) (RemoteCIRunRecord, WorkloadPassIdentity, []CheckReceiptRecord) {
	t.Helper()
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC).Add(time.Duration(generation) * time.Hour)
	return recordWorkloadPassRunAt(t, store, jobID, generation, workload, now)
}

// recordWorkloadPassRunAt 写入指定完成时间的完整权威 run，供 freshness 的无 TTL 语义验证。
func recordWorkloadPassRunAt(t *testing.T, store *DurationLedgerStore, jobID string, generation uint64, workload string, now time.Time) (RemoteCIRunRecord, WorkloadPassIdentity, []CheckReceiptRecord) {
	t.Helper()
	treeSHA := strings.Repeat(fmt.Sprintf("%x", generation), 40)
	workloadID := GateIDBackendTestWithGuard
	catalogWorkload := Workload{ID: string(workloadID), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), InputDigest: digestForWorkloadPass("input-" + workload), BootstrapEstimateMS: 1, Shardable: true}
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{catalogWorkload}}
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{SourceTreeSHA: treeSHA, Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: generation, ObservedAt: now}); err != nil {
		t.Fatalf("record workload catalog: %v", err)
	}
	identity := WorkloadPassIdentity{WorkloadID: workloadID, ExecutionDigest: WorkloadPassExecutionDigest(catalogWorkload), InputDigest: catalogWorkload.InputDigest, EnvironmentDigest: digestForWorkloadPass("environment-" + workload)}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	shard := RemoteCIShardRecord{ShardIdentity: digestForWorkloadPass("shard-" + jobID), ContainerGroup: "eci-" + jobID, ContainerStatus: "Succeeded", Workloads: []GateID{workloadID}, MaterializationTiming: measuredShardMaterializationTiming(digestForWorkloadPass("shard-" + jobID)), Resources: RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8}}
	execution := PlanGateExecution{ShardIdentity: shard.ShardIdentity, GateID: workloadID, Status: ResultStatusPassed, StartedAt: now.Add(3 * time.Millisecond), CompletedAt: now.Add(10 * time.Millisecond), ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
	record := RemoteCIRunRecord{JobID: jobID, AgentTokenDigest: digestForWorkloadPass("agent-" + jobID), Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: generation, ImageCacheSnapshotID: fmt.Sprintf("snapshot-%d", generation), PlanDigest: "sha256:plan", CatalogDigest: catalogDigest, SourceTreeSHA: treeSHA, CandidateGateSourceSHA256: digestForWorkloadPass("gate-source-" + jobID), CandidateGateToolchainSHA256: digestForWorkloadPass("gate-toolchain-" + jobID), RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: false, StartedAt: now, CompletedAt: now.Add(time.Second), CleanupComplete: true, Shards: []RemoteCIShardRecord{shard}, WorkloadExecutions: []PlanGateExecution{execution}, WorkloadResults: []RemoteCIWorkloadResult{{Identity: identity, Disposition: WorkloadDispositionExecuted, OriginJobID: jobID, OriginAcceptedGeneration: generation}}, TimingObservations: authoritativeTimingObservationsForTest(jobID, execution)}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record workload pass run: %v", err)
	}
	return record, identity, completeWorkloadPassReceipts(t, record)
}

// completeWorkloadPassReceipts 构造与 run 的 tree、generation 和 agent 身份完全绑定的全部回执。
func completeWorkloadPassReceipts(t *testing.T, record RemoteCIRunRecord) []CheckReceiptRecord {
	t.Helper()
	receipts := completeWorkloadPassReceiptsForTime(record, record.StartedAt)
	for index := range receipts {
		receipts[index].AcceptedGeneration = record.AcceptedGeneration
		receipts[index].AcceptedSnapshotID = record.ImageCacheSnapshotID
		receipts[index].AgentTokenDigest = record.AgentTokenDigest
		digest, err := CheckReceiptSHA256(receipts[index])
		if err != nil {
			t.Fatal(err)
		}
		receipts[index].ReceiptSHA256 = digest
	}
	return receipts
}

// completeWorkloadPassReceiptsForTime 构造测试目录实际覆盖的 normal 检查回执。
func completeWorkloadPassReceiptsForTime(record RemoteCIRunRecord, startedAt time.Time) []CheckReceiptRecord {
	receipts := testCompleteCheckReceipts(record.JobID, record.SourceTreeSHA, startedAt)[1:2]
	return append([]CheckReceiptRecord(nil), receipts...)
}

// lookupSingleWorkloadPassEvidence 查回恰好一条严格匹配的提升证据。
func lookupSingleWorkloadPassEvidence(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) WorkloadPassEvidence {
	t.Helper()
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup workload pass evidence: %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("lookup workload pass evidence count = %d, want 1", len(evidence))
	}
	return evidence[0]
}

// assertWorkloadPassLookupMiss 确认 SQLite lookup 不返回不可复用的证据。
func assertWorkloadPassLookupMiss(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) {
	t.Helper()
	evidence, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		return
	}
	if len(evidence) != 0 {
		t.Fatalf("workload pass evidence = %#v, want lookup miss", evidence)
	}
}

// mutateWorkloadPassReceipt 直接模拟持久化回执删除或不重算摘要的篡改。
func mutateWorkloadPassReceipt(t *testing.T, store *DurationLedgerStore, jobID, mutation string) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	query := `DELETE FROM ci_check_receipts WHERE rowid IN (SELECT rowid FROM ci_check_receipts WHERE job_id = ? LIMIT 1)`
	if mutation == "tamper" {
		query = `UPDATE ci_check_receipts SET passed = 0 WHERE job_id = ? AND required_check = ?`
	}
	arguments := []any{jobID}
	if mutation == "tamper" {
		arguments = append(arguments, cicontract.RequiredCheckNormal)
	}
	if _, err := database.Exec(query, arguments...); err != nil {
		t.Fatalf("%s workload receipt: %v", mutation, err)
	}
}

// countWorkloadPassEvidence 返回精确 identity 当前保留的证据代数。
func countWorkloadPassEvidence(t *testing.T, store *DurationLedgerStore, identity WorkloadPassIdentity) int {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE identity_digest = ?`, identity.IdentityDigest).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// assertWorkloadPassGenerationAbsent 确认 compaction 已删除指定 accepted generation 的 PASS 证据。
func assertWorkloadPassGenerationAbsent(t *testing.T, store *DurationLedgerStore, generation uint64) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_workload_pass_evidence WHERE accepted_generation = ?`, fmt.Sprintf("%d", generation)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("generation %d workload pass evidence count = %d, want 0", generation, count)
	}
}

// openWorkloadPassDatabase 打开本测试唯一的 SQLite authority 以验证持久化边界。
func openWorkloadPassDatabase(t *testing.T, store *DurationLedgerStore) *sql.DB {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

// workloadPassIdentityDigest 生成内容绑定的 identity 摘要。
func workloadPassIdentityDigest(t *testing.T, identity WorkloadPassIdentity) string {
	t.Helper()
	digest, err := WorkloadPassIdentitySHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// digestForWorkloadPass 生成测试专用、格式正确的 SHA-256 文本。
func digestForWorkloadPass(value string) string {
	return "sha256:" + fmt.Sprintf("%064x", len(value))
}
