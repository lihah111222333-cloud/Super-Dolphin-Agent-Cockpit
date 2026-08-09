package gate

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestFinalizeRemoteCIRunAuthorityRejectsCatalogDigestDrift 验证最终化回读目录内容摘要，拒绝 SQLite 内容漂移。
func TestFinalizeRemoteCIRunAuthorityRejectsCatalogDigestDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, workloadIdentity, receipts := recordWorkloadPassRun(t, store, "finalize-catalog-digest-drift", 1, "workload-catalog-digest-drift")
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_catalog_workloads SET command_digest = ? WHERE catalog_digest = ?`, strings.Repeat("b", 64), record.CatalogDigest); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err == nil || !strings.Contains(err.Error(), "does not match its digest") {
		t.Fatalf("finalize catalog digest drift error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
	assertWorkloadPassLookupMiss(t, store, workloadIdentity)
}

// TestFinalizeRemoteCIRunAuthorityRejectsCatalogObservationIdentityDrift 验证最终化必须命中与 run identity 完全一致的目录观测。
func TestFinalizeRemoteCIRunAuthorityRejectsCatalogObservationIdentityDrift(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, workloadIdentity, receipts := recordWorkloadPassRun(t, store, "finalize-catalog-observation-drift", 1, "workload-catalog-observation-drift")
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_catalog_observations SET profile = ? WHERE catalog_digest = ?`, string(ProfilePush), record.CatalogDigest); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err == nil || !strings.Contains(err.Error(), "matching workload catalog observation") {
		t.Fatalf("finalize catalog observation drift error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
	assertWorkloadPassLookupMiss(t, store, workloadIdentity)
}

// TestFinalizeRemoteCIRunAuthorityRejectsNonAuthoritativeCatalog 验证最终化不能把被篡改的非权威目录错误升权。
func TestFinalizeRemoteCIRunAuthorityRejectsNonAuthoritativeCatalog(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, workloadIdentity, receipts := recordWorkloadPassRun(t, store, "finalize-non-authoritative-catalog", 1, "workload-non-authoritative-catalog")
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_workload_catalogs SET authoritative = 0 WHERE catalog_digest = ?`, record.CatalogDigest); err != nil {
		database.Close()
		t.Fatal(err)
	}
	database.Close()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err == nil || (!strings.Contains(err.Error(), "authoritative workload catalog") && !strings.Contains(err.Error(), "does not match its digest")) {
		t.Fatalf("finalize non-authoritative catalog error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, record.JobID, 0)
	assertWorkloadPassLookupMiss(t, store, workloadIdentity)
}

// TestFinalizeRemoteCIRunAuthorityRejectsLegalSelectedCatalog 验证合法 selected catalog 可以写入 provisional，但不能升为权威。
func TestFinalizeRemoteCIRunAuthorityRejectsLegalSelectedCatalog(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	fixture := newSelectedCatalogAuthorityFixture(t, store)
	receipts := completeWorkloadPassReceiptsForCatalog(t, fixture.record, fixture.catalog)
	err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(fixture.record), receipts, nil, true)
	if err == nil || !strings.Contains(err.Error(), "authoritative workload catalog") {
		t.Fatalf("finalize legal selected catalog error = %v", err)
	}
	assertRemoteCIRunAuthoritative(t, store, fixture.record.JobID, false)
	assertRemoteCIRunReceiptCount(t, store, fixture.record.JobID, 0)
	for _, identity := range fixture.identities {
		assertWorkloadPassLookupMiss(t, store, identity)
	}
}

type selectedCatalogAuthorityFixture struct {
	record     RemoteCIRunRecord
	catalog    WorkloadCatalog
	identities []WorkloadPassIdentity
}

func newSelectedCatalogAuthorityFixture(t *testing.T, store *DurationLedgerStore) selectedCatalogAuthorityFixture {
	t.Helper()
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildSelectedTestWorkloadCatalog(plan, WorkloadInventory{
		GoPackages:        []string{"./internal/alpha"},
		FrontendFullTests: []string{"src/alpha.test.ts"},
	})
	if err != nil {
		t.Fatalf("BuildSelectedTestWorkloadCatalog() error = %v", err)
	}
	if catalog.Authoritative {
		t.Fatal("selected workload catalog unexpectedly authoritative")
	}
	now := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	treeSHA := strings.Repeat("e", 40)
	jobID := "finalize-legal-selected-catalog"
	observation := WorkloadCatalogObservation{
		SourceTreeSHA: treeSHA, Entrypoint: CIEntrypointGitPreCommit,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now,
	}
	if err := store.RecordWorkloadCatalog(catalog, observation); err != nil {
		t.Fatalf("record selected workload catalog: %v", err)
	}
	catalogDigest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	shards, executions, results, identities, timings := selectedCatalogRunRecords(t, catalog, jobID, now)
	record := RemoteCIRunRecord{
		JobID: jobID, AgentTokenDigest: digestForWorkloadPass("agent-" + jobID),
		Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast, AcceptedGeneration: 1,
		ImageCacheSnapshotID: "snapshot-1", PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest,
		SourceTreeSHA: treeSHA, CandidateGateSourceSHA256: digestForWorkloadPass("gate-source-" + jobID),
		CandidateGateToolchainSHA256: digestForWorkloadPass("gate-toolchain-" + jobID), RunnerImage: "ubuntu:22.04",
		Status: ResultStatusPassed, CleanupComplete: true, StartedAt: now, CompletedAt: now.Add(2 * time.Minute),
		Shards: shards, WorkloadExecutions: executions, WorkloadResults: results, TimingObservations: timings,
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record selected provisional run: %v", err)
	}
	return selectedCatalogAuthorityFixture{record: record, catalog: catalog, identities: identities}
}

func selectedCatalogRunRecords(
	t *testing.T,
	catalog WorkloadCatalog,
	jobID string,
	now time.Time,
) ([]RemoteCIShardRecord, []PlanGateExecution, []RemoteCIWorkloadResult, []WorkloadPassIdentity, []TimingObservation) {
	t.Helper()
	shards := make([]RemoteCIShardRecord, 0, len(catalog.Workloads))
	executions := make([]PlanGateExecution, 0, len(catalog.Workloads))
	results := make([]RemoteCIWorkloadResult, 0, len(catalog.Workloads))
	identities := make([]WorkloadPassIdentity, 0, len(catalog.Workloads))
	timings := make([]TimingObservation, 0, len(catalog.Workloads))
	for index, workload := range catalog.Workloads {
		shard, execution, result, identity, workloadTimings := selectedCatalogWorkloadRecords(t, workload, jobID, now, index)
		shards = append(shards, shard)
		executions = append(executions, execution)
		results = append(results, result)
		identities = append(identities, identity)
		timings = append(timings, workloadTimings...)
	}
	return shards, executions, results, identities, timings
}

func selectedCatalogWorkloadRecords(
	t *testing.T,
	workload Workload,
	jobID string,
	now time.Time,
	index int,
) (RemoteCIShardRecord, PlanGateExecution, RemoteCIWorkloadResult, WorkloadPassIdentity, []TimingObservation) {
	t.Helper()
	workloadID := GateID(workload.ID)
	shardIdentity := digestForWorkloadPass("shard-" + jobID + strings.Repeat("x", index+1))
	startedAt := now.Add(time.Duration(index+1) * time.Second)
	goFlags, err := WorkloadExecutionGoFlags(workload.ID)
	if err != nil {
		t.Fatalf("derive selected workload GoFlags: %v", err)
	}
	execution := PlanGateExecution{
		ShardIdentity: shardIdentity, GateID: workloadID, Status: ResultStatusPassed,
		StartedAt: startedAt, CompletedAt: startedAt.Add(7 * time.Millisecond),
		ExecutionProfile: ExecutionProfile{
			GoFlags:     goFlags,
			CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured",
			StartupMS: 1, TestBodyMS: 6, TotalMS: 7,
		},
	}
	shard := RemoteCIShardRecord{
		ShardIdentity: shardIdentity, ContainerGroup: fmt.Sprintf("eci-%s-%d", jobID, index),
		ContainerStatus: "Succeeded", Workloads: []GateID{workloadID},
		MaterializationTiming: measuredShardMaterializationTiming(shardIdentity),
		Resources:             RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8},
	}
	identity := WorkloadPassIdentity{
		WorkloadID: workloadID, ExecutionDigest: digestForWorkloadPass("execution-" + string(workloadID)),
		InputDigest:       digestForWorkloadPass("input-" + string(workloadID)),
		EnvironmentDigest: digestForWorkloadPass("environment-" + string(workloadID)),
	}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	result := RemoteCIWorkloadResult{
		Identity: identity, Disposition: WorkloadDispositionExecuted,
		OriginJobID: jobID, OriginAcceptedGeneration: 1,
	}
	return shard, execution, result, identity, workloadTimingObservations(jobID, execution)
}

func workloadTimingObservations(jobID string, execution PlanGateExecution) []TimingObservation {
	observations := authoritativeTimingObservationsForTest(jobID, execution)
	workloads := make([]TimingObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.Scope == cicontract.TimingScopeWorkload {
			workloads = append(workloads, observation)
		}
	}
	return workloads
}

// completeWorkloadPassReceiptsForCatalog 构造精确覆盖 selected catalog 检查子集的合法回执。
func completeWorkloadPassReceiptsForCatalog(t *testing.T, record RemoteCIRunRecord, catalog WorkloadCatalog) []CheckReceiptRecord {
	t.Helper()
	required, err := RequiredChecksForWorkloadCatalog(catalog)
	if err != nil {
		t.Fatalf("required checks for selected catalog: %v", err)
	}
	available := make(map[cicontract.RequiredCheck]CheckReceiptRecord)
	for _, receipt := range testCompleteCheckReceipts(record.JobID, record.SourceTreeSHA, record.StartedAt) {
		available[receipt.RequiredCheck] = receipt
	}
	receipts := make([]CheckReceiptRecord, 0, len(required))
	for index, check := range required {
		receipt, ok := available[check]
		if !ok {
			t.Fatalf("selected catalog required check %q has no fixture receipt", check)
		}
		receipt.RunID, receipt.JobID = record.JobID, record.JobID
		receipt.CandidateTreeSHA, receipt.AgentTokenDigest = record.SourceTreeSHA, record.AgentTokenDigest
		receipt.AcceptedGeneration, receipt.AcceptedSnapshotID = record.AcceptedGeneration, record.ImageCacheSnapshotID
		receipt.StartedAt = record.StartedAt.Add(time.Duration(index) * time.Second)
		receipt.CompletedAt, receipt.Duration = receipt.StartedAt.Add(time.Second), time.Second
		receipt.ReceiptSHA256, err = CheckReceiptSHA256(receipt)
		if err != nil {
			t.Fatalf("hash selected catalog receipt: %v", err)
		}
		receipts = append(receipts, receipt)
	}
	return receipts
}
