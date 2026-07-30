package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

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
	ledgerStore, err := prepareRemoteCalibrationLedger(filepath.Join(t.TempDir(), "duration-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := remoteci.NewCalibrationCheckpoint(
		filepath.Join(t.TempDir(), "duration-ledger.json.calibration.checkpoint"), "sha256:checkpoint",
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
