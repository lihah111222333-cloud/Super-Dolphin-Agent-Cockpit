package remoteci

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteDurationSamplesPreserveSuccessfulShardsWhenBatchIsIncomplete(t *testing.T) {
	started := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	catalog := gate.WorkloadCatalog{Workloads: []gate.Workload{
		{ID: "guard:passed", Kind: gate.WorkloadKindGuard, CommandDigest: testDurationDigest("a")},
		{ID: "guard:missing", Kind: gate.WorkloadKindGuard, CommandDigest: testDurationDigest("b")},
	}}
	shards := []ShardResult{
		{
			ExecutedWorkloads: []gate.GateID{"guard:passed"},
			Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{
				GateID: "guard:passed", Status: gate.ResultStatusPassed, ExitCode: 0,
				StartedAt: started, CompletedAt: started.Add(25 * time.Millisecond),
			}}},
		},
		{ExecutedWorkloads: []gate.GateID{"guard:missing"}},
	}
	input := RunInput{
		Platform: "linux/amd64", RunnerIdentityDigest: testDurationDigest("c"),
		ToolchainDigest: testDurationDigest("d"),
	}

	samples, err := remoteDurationSamples(catalog, shards, input)
	if err == nil || !strings.Contains(err.Error(), "duration execution coverage is incomplete") {
		t.Fatalf("remoteDurationSamples() error = %v", err)
	}
	if len(samples) != 1 || samples[0].Bucket.WorkloadID != "guard:passed" ||
		!samples[0].Succeeded || samples[0].DurationMS != 25 {
		t.Fatalf("remoteDurationSamples() = %#v", samples)
	}
}

func TestRecordRemoteCIRunFiltersUncreatedShardPlaceholder(t *testing.T) {
	ledgerStore := newPartialResultsLedgerStore(t)
	started := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	runErr := errors.New("ECI create request failed after shard-000 was accepted")
	result := RunResult{
		JobID: "job-partial-eci-create", Entrypoint: gate.CIEntrypointGitPreCommit,
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: testDurationDigest("e"), SourceTreeSHA: strings.Repeat("f", 40),
		RunnerImage: "ubuntu:22.04", Status: gate.ResultStatusFailed, Authoritative: true,
		StartedAt: started, CompletedAt: started.Add(time.Second),
		Shards: []ShardResult{
			{
				ShardIdentity: "sha256:" + strings.Repeat("a", 64), ContainerGroup: "eci-created",
				ExecutedWorkloads:     []gate.GateID{"guard:passed"},
				MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: "sha256:" + strings.Repeat("a", 64)},
			},
			{},
		},
		WorkloadExecutions: []gate.PlanGateExecution{{
			GateID: "guard:passed", Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: started, CompletedAt: started.Add(30 * time.Millisecond),
			ExecutionProfile: gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 10, TestBodyMS: 20, TotalMS: 30},
		}},
	}
	var err error
	result.CandidateTestBinaryReceiptBindingDigest, err = CandidateTestBinaryReceiptBindingDigestFromBuilds(nil, result.SourceTreeSHA)
	if err != nil {
		t.Fatal(err)
	}
	catalog := gate.WorkloadCatalog{
		Version: 1, Authoritative: true,
		Workloads: []gate.Workload{{
			ID: "guard:passed", Kind: gate.WorkloadKindGuard,
			CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000, Shardable: true,
		}},
	}
	recordPartialResultsCatalog(t, ledgerStore, &result, catalog, started)

	if err := recordRemoteCIRun(ledgerStore, result, runErr); err != nil {
		t.Fatal(err)
	}
	assertRecordedPartialResultsRun(t, ledgerStore, result.JobID, runErr)
}

func newPartialResultsLedgerStore(t *testing.T) *gate.DurationLedgerStore {
	t.Helper()
	ledgerStore, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerStore.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	return ledgerStore
}

func recordPartialResultsCatalog(t *testing.T, ledgerStore *gate.DurationLedgerStore, result *RunResult, catalog gate.WorkloadCatalog, observedAt time.Time) {
	t.Helper()
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	result.CatalogDigest = catalogDigest
	if err := ledgerStore.RecordWorkloadCatalog(catalog, gate.WorkloadCatalogObservation{
		SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile, ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertRecordedPartialResultsRun(t *testing.T, ledgerStore *gate.DurationLedgerStore, jobID string, runErr error) {
	t.Helper()
	recorded, err := ledgerStore.LoadRemoteCIRun(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Status != gate.ResultStatusFailed {
		t.Fatalf("recorded status = %q, want failed", recorded.Status)
	}
	if recorded.ErrorText != runErr.Error() {
		t.Fatalf("recorded error = %q, want %q", recorded.ErrorText, runErr)
	}
	if len(recorded.WorkloadExecutions) != 1 {
		t.Fatalf("recorded workload executions = %#v", recorded.WorkloadExecutions)
	}
	profile := recorded.WorkloadExecutions[0].ExecutionProfile
	if profile.CacheStatus != "miss" || profile.StartupMS != 10 || profile.TestBodyMS != 20 || profile.TotalMS != 30 {
		t.Fatalf("recorded workload execution profile = %#v", profile)
	}
	if len(recorded.Shards) != 1 {
		t.Fatalf("recorded shards = %#v, want only created shard", recorded.Shards)
	}
	shard := recorded.Shards[0]
	if shard.ShardIdentity != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("shard identity = %q", shard.ShardIdentity)
	}
	if shard.ContainerGroup != "eci-created" || shard.ContainerStatus != "Unknown" || len(shard.Workloads) != 1 || shard.Workloads[0] != "guard:passed" {
		t.Fatalf("recorded created shard = %#v", shard)
	}
}

func TestAggregateRemoteReportsPreservesExactGoTestWorkloadExecutionProfile(t *testing.T) {
	started := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	workloadID := string(gate.GateIDBackendTestWithGuard)
	profile := gate.ExecutionProfile{
		CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured",
		StartupMS: 10, TestBodyMS: 20, TotalMS: 30,
	}
	catalog := gate.WorkloadCatalog{Workloads: []gate.Workload{{
		ID: workloadID, Kind: gate.WorkloadKindGoTest, Shardable: true,
	}}}
	observed := map[string]gate.PlanGateExecution{workloadID: {
		GateID: gate.GateID(workloadID), Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: started, CompletedAt: started.Add(30 * time.Millisecond), ExecutionProfile: profile,
	}}
	parents, workloads, status, err := aggregateRemoteReports(catalog, observed, []ShardResult{{ContainerStatus: "Succeeded"}})
	if err != nil {
		t.Fatal(err)
	}
	if status != gate.ResultStatusPassed || len(parents) != 1 || len(workloads) != 1 {
		t.Fatalf("aggregateRemoteReports() parents=%#v workloads=%#v status=%q", parents, workloads, status)
	}
	if workloads[0].ExecutionProfile.CacheSource != profile.CacheSource ||
		workloads[0].ExecutionProfile.CacheStatus != profile.CacheStatus ||
		workloads[0].ExecutionProfile.CacheMeasurement != profile.CacheMeasurement ||
		workloads[0].ExecutionProfile.StartupMS != profile.StartupMS ||
		workloads[0].ExecutionProfile.TestBodyMS != profile.TestBodyMS ||
		workloads[0].ExecutionProfile.TotalMS != profile.TotalMS {
		t.Fatalf("workload execution profile = %#v, want %#v", workloads[0].ExecutionProfile, profile)
	}
	if parents[0].ExecutionProfile.TestBodyMS != 0 {
		t.Fatalf("parent aggregate must not replace workload timing ledger: %#v", parents[0].ExecutionProfile)
	}
}

func TestRemoteWorkloadExecutionProfileNormalization(t *testing.T) {
	started := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	exact, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	exactID := exact.ID
	guard := gate.Workload{ID: "guard:profile", Kind: gate.WorkloadKindGuard, Shardable: true}
	zero := func(id string) gate.PlanGateExecution {
		return gate.PlanGateExecution{
			GateID: gate.GateID(id), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: started, CompletedAt: started.Add(time.Millisecond),
		}
	}

	t.Run("legacy v1 exact profile is made explicit", func(t *testing.T) {
		fresh, err := remoteFreshWorkloadExecutions([]gate.Workload{exact}, []ShardResult{{Report: gate.PlanExecutionReport{SchemaVersion: 1, Gates: []gate.PlanGateExecution{zero(exactID)}}}})
		if err != nil {
			t.Fatal(err)
		}
		profile := fresh[exactID].ExecutionProfile
		if profile.CacheSource != "none" || profile.CacheStatus != "not_applicable" || profile.CacheMeasurement != "not_measured" {
			t.Fatalf("legacy profile = %#v", profile)
		}
	})

	t.Run("v2 guard profile is made explicit", func(t *testing.T) {
		fresh, err := remoteFreshWorkloadExecutions([]gate.Workload{guard}, []ShardResult{{Report: gate.PlanExecutionReport{SchemaVersion: 2, Gates: []gate.PlanGateExecution{zero(guard.ID)}}}})
		if err != nil {
			t.Fatal(err)
		}
		catalog := gate.WorkloadCatalog{Workloads: []gate.Workload{guard}}
		_, workloads, _, err := aggregateRemoteReports(catalog, fresh, []ShardResult{{ContainerStatus: "Succeeded"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(workloads) != 1 || workloads[0].ExecutionProfile.CacheMeasurement != "not_measured" {
			t.Fatalf("guard workloads = %#v", workloads)
		}
	})

	t.Run("v4 exact zero profile remains invalid", func(t *testing.T) {
		_, err := remoteFreshWorkloadExecutions([]gate.Workload{exact}, []ShardResult{{Report: gate.PlanExecutionReport{SchemaVersion: 4, Gates: []gate.PlanGateExecution{zero(exactID)}}}})
		if err == nil || !strings.Contains(err.Error(), "execution profile cache source is invalid") {
			t.Fatalf("remoteFreshWorkloadExecutions() error = %v", err)
		}
	})
}

func testDurationDigest(seed string) string {
	return "sha256:" + strings.Repeat(seed, 64)
}
