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
		AcceptedGeneration: 1, JobID: "job-partial-eci-create", Entrypoint: gate.CIEntrypointGitPreCommit,
		Profile: gate.ProfileLocalFast, PlanDigest: "sha256:plan",
		CatalogDigest: testDurationDigest("e"), SourceTreeSHA: strings.Repeat("f", 40),
		CandidateGateSourceSHA256: "sha256:" + strings.Repeat("b", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
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
	catalog := gate.WorkloadCatalog{
		Version: 1, Authoritative: true,
		Workloads: []gate.Workload{{
			ID: "guard:passed", Kind: gate.WorkloadKindGuard,
			CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000, Shardable: true,
		}},
	}
	recordPartialResultsCatalog(t, ledgerStore, &result, catalog, started)

	if err := recordRemoteCIRun(ledgerStore, result, runErr); err == nil || !strings.Contains(err.Error(), "shard timing") {
		t.Fatalf("recordRemoteCIRun() error = %v, want missing producer timing", err)
	}
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
	seedRemoteCITestAcceptedGeneration(t, ledgerStore, 1)
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
		SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile, AcceptedGeneration: result.AcceptedGeneration, ObservedAt: observedAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateRemoteReportsPreservesExactGoTestWorkloadExecutionProfile(t *testing.T) {
	started := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	workloadID := string(gate.GateIDBackendTestWithGuard)
	profile := gate.ExecutionProfile{
		CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured",
		CacheMissCount: 1, StartupMS: 10, TestBodyMS: 20, TotalMS: 30,
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
	assertAggregateRemoteReportsIdentity(t, parents, workloads, status)
	assertRemoteExecutionProfile(t, "workload", workloads[0].ExecutionProfile, profile)
	assertRemoteExecutionProfile(t, "parent aggregate", parents[0].ExecutionProfile, profile)
}

func assertAggregateRemoteReportsIdentity(t *testing.T, parents []gate.PlanGateExecution, workloads []gate.PlanGateExecution, status gate.ResultStatus) {
	t.Helper()
	if status != gate.ResultStatusPassed || len(parents) != 1 || len(workloads) != 1 {
		t.Fatalf("aggregateRemoteReports() parents=%#v workloads=%#v status=%q", parents, workloads, status)
	}
}

func assertRemoteExecutionProfile(t *testing.T, label string, got gate.ExecutionProfile, want gate.ExecutionProfile) {
	t.Helper()
	if got.CacheSource != want.CacheSource || got.CacheStatus != want.CacheStatus || got.CacheMeasurement != want.CacheMeasurement ||
		got.StartupMS != want.StartupMS || got.TestBodyMS != want.TestBodyMS || got.TotalMS != want.TotalMS {
		t.Fatalf("%s execution profile = %#v, want %#v", label, got, want)
	}
}

func TestRemoteWorkloadExecutionProfileIsStrict(t *testing.T) {
	started := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	exact, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	guard := gate.Workload{ID: "guard:profile", Kind: gate.WorkloadKindGuard, Shardable: true}

	t.Run("legacy report schema is rejected", func(t *testing.T) {
		assertRemoteWorkloadProfileRejected(t, exact, "shard-legacy", 1, "report schema is unsupported", started)
	})

	t.Run("current guard requires structured profile", func(t *testing.T) {
		assertRemoteWorkloadProfileRejected(t, guard, "shard-current", gate.ExecutorPlanReportSchemaVersion, "execution profile cache source is invalid", started)
	})

	t.Run("current exact workload requires structured profile", func(t *testing.T) {
		assertRemoteWorkloadProfileRejected(t, exact, "shard-current", gate.ExecutorPlanReportSchemaVersion, "execution profile cache source is invalid", started)
	})
}

func assertRemoteWorkloadProfileRejected(t *testing.T, workload gate.Workload, shardID string, schema uint32, want string, started time.Time) {
	t.Helper()
	_, err := remoteFreshWorkloadExecutions([]gate.Workload{workload}, []ShardResult{{
		ShardIdentity: shardID,
		Report: gate.PlanExecutionReport{
			SchemaVersion: schema,
			Gates:         []gate.PlanGateExecution{zeroRemoteWorkloadProfileExecution(started, workload.ID)},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected workload profile rejection containing %q, got %v", want, err)
	}
}

func zeroRemoteWorkloadProfileExecution(started time.Time, id string) gate.PlanGateExecution {
	return gate.PlanGateExecution{
		GateID:      gate.GateID(id),
		Status:      gate.ResultStatusPassed,
		ExitCode:    0,
		StartedAt:   started,
		CompletedAt: started.Add(time.Millisecond),
	}
}

func testDurationDigest(seed string) string {
	return "sha256:" + strings.Repeat(seed, 64)
}
