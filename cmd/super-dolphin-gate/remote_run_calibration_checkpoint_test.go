package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRemoteRunResultFromLedgerRecordRehydratesSQLiteProjection(t *testing.T) {
	ledgerStore, _, input, _ := remoteCalibrationCheckpointFixture(t)
	catalog, _, err := remoteCalibrationCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := ledgerStore.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{SourceTreeSHA: input.Tree, Entrypoint: input.Entrypoint, Profile: input.Profile, ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	workloadID := gatecontract.GateID(catalog.Workloads[0].ID)
	shardIdentity := "sha256:" + strings.Repeat("a", 64)
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record := gatecontract.RemoteCIRunRecord{JobID: "job-rehydration", Entrypoint: input.Entrypoint, Profile: input.Profile, PlanDigest: "sha256:plan", CatalogDigest: catalogDigest, SourceTreeSHA: input.Source.SourceTreeSHA, RunnerImage: input.RunnerImage, Status: gatecontract.ResultStatusFailed, Authoritative: true, StartedAt: now, CompletedAt: now.Add(time.Second), CleanupComplete: true, Shards: []gatecontract.RemoteCIShardRecord{{ShardIdentity: shardIdentity, ContainerGroup: "group-1", ContainerStatus: "Succeeded", Workloads: []gatecontract.GateID{workloadID}, MaterializationTiming: gatecontract.ShardMaterializationTiming{Measurement: gatecontract.MaterializationMeasurementMeasured, ShardIdentity: shardIdentity, Source: gatecontract.MaterializationPhaseTiming{DownloadMS: 1, VerifyMS: 1, InstallMS: 1, MaterializeMS: 3}, Baseline: gatecontract.MaterializationPhaseTiming{MaterializeMS: 1}, CandidateCompile: gatecontract.MaterializationPhaseTiming{MaterializeMS: 1}}}}, CacheMisses: []gatecontract.GateID{workloadID}, Warnings: []string{"projection warning"}, PhaseTimings: []gatecontract.RemoteCIPhaseTiming{{Phase: "result.merge", StartedAt: now, DurationMillis: 1, Outcome: gatecontract.RemoteCIPhaseOutcomeSucceeded, WorkloadCount: 1, ShardCount: 1, CacheMissCount: 1}}}
	if err := ledgerStore.RecordRemoteCIRun(record); err != nil {
		t.Fatal(err)
	}
	loaded, err := ledgerStore.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	result := remoteRunResultFromLedgerRecord(loaded)
	if result.SchemaVersion != remoteci.RunResultSchemaVersion || !reflect.DeepEqual(result.CacheMissWorkloads, loaded.CacheMisses) || !reflect.DeepEqual(result.OptimizationWarnings, loaded.Warnings) || !reflect.DeepEqual(result.PerformanceTimings, loaded.PhaseTimings) || !reflect.DeepEqual(result.GateExecutions, loaded.Executions) {
		t.Fatalf("rehydrated result lost run projection: %#v", result)
	}
	if len(result.Shards) != 1 || !reflect.DeepEqual(result.Shards[0].ExecutedWorkloads, loaded.Shards[0].Workloads) || !reflect.DeepEqual(result.Shards[0].MaterializationTiming, loaded.Shards[0].MaterializationTiming) {
		t.Fatalf("rehydrated shard = %#v, loaded shard = %#v", result.Shards, loaded.Shards)
	}
}

func TestReusableRemoteCalibrationCheckpointReusesVerifiedPassWithoutMissingDuration(t *testing.T) {
	ledgerStore, checkpoint, input, _ := remoteCalibrationCheckpointFixture(t)
	if _, _, reusable, err := reusableRemoteCalibrationCheckpoint(ledgerStore, checkpoint, "commit"); err != nil || reusable {
		t.Fatalf("checkpoint with missing ledger evidence reusable=%t err=%v", reusable, err)
	}
	if _, _, completed := checkpoint.Completed("commit"); completed {
		t.Fatal("checkpoint with missing ledger evidence was not reopened")
	}
	catalog, _, err := remoteCalibrationCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	result, reused := remoteCalibrationCheckpointVerifiedPass(t, input, catalog)
	remoteCalibrationCheckpointRecordVerifiedPass(t, ledgerStore, catalog, input, result, reused)
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}
	if _, _, reusable, err := reusableRemoteCalibrationCheckpoint(ledgerStore, checkpoint, "commit"); err != nil || !reusable {
		t.Fatalf("checkpoint with verified PASS coverage reusable=%t err=%v", reusable, err)
	}
}

func remoteCalibrationCheckpointVerifiedPass(t *testing.T, input remoteci.RunInput, catalog gatecontract.WorkloadCatalog) (remoteci.RunResult, []gatecontract.GateID) {
	t.Helper()
	executions := make([]gatecontract.PlanGateExecution, 0, len(catalog.Workloads))
	reused := make([]gatecontract.GateID, len(catalog.Workloads))
	seenExecutions := make(map[gatecontract.GateID]struct{})
	for index, workload := range catalog.Workloads {
		parent, err := gatecontract.WorkloadParentGateID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := seenExecutions[parent]; !exists {
			executions = append(executions, gatecontract.PlanGateExecution{
				GateID: parent,
				Status: gatecontract.ResultStatusPassed,
				ExecutionProfile: gatecontract.ExecutionProfile{
					CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured",
				},
			})
			seenExecutions[parent] = struct{}{}
		}
		reused[index] = gatecontract.GateID(workload.ID)
	}
	now := time.Now().UTC()
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return remoteci.RunResult{
		JobID:           "job-checkpoint-pass",
		Entrypoint:      input.Entrypoint,
		Profile:         input.Profile,
		PlanDigest:      "sha256:plan",
		CatalogDigest:   catalogDigest,
		SourceTreeSHA:   input.Source.SourceTreeSHA,
		RunnerImage:     "ubuntu:22.04",
		Authoritative:   true,
		Status:          gatecontract.ResultStatusPassed,
		StartedAt:       now,
		CompletedAt:     now.Add(time.Second),
		GateExecutions:  executions,
		CleanupComplete: true,
	}, reused
}

func remoteCalibrationCheckpointRecordVerifiedPass(t *testing.T, ledgerStore *gatecontract.DurationLedgerStore, catalog gatecontract.WorkloadCatalog, input remoteci.RunInput, result remoteci.RunResult, reused []gatecontract.GateID) {
	t.Helper()
	now := result.StartedAt
	if err := ledgerStore.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{
		SourceTreeSHA: input.Tree,
		Entrypoint:    input.Entrypoint,
		Profile:       input.Profile,
		ObservedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := ledgerStore.RecordRemoteCIRun(gatecontract.RemoteCIRunRecord{
		JobID:           result.JobID,
		Entrypoint:      result.Entrypoint,
		Profile:         result.Profile,
		PlanDigest:      result.PlanDigest,
		CatalogDigest:   result.CatalogDigest,
		SourceTreeSHA:   result.SourceTreeSHA,
		RunnerImage:     result.RunnerImage,
		Status:          result.Status,
		Authoritative:   result.Authoritative,
		StartedAt:       result.StartedAt,
		CompletedAt:     result.CompletedAt,
		CleanupComplete: result.CleanupComplete,
		Executions:      result.GateExecutions,
		ReusedWorkloads: reused,
	}); err != nil {
		t.Fatal(err)
	}
}

func remoteCalibrationCheckpointFixture(
	t *testing.T,
) (*gatecontract.DurationLedgerStore, *remoteci.CalibrationCheckpoint, remoteci.RunInput, gatecontract.DurationSample) {
	t.Helper()
	ledgerStore, err := prepareRemoteCalibrationLedger(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := remoteci.NewCalibrationCheckpoint(
		ledgerStore, "sha256:checkpoint",
	)
	if err != nil {
		t.Fatal(err)
	}
	tree := strings.Repeat("3", 40)
	input := remoteci.RunInput{
		Calibration: true, Profile: gatecontract.ProfileLocalFast,
		Entrypoint: gatecontract.CIEntrypointGitPreCommit,
		Platform:   "linux/amd64", RunnerIdentityDigest: "runner", ToolchainDigest: "toolchain",
		Tree: tree, RunnerImage: "ubuntu:22.04",
		Inventory: gatecontract.WorkloadInventory{GoPackages: []string{"./internal/alpha"}},
		Source: gatecontract.SourceSpec{
			Kind: gatecontract.SourceKindTree, ObjectFormat: gatecontract.GitObjectFormatSHA1,
			Tree: &gatecontract.TreeSource{SHA: tree, ParentCommitSHA: strings.Repeat("1", 40)}, SourceTreeSHA: tree,
		},
	}
	catalog, _, err := remoteCalibrationCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	samples := make([]gatecontract.DurationSample, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		samples = append(samples, gatecontract.DurationSample{
			Bucket: gatecontract.DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
			},
			Succeeded: true, DurationMS: 1_000,
		})
	}
	missing := samples[len(samples)-1]
	if _, err := ledgerStore.AppendSamples(samples[:len(samples)-1]); err != nil {
		t.Fatal(err)
	}
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	completed := remoteci.RunResult{
		JobID: "job-missing-checkpoint", Entrypoint: input.Entrypoint, Profile: input.Profile,
		PlanDigest: "sha256:plan", CatalogDigest: catalogDigest, SourceTreeSHA: input.Tree,
		RunnerImage: input.RunnerImage, Status: gatecontract.ResultStatusPassed,
		Authoritative: true, CleanupComplete: true, CompletedAt: time.Now().UTC(),
		DurationSamples: []gatecontract.DurationSample{{DurationMS: 1}},
	}
	if err := checkpoint.Observe("commit", input, completed, true); err != nil {
		t.Fatal(err)
	}
	return ledgerStore, checkpoint, input, missing
}
