package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestNewDurationLedgerStoreRequiresAbsoluteCanonicalParent(t *testing.T) {
	if _, err := NewDurationLedgerStore("duration-ledger.sqlite"); err == nil {
		t.Fatal("relative duration ledger path was accepted")
	}
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "ledger-parent")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	store, err := NewDurationLedgerStore(filepath.Join(alias, "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "duration-ledger.sqlite")
	if store.AuthorityPath() != want {
		t.Fatalf("authority path = %q, want %q", store.AuthorityPath(), want)
	}
}

func TestDurationLedgerSQLiteRejectsZeroAcceptedGenerationForPlanningIndex(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	sample := testDurationSample("zero-generation", testWorkloadDigest, true, 10)
	statementSQL, err := sqliteDurationSampleBatchSQL(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(statementSQL, sqliteDurationSampleArguments(0, sample)...); err == nil || !strings.Contains(err.Error(), "accepted_generation") {
		t.Fatalf("zero accepted generation insert error = %v, want schema rejection", err)
	}
}

func TestDurationLedgerSQLiteCompactsLargeRetentionScopeWithoutVariableOverflow(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, testDurationLedger(1)); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	const sampleCount = 7_000
	samples := make([]DurationSample, sampleCount)
	for index := range samples {
		samples[index] = testDurationSample(
			fmt.Sprintf("workload-%05d", index),
			testWorkloadDigest,
			true,
			int64(index+1),
		)
	}
	generation, err := store.AppendSamplesFast(1, samples)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 2 {
		t.Fatalf("generation = %d, want 2", generation)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var retainedRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM duration_samples WHERE accepted_generation = ?`, "1").Scan(&retainedRows); err != nil {
		t.Fatal(err)
	}
	if retainedRows != sampleCount+1 {
		t.Fatalf("retained duration sample rows = %d, want %d", retainedRows, sampleCount+1)
	}
	assertSQLiteQueryPlan(
		t,
		database,
		`SELECT accepted_generation FROM duration_samples WHERE accepted_generation = ?`,
		[]string{"USING COVERING INDEX idx_duration_samples_retention"},
		[]string{"duration_samples"},
		"1",
	)
}

func TestDurationLedgerSQLiteCatalogProjectionPreservesOrderAndObservations(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	catalog := WorkloadCatalog{
		Version:       durationLedgerVersion,
		Authoritative: true,
		Workloads: []Workload{
			{
				ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard,
				CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 2_000,
				Shardable: true,
			},
			{
				ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
				CommandDigest: strings.Repeat("2", 64), BootstrapEstimateMS: 1_000,
				Shardable: true,
			},
		},
	}
	first := WorkloadCatalogObservation{
		SourceTreeSHA:      strings.Repeat("3", 40),
		Entrypoint:         CIEntrypointGitPreCommit,
		Profile:            ProfileLocalFast,
		AcceptedGeneration: 1,
		ObservedAt:         time.Now().UTC().Truncate(time.Millisecond),
	}
	second := first
	second.SourceTreeSHA = strings.Repeat("4", 40)
	second.ObservedAt = first.ObservedAt.Add(time.Second)
	if err := store.RecordWorkloadCatalog(catalog, first); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, second); err != nil {
		t.Fatal(err)
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.LoadWorkloadCatalogRecord(digest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(record.Catalog, catalog) {
		t.Fatalf("catalog = %#v, want %#v", record.Catalog, catalog)
	}
	if !reflect.DeepEqual(record.Observations, []WorkloadCatalogObservation{second, first}) {
		t.Fatalf("observations = %#v", record.Observations)
	}
}

func TestCompareAndSwapCalibrationPreservesSQLiteSamples(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	first, err := store.CompareAndSwap(0, testDurationLedger(101))
	if err != nil {
		t.Fatal(err)
	}
	calibration := testSQLiteDurationCalibration()
	updated, err := store.CompareAndSwapCalibration(first.Generation, &calibration)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Generation != first.Generation+1 {
		t.Fatalf("generation = %d", updated.Generation)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Ledger.Samples) != 1 ||
		loaded.Ledger.Samples[0].DurationMS != 101 ||
		!reflect.DeepEqual(loaded.Ledger.Calibration, calibrationAtMillisecond(&calibration)) {
		t.Fatalf("loaded ledger = %#v", loaded.Ledger)
	}
}

func TestDurationLedgerSQLiteShardOverheadRoundTripPreservesAccountedIntervals(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	durationLedgerSQLiteAssertShardOverheadRoundTrip(t, store, sample, overhead)
}

// durationLedgerSQLiteShardOverheadRoundTripSample 构造保留间隔往返测试样本。
func durationLedgerSQLiteShardOverheadRoundTripSample() ShardOrchestrationOverheadSample {
	digest := "sha256:" + strings.Repeat("a", 64)
	start := time.UnixMilli(1_000).UTC()
	return ShardOrchestrationOverheadSample{
		AcceptedGeneration:     1,
		ProvenanceDigest:       digest,
		JobID:                  "overhead-roundtrip-job",
		ShardIdentity:          "overhead-roundtrip-shard",
		TotalStartedAt:         start,
		TotalCompletedAt:       start.Add(2 * time.Second),
		WorkloadEnvelopeStart:  start.Add(500 * time.Millisecond),
		WorkloadEnvelopeEnd:    start.Add(1500 * time.Millisecond),
		AccountedDurationMS:    1_000,
		AccountedIntervalCount: 1,
		OverheadMS:             1_000,
	}
}

// durationLedgerSQLiteShardOverheadRoundTripValue 构造与样本绑定的账本开销值。
func durationLedgerSQLiteShardOverheadRoundTripValue(digest string) ShardOrchestrationOverhead {
	return ShardOrchestrationOverhead{
		SchemaVersion:                ShardOrchestrationOverheadSchemaVersion,
		PolicyVersion:                ShardOverheadPolicyVersion,
		Platform:                     "linux/amd64",
		Runner:                       "eci",
		Toolchain:                    "go",
		CalibrationResourceClassID:   "calibration",
		CalibrationResourceCPU:       4,
		CalibrationResourceMemoryGiB: 8,
		P95MS:                        1_000,
		SampleCount:                  1,
		ProvenanceDigest:             digest,
		AcceptedGeneration:           1,
		AcceptedSnapshotID:           "snapshot-1",
	}
}

// durationLedgerSQLiteRecordShardOverheadRoundTrip 写入开销样本并保留同一事务语义。
func durationLedgerSQLiteRecordShardOverheadRoundTrip(t *testing.T, store *DurationLedgerStore, sample ShardOrchestrationOverheadSample, overhead ShardOrchestrationOverhead) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	insertGenerationOneRunForTest(t, database, sample.JobID)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapShardOverhead(1, overhead, []ShardOrchestrationOverheadSample{sample}); err != nil {
		t.Fatal(err)
	}
}

// durationLedgerSQLiteAssertShardOverheadRoundTrip 校验开销值和原始样本均可恢复。
func durationLedgerSQLiteAssertShardOverheadRoundTrip(t *testing.T, store *DurationLedgerStore, sample ShardOrchestrationOverheadSample, overhead ShardOrchestrationOverhead) {
	t.Helper()
	planning := PlanningContext{
		Platform:           overhead.Platform,
		Runner:             overhead.Runner,
		Toolchain:          overhead.Toolchain,
		TargetDurationMS:   FullCITargetDurationMS,
		AcceptedSnapshotID: overhead.AcceptedSnapshotID,
	}
	loaded, err := store.LoadPlanning(planning)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Ledger.ShardOverhead == nil || !reflect.DeepEqual(*loaded.Ledger.ShardOverhead, overhead) {
		t.Fatalf("loaded shard overhead = %#v, want %#v", loaded.Ledger.ShardOverhead, overhead)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	gotSamples, err := loadSQLiteShardOverheadSamples(database, "1", sample.ProvenanceDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotSamples, []ShardOrchestrationOverheadSample{sample}) {
		t.Fatalf("loaded shard overhead samples = %#v, want %#v", gotSamples, []ShardOrchestrationOverheadSample{sample})
	}
}

func TestRecordRemoteCIRunRejectsUncoveredPassedCatalogWorkload(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: []Workload{{
		ID: string(GateIDAIMaintenanceSelfTest), Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA: strings.Repeat("b", 40), Entrypoint: CIEntrypointGitPreCommit,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	err = store.RecordProvisionalRemoteCIRun(RemoteCIRunRecord{
		JobID: "uncovered", AgentTokenDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast,
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1",
		PlanDigest: "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("b", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("1", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("2", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: false,
		StartedAt: now, CompletedAt: now,
		TimingObservations: []TimingObservation{authoritativeRunTimingObservation("uncovered")},
	})
	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("passed uncovered catalog error = %v", err)
	}
}

// TestRecordRemoteCIRunRejectsAuthoritativeRunWithoutTimingObservations keeps the
// authority boundary at the direct SQLite store entrypoint.
func TestRecordRemoteCIRunRejectsAuthoritativeRunWithoutTimingObservations(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := store.RecordProvisionalRemoteCIRun(RemoteCIRunRecord{
		JobID: "authoritative-missing-timing", AgentTokenDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entrypoint: CIEntrypointGitPreCommit, Profile: ProfileLocalFast,
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1",
		PlanDigest: "sha256:plan", CatalogDigest: "sha256:" + strings.Repeat("a", 64), SourceTreeSHA: strings.Repeat("a", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusFailed, Authoritative: true, StartedAt: now, CompletedAt: now,
	})
	if err == nil || !strings.Contains(err.Error(), "provisional remote CI run must not be authoritative") {
		t.Fatalf("authoritative empty timing observations error = %v", err)
	}
}

func TestRecordRemoteCIRunRejectsUnknownStatusAndFailedCatalogDrift(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	catalog := WorkloadCatalog{Version: durationLedgerVersion, Workloads: []Workload{{
		ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
		CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA: strings.Repeat("b", 40), Entrypoint: CIEntrypointManualCLI,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "failed-catalog-drift", AgentTokenDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1",
		PlanDigest: "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("b", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("3", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("4", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusFailed, StartedAt: now, CompletedAt: now,
		Shards: []RemoteCIShardRecord{{
			ShardIdentity: "sha256:" + strings.Repeat("5", 64), ContainerGroup: "eci-failed", ContainerStatus: "Failed",
			Workloads:             []GateID{GateIDAIMaintenanceSelfTest},
			MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("5", 64)),
		}},
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "absent from its catalog") {
		t.Fatalf("failed run workload catalog error = %v", err)
	}
	record.Shards = []RemoteCIShardRecord{{
		ShardIdentity: "sha256:" + strings.Repeat("6", 64), ContainerGroup: "eci-123", ContainerStatus: "Failed",
		Workloads:             []GateID{GateIDAIMaintenanceSelfTest},
		MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("6", 64)),
	}}
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "absent from its catalog") {
		t.Fatalf("failed run shard workload catalog error = %v", err)
	}
	record.Shards = nil
	record.Status = ResultStatus("unknown")
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown remote CI run status error = %v", err)
	}
}

func TestRecordRemoteCIRunAcceptsPassedManualSelectionCatalog(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest := durationLedgerSQLiteRecordManualSelectionCatalog(t, store, now)
	record := durationLedgerSQLiteManualSelectionRecord(now, digest)
	execution := durationLedgerSQLitePopulateManualSelectionRecord(t, &record, now)
	durationLedgerSQLiteAssertManualSelectionRecord(t, store, record)
	durationLedgerSQLiteAssertManualSelectionAuthorityRejections(t, store, record, execution)
}

// durationLedgerSQLiteRecordManualSelectionCatalog 写入手工选择目录并返回其摘要。
func durationLedgerSQLiteRecordManualSelectionCatalog(t *testing.T, store *DurationLedgerStore, observedAt time.Time) string {
	t.Helper()
	catalog := WorkloadCatalog{
		Version: durationLedgerVersion, Authoritative: false,
		Workloads: []Workload{{
			ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
			CommandDigest: strings.Repeat("c", 64), BootstrapEstimateMS: 1_000,
			Shardable: true,
		}},
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA: strings.Repeat("d", 40), Entrypoint: CIEntrypointManualCLI,
		Profile: ProfileLocalFast, AcceptedGeneration: 1, ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
	return digest
}

// durationLedgerSQLiteManualSelectionRecord 构造手工选择目录对应的远程运行记录。
func durationLedgerSQLiteManualSelectionRecord(now time.Time, digest string) RemoteCIRunRecord {
	return RemoteCIRunRecord{
		JobID: "manual-selection", AgentTokenDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1",
		PlanDigest: "sha256:plan", CatalogDigest: digest, SourceTreeSHA: strings.Repeat("d", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("5", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("6", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed, Authoritative: false,
		StartedAt: now, CompletedAt: now, CleanupComplete: true,
		TimingObservations: []TimingObservation{authoritativeRunTimingObservation("manual-selection")},
		Shards: []RemoteCIShardRecord{{
			ShardIdentity: "sha256:" + strings.Repeat("7", 64), ContainerGroup: "eci-manual", ContainerStatus: "Succeeded",
			Workloads:             []GateID{GateIDWhitespaceCheck},
			MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("7", 64)),
			Resources:             RemoteCIShardResources{ClassID: "medium", CPU: 4, MemoryGiB: 8},
		}},
	}
}

// durationLedgerSQLitePopulateManualSelectionRecord 补齐运行记录的执行结果和计时证据。
func durationLedgerSQLitePopulateManualSelectionRecord(t *testing.T, record *RemoteCIRunRecord, now time.Time) PlanGateExecution {
	t.Helper()
	execution := PlanGateExecution{ShardIdentity: record.Shards[0].ShardIdentity, GateID: GateIDWhitespaceCheck, StartedAt: now.Add(3 * time.Millisecond), CompletedAt: now.Add(10 * time.Millisecond), ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
	record.WorkloadExecutions = []PlanGateExecution{execution}
	record.WorkloadResults = []RemoteCIWorkloadResult{executedWorkloadResultForCatalogTest(t, GateIDWhitespaceCheck, record.JobID)}
	record.TimingObservations = authoritativeTimingObservationsForTest(record.JobID, execution)
	return execution
}

// durationLedgerSQLiteAssertManualSelectionRecord 校验手工选择目录记录可写入并保留资源。
func durationLedgerSQLiteAssertManualSelectionRecord(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord) {
	t.Helper()
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record passed manual selection: %v", err)
	}
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatalf("load normal-resource remote CI run: %v", err)
	}
	if got, want := loaded.Shards[0].Resources, record.Shards[0].Resources; got != want {
		t.Fatalf("loaded normal resources = %#v, want %#v", got, want)
	}
}

// durationLedgerSQLiteAssertManualSelectionAuthorityRejections 保留两类权威性拒绝断言。
func durationLedgerSQLiteAssertManualSelectionAuthorityRejections(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, execution PlanGateExecution) {
	t.Helper()
	record.TimingObservations = authoritativeTimingObservationsForTest(record.JobID, execution)
	record.JobID = "authoritative-selection"
	record.Entrypoint = CIEntrypointGitPreCommit
	record.Authoritative = true
	for index := range record.TimingObservations {
		record.TimingObservations[index].JobID = record.JobID
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil ||
		!strings.Contains(err.Error(), "provisional remote CI run must not be authoritative") {
		t.Fatalf("authoritative run with selection catalog error = %v", err)
	}
	record.JobID = "manual-authority-mismatch"
	record.Entrypoint = CIEntrypointManualCLI
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil ||
		!strings.Contains(err.Error(), "provisional remote CI run must not be authoritative") {
		t.Fatalf("manual run authority mismatch error = %v", err)
	}
}

func TestRecordRemoteCIRunRejectsPassedShardCoverageDrift(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	catalog := WorkloadCatalog{
		Version: durationLedgerVersion, Authoritative: true,
		Workloads: []Workload{{
			ID: string(GateIDWhitespaceCheck), Kind: WorkloadKindGuard,
			CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000,
			Shardable: true,
		}},
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.RecordWorkloadCatalog(catalog, WorkloadCatalogObservation{
		SourceTreeSHA:      strings.Repeat("2", 40),
		Entrypoint:         CIEntrypointGitPreCommit,
		Profile:            ProfileLocalFast,
		AcceptedGeneration: 1,
		ObservedAt:         now,
	}); err != nil {
		t.Fatal(err)
	}
	digest, err := WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record := RemoteCIRunRecord{
		JobID: "job-shard-drift", AgentTokenDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entrypoint: CIEntrypointGitPreCommit,
		AcceptedGeneration: 1, ImageCacheSnapshotID: "snapshot-1",
		Profile: ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: digest, SourceTreeSHA: strings.Repeat("2", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("7", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("8", 64),
		RunnerImage: "ubuntu:22.04", Status: ResultStatusPassed,
		Authoritative: false, StartedAt: now, CompletedAt: now.Add(time.Second),
		CleanupComplete: true,
	}
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil {
		t.Fatal("passed workload without a shard was accepted")
	}
	record.Shards = []RemoteCIShardRecord{{
		ShardIdentity: "sha256:" + strings.Repeat("9", 64), ContainerGroup: "eci-executed",
		ContainerStatus: "Succeeded", Workloads: []GateID{GateIDWhitespaceCheck},
		MaterializationTiming: measuredShardMaterializationTiming("sha256:" + strings.Repeat("9", 64)),
		Resources:             RemoteCIShardResources{ClassID: "fixed", CPU: 4, MemoryGiB: 8},
	}}
	execution := PlanGateExecution{ShardIdentity: record.Shards[0].ShardIdentity, GateID: GateIDWhitespaceCheck, StartedAt: now.Add(3 * time.Millisecond), CompletedAt: now.Add(10 * time.Millisecond), ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 6, TotalMS: 7}}
	record.WorkloadExecutions = []PlanGateExecution{execution}
	record.WorkloadResults = []RemoteCIWorkloadResult{executedWorkloadResultForCatalogTest(t, GateIDWhitespaceCheck, record.JobID)}
	record.TimingObservations = authoritativeTimingObservationsForTest(record.JobID, execution)
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("passed executed workload rejected: %v", err)
	}
	record.JobID = "job-catalog-external"
	record.Shards[0].Workloads = []GateID{"catalog-external"}
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil {
		t.Fatal("passed catalog-external workload was accepted")
	}
}

func authoritativeRunTimingObservation(jobID string) TimingObservation {
	startedAt := time.UnixMilli(100).UTC()
	return TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeRun, Phase: cicontract.TimingTotal, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond), DurationMS: 1, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationCriticalPath, CacheEvidence: NewNotApplicableCacheEvidence("run_has_no_workload_cache")}
}

// executedWorkloadResultForCatalogTest 构造与本次 job/generation 绑定的 fresh workload result。
func executedWorkloadResultForCatalogTest(t *testing.T, workloadID GateID, jobID string) RemoteCIWorkloadResult {
	t.Helper()
	identity := WorkloadPassIdentity{WorkloadID: workloadID, ExecutionDigest: digestForWorkloadPass("execution-" + string(workloadID)), InputDigest: digestForWorkloadPass("input-" + string(workloadID)), EnvironmentDigest: digestForWorkloadPass("environment-" + string(workloadID))}
	identity.IdentityDigest = workloadPassIdentityDigest(t, identity)
	return RemoteCIWorkloadResult{Identity: identity, Disposition: WorkloadDispositionExecuted, OriginJobID: jobID, OriginAcceptedGeneration: 1}
}

func calibrationAtMillisecond(calibration *DurationCalibration) *DurationCalibration {
	copy := *calibration
	copy.CompletedAt = copy.CompletedAt.UTC().Truncate(time.Millisecond)
	return &copy
}

func measuredShardMaterializationTiming(shardIdentity string) ShardMaterializationTiming {
	return ShardMaterializationTiming{
		Measurement:   MaterializationMeasurementMeasured,
		ShardIdentity: shardIdentity,
		Source: MaterializationPhaseTiming{
			DownloadMS: 5, VerifyMS: 3, InstallMS: 2, MaterializeMS: 10,
		},
		Baseline: MaterializationPhaseTiming{
			DownloadMS: 7, VerifyMS: 4, InstallMS: 3, MaterializeMS: 14,
		},
		CandidateCompile: MaterializationPhaseTiming{
			DownloadMS: 11, VerifyMS: 6, InstallMS: 4, MaterializeMS: 21,
		},
		CandidateTestBinaries: MaterializationPhaseTiming{
			DownloadMS: 13, VerifyMS: 7, InstallMS: 5, MaterializeMS: 25,
		},
	}
}

func testSQLiteDurationCalibration() DurationCalibration {
	return DurationCalibration{
		SchemaVersion:              DurationCalibrationSchemaVersion,
		Commit:                     strings.Repeat("1", 40),
		Tree:                       strings.Repeat("2", 40),
		Platform:                   "linux/amd64",
		Runner:                     "runner-v1",
		Toolchain:                  RequiredGoToolchain,
		CommitEntrypoint:           CIEntrypointGitPreCommit,
		PushEntrypoint:             CIEntrypointGitPrePush,
		ReleaseEntrypoint:          CIEntrypointRelease,
		CommitCatalogDigest:        "sha256:" + strings.Repeat("3", 64),
		PushCatalogDigest:          "sha256:" + strings.Repeat("4", 64),
		ReleaseCatalogDigest:       "sha256:" + strings.Repeat("5", 64),
		CalibrationResourceClassID: "calibration", CalibrationResourceCPU: 4, CalibrationResourceMemoryGiB: 8,
		WorkloadCount:      1,
		RacePackageCount:   1,
		AcceptedSnapshotID: "snapshot-test",
		CompletedAt:        time.Now().UTC(),
	}
}
