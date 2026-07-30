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
				ShardIdentity: "shard-000", ContainerGroup: "eci-created",
				ExecutedWorkloads: []gate.GateID{"guard:passed"},
			},
			{},
		},
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
	ledgerStore, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.json"))
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
	if len(recorded.Shards) != 1 {
		t.Fatalf("recorded shards = %#v, want only created shard", recorded.Shards)
	}
	shard := recorded.Shards[0]
	if shard.ShardIdentity != "shard-000" {
		t.Fatalf("shard identity = %q", shard.ShardIdentity)
	}
	if shard.ContainerGroup != "eci-created" || shard.ContainerStatus != "Unknown" || len(shard.Workloads) != 1 || shard.Workloads[0] != "guard:passed" {
		t.Fatalf("recorded created shard = %#v", shard)
	}
}

func testDurationDigest(seed string) string {
	return "sha256:" + strings.Repeat(seed, 64)
}
