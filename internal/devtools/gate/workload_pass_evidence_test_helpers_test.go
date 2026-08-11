package gate

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type secondReceiptTamperFixture struct {
	store         *DurationLedgerStore
	mixed         RemoteCIRunRecord
	mixedReceipts []CheckReceiptRecord
	freshIdentity WorkloadPassIdentity
	freshEvidence WorkloadPassEvidence
	consumer      RemoteCIRunRecord
	freshCatalog  WorkloadCatalog
}

// newSecondReceiptTamperFixture 构造两条回执的 authoritative origin 和只复用 fresh workload 的 consumer。
func newSecondReceiptTamperFixture(t *testing.T) secondReceiptTamperFixture {
	t.Helper()
	store := newWorkloadPassEvidenceStore(t, 1)
	origin, staleIdentity, _ := recordWorkloadPassRunAtForRetentionID(t, store, "receipt-set-origin", 1, "mixed-origin", GateIDWhitespaceCheck)
	originReceipts := completeRetentionReceiptsForWorkloadID(t, origin, GateIDWhitespaceCheck)
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(origin), originReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	originEvidence := lookupSingleWorkloadPassEvidence(t, store, staleIdentity)
	seedAcceptedGenerationForTest(t, store, 2)
	mixed, freshIdentity := recordMixedRetentionConsumer(t, store, origin, originEvidence)
	mixedReceipts := completeWorkloadPassReceiptsForCatalog(t, mixed, mixedRetentionCatalog(t))
	if len(mixedReceipts) != 2 {
		t.Fatalf("mixed receipt count = %d, want 2", len(mixedReceipts))
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(mixed), mixedReceipts, nil, true); err != nil {
		t.Fatal(err)
	}
	freshEvidence := lookupSingleWorkloadPassEvidence(t, store, freshIdentity)
	consumer, freshCatalog := recordSecondReceiptTamperConsumer(t, store, mixed, freshIdentity, freshEvidence)
	return secondReceiptTamperFixture{
		store: store, mixed: mixed, mixedReceipts: mixedReceipts, freshIdentity: freshIdentity,
		freshEvidence: freshEvidence, consumer: consumer, freshCatalog: freshCatalog,
	}
}

// recordSecondReceiptTamperConsumer 写入只复用 fresh workload 的 all-hit consumer。
func recordSecondReceiptTamperConsumer(t *testing.T, store *DurationLedgerStore, mixed RemoteCIRunRecord, freshIdentity WorkloadPassIdentity, freshEvidence WorkloadPassEvidence) (RemoteCIRunRecord, WorkloadCatalog) {
	t.Helper()
	consumer := mixed
	consumer.JobID = "receipt-set-consumer"
	consumer.AgentTokenDigest = digestForWorkloadPass("receipt-set-consumer-agent")
	consumer.SourceTreeSHA = strings.Repeat("3", 40)
	consumer.CandidateGateSourceSHA256 = ""
	consumer.CandidateGateToolchainSHA256 = ""
	consumer.StartedAt = mixed.StartedAt.Add(time.Hour)
	consumer.CompletedAt = consumer.StartedAt.Add(time.Second)
	consumer.Shards, consumer.WorkloadExecutions, consumer.TimingObservations = nil, nil, nil
	freshCatalog := mixedRetentionCatalog(t)
	freshCatalog.Workloads = append([]Workload(nil), freshCatalog.Workloads[1])
	catalogDigest, err := WorkloadCatalogDigest(freshCatalog)
	if err != nil {
		t.Fatal(err)
	}
	consumer.CatalogDigest = catalogDigest
	if err := store.RecordWorkloadCatalog(freshCatalog, WorkloadCatalogObservation{SourceTreeSHA: consumer.SourceTreeSHA, Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: consumer.AcceptedGeneration, ObservedAt: consumer.StartedAt}); err != nil {
		t.Fatal(err)
	}
	consumer.WorkloadResults = []RemoteCIWorkloadResult{{Identity: freshIdentity, Disposition: WorkloadDispositionReused, OriginJobID: mixed.JobID, OriginAcceptedGeneration: mixed.AcceptedGeneration, EvidenceSHA256: freshEvidence.EvidenceSHA256}}
	if err := store.RecordProvisionalRemoteCIRun(consumer); err != nil {
		t.Fatal(err)
	}
	return consumer, freshCatalog
}

// forgeSecondReceiptDigests 只篡改第二条回执并重算行、集合及 evidence 摘要。
func forgeSecondReceiptDigests(t *testing.T, fixture secondReceiptTamperFixture) (CheckReceiptRecord, WorkloadPassEvidence) {
	t.Helper()
	tamperedReceipts := append([]CheckReceiptRecord(nil), fixture.mixedReceipts...)
	second := &tamperedReceipts[1]
	if second.RequiredCheck != cicontract.RequiredCheckNormal {
		t.Fatalf("second mixed receipt = %q, want normal", second.RequiredCheck)
	}
	second.AgentTokenDigest = digestForWorkloadPass("receipt-set-tampered-second-agent")
	secondDigest, err := CheckReceiptSHA256(*second)
	if err != nil {
		t.Fatal(err)
	}
	second.ReceiptSHA256 = secondDigest
	tamperedSetDigest, err := digestWorkloadReceiptSet(tamperedReceipts)
	if err != nil {
		t.Fatal(err)
	}
	forgedEvidence := fixture.freshEvidence
	forgedEvidence.OriginReceiptSetSHA256 = tamperedSetDigest
	forgedEvidence.EvidenceSHA256, err = WorkloadPassEvidenceSHA256(forgedEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if second.ReceiptSHA256 == fixture.mixedReceipts[1].ReceiptSHA256 || tamperedSetDigest == fixture.freshEvidence.OriginReceiptSetSHA256 || forgedEvidence.EvidenceSHA256 == fixture.freshEvidence.EvidenceSHA256 {
		t.Fatal("second receipt tampering did not change all recomputed digests")
	}
	return *second, forgedEvidence
}

// persistSecondReceiptTampering 同步篡改回执、origin evidence 与 consumer projection。
func persistSecondReceiptTampering(t *testing.T, fixture secondReceiptTamperFixture, second CheckReceiptRecord, forgedEvidence WorkloadPassEvidence) {
	t.Helper()
	database := openWorkloadPassDatabase(t, fixture.store)
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_check_receipts SET agent_token_digest = ?, receipt_sha256 = ? WHERE job_id = ? AND required_check = ?`, second.AgentTokenDigest, second.ReceiptSHA256, fixture.mixed.JobID, second.RequiredCheck); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET origin_receipt_set_sha256 = ?, evidence_sha256 = ? WHERE identity_digest = ? AND accepted_generation = ?`, forgedEvidence.OriginReceiptSetSHA256, forgedEvidence.EvidenceSHA256, fixture.freshIdentity.IdentityDigest, fmt.Sprintf("%d", forgedEvidence.OriginAcceptedGeneration)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE ci_run_workload_results SET evidence_sha256 = ? WHERE job_id = ? AND identity_digest = ?`, forgedEvidence.EvidenceSHA256, fixture.consumer.JobID, fixture.freshIdentity.IdentityDigest); err != nil {
		t.Fatal(err)
	}
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
