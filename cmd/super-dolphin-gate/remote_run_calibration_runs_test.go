package main

import (
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

func TestExecuteRemoteCalibrationRunsResumesOnlyPersistedAuthoritativeScenario(t *testing.T) {
	store := newRemoteCalibrationRunsStore(t, 1)
	checkpoint := newRemoteCalibrationRunsCheckpoint(t, store, "checkpoint-resume", 1)
	options := remoteRunOptions{}
	identity := remoteCalibrationIdentity{commit: strings.Repeat("c", 40), tree: strings.Repeat("t", 40), base: strings.Repeat("b", 40)}
	input, result := calibrationRunsInputResult("commit", 1)
	result = calibrationRunsRecord(t, store, input, result)
	if err := checkpoint.Observe("commit", input, result, true); err != nil {
		t.Fatal(err)
	}

	checkpoint = newRemoteCalibrationRunsCheckpoint(t, store, "checkpoint-resume", 1)
	var resumedCalls []string
	_, _, err := executeRemoteCalibrationRunsWithExecutor(options, identity, store, checkpoint, io.Discard, calibrationRunsExecutor(t, store, &resumedCalls, "push"))
	if err == nil {
		t.Fatal("resumed executeRemoteCalibrationRunsWithExecutor() error = nil")
	}
	if got, want := strings.Join(resumedCalls, ","), "push"; got != want {
		t.Fatalf("resumed executor calls = %q, want only the unfinished scenario %q", got, want)
	}
}

func TestExecuteRemoteCalibrationRunsFailedScenarioIsStartedAndRerun(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store := newRemoteCalibrationRunsStoreAt(t, ledgerPath, 1)
	checkpoint := newRemoteCalibrationRunsCheckpoint(t, store, "checkpoint-failed", 1)
	identity := remoteCalibrationIdentity{commit: strings.Repeat("c", 40), tree: strings.Repeat("t", 40), base: strings.Repeat("b", 40)}
	var failedCalls []string
	_, _, err := executeRemoteCalibrationRunsWithExecutor(remoteRunOptions{}, identity, store, checkpoint, io.Discard, calibrationRunsExecutor(t, store, &failedCalls, "commit"))
	if err == nil {
		t.Fatal("failed executeRemoteCalibrationRunsWithExecutor() error = nil")
	}
	if got, want := strings.Join(failedCalls, ","), "commit"; got != want {
		t.Fatalf("failed executor calls = %q, want %q", got, want)
	}
	reopenedStore, err := prepareRemoteCalibrationLedger(ledgerPath)
	if err != nil {
		t.Fatalf("reopen duration ledger authority: %v", err)
	}
	record, found, err := reopenedStore.LoadCalibrationCheckpoint("checkpoint-failed")
	if err != nil {
		t.Fatalf("load failed calibration checkpoint: %v", err)
	}
	if !found || len(record.Scenarios) != 1 {
		t.Fatalf("failed calibration checkpoint = %#v, found=%t, want one scenario", record, found)
	}
	scenario := record.Scenarios[0]
	if scenario.Scenario != "commit" || !scenario.Started || scenario.Completed || scenario.InputJSON != "" || scenario.ResultJSON != "" {
		t.Fatalf("failed calibration scenario = %#v, want commit started with no result", scenario)
	}

	checkpoint = newRemoteCalibrationRunsCheckpoint(t, reopenedStore, "checkpoint-failed", 1)
	var retriedCalls []string
	_, _, err = executeRemoteCalibrationRunsWithExecutor(remoteRunOptions{}, identity, reopenedStore, checkpoint, io.Discard, calibrationRunsExecutor(t, reopenedStore, &retriedCalls, "push"))
	if err == nil {
		t.Fatal("retry executeRemoteCalibrationRunsWithExecutor() error = nil")
	}
	if got, want := strings.Join(retriedCalls, ","), "commit,push"; got != want {
		t.Fatalf("retry executor calls = %q, want %q", got, want)
	}
}

func TestExecuteRemoteCalibrationRunsReopensMissingOrMismatchedAuthorityRun(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult)
	}{
		{name: "missing run", apply: func(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult) {
			t.Helper()
			calibrationRunsUpdateAuthorityRun(t, store, "DELETE FROM ci_runs WHERE job_id = ?", result.JobID)
		}},
		{name: "generation mismatch", apply: func(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult) {
			t.Helper()
			calibrationRunsUpdateAuthorityRun(t, store, "UPDATE ci_runs SET accepted_generation = accepted_generation + 1 WHERE job_id = ?", result.JobID)
		}},
		{name: "JSON identity mismatch", apply: func(t *testing.T, store *gatecontract.DurationLedgerStore, result remoteci.RunResult) {
			t.Helper()
			calibrationRunsUpdateAuthorityRun(t, store, "UPDATE ci_runs SET candidate_gate_source_sha256 = ? WHERE job_id = ?", "sha256:"+strings.Repeat("g", 64), result.JobID)
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			store := newRemoteCalibrationRunsStore(t, 1)
			checkpoint := newRemoteCalibrationRunsCheckpoint(t, store, "checkpoint-reopen-"+mutate.name, 1)
			input, result := calibrationRunsInputResult("commit", 1)
			result = calibrationRunsRecord(t, store, input, result)
			if err := checkpoint.Observe("commit", input, result, true); err != nil {
				t.Fatal(err)
			}
			mutate.apply(t, store, result)
			checkpoint = newRemoteCalibrationRunsCheckpoint(t, store, "checkpoint-reopen-"+mutate.name, 1)
			var calls []string
			_, _, err := executeRemoteCalibrationRunsWithExecutor(remoteRunOptions{}, remoteCalibrationIdentity{commit: strings.Repeat("c", 40), tree: strings.Repeat("t", 40), base: strings.Repeat("b", 40)}, store, checkpoint, io.Discard, calibrationRunsExecutor(t, store, &calls, "push"))
			if err == nil {
				t.Fatal("executeRemoteCalibrationRunsWithExecutor() error = nil")
			}
			if got, want := strings.Join(calls, ","), "commit,push"; got != want {
				t.Fatalf("reopened calls = %q, want %q", got, want)
			}
		})
	}
}

func TestExecuteRemoteCalibrationRunsReturnsObserveFailure(t *testing.T) {
	store := newRemoteCalibrationRunsStore(t, 1)
	checkpoint := newRemoteCalibrationRunsCheckpoint(t, store, "checkpoint-observe-failure", 1)
	var calls []string
	_, _, err := executeRemoteCalibrationRunsWithExecutor(remoteRunOptions{}, remoteCalibrationIdentity{}, store, checkpoint, io.Discard, func(options remoteRunOptions) (remoteci.RunResult, remoteci.RunInput, error) {
		calls = append(calls, options.Scenario)
		input, result := calibrationRunsInputResult(options.Scenario, 2)
		return result, input, nil
	})
	if err == nil || !strings.Contains(err.Error(), "persist remote calibration checkpoint") {
		t.Fatalf("executeRemoteCalibrationRunsWithExecutor() error = %v, want Observe failure", err)
	}
	if got, want := strings.Join(calls, ","), "commit"; got != want {
		t.Fatalf("executor calls after Observe failure = %q, want %q", got, want)
	}
}

func newRemoteCalibrationRunsStore(t *testing.T, generation uint64) *gatecontract.DurationLedgerStore {
	return newRemoteCalibrationRunsStoreAt(t, filepath.Join(t.TempDir(), "duration-ledger.sqlite"), generation)
}

func newRemoteCalibrationRunsStoreAt(t *testing.T, ledgerPath string, generation uint64) *gatecontract.DurationLedgerStore {
	t.Helper()
	store, err := prepareRemoteCalibrationLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	seedRemoteRunTestAcceptedGeneration(t, store, generation)
	return store
}

func newRemoteCalibrationRunsCheckpoint(t *testing.T, store *gatecontract.DurationLedgerStore, identity string, generation uint64) *remoteci.CalibrationCheckpoint {
	t.Helper()
	checkpoint, err := remoteci.NewCalibrationCheckpoint(store, identity, generation)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func calibrationRunsExecutor(t *testing.T, store *gatecontract.DurationLedgerStore, calls *[]string, failScenario string) func(remoteRunOptions) (remoteci.RunResult, remoteci.RunInput, error) {
	t.Helper()
	return func(options remoteRunOptions) (remoteci.RunResult, remoteci.RunInput, error) {
		*calls = append(*calls, options.Scenario)
		input, result := calibrationRunsInputResult(options.Scenario, 1)
		result.JobID += "-retry"
		if options.Scenario == failScenario {
			return result, input, errors.New("remote run failed")
		}
		result = calibrationRunsRecord(t, store, input, result)
		return result, input, nil
	}
}

func calibrationRunsInputResult(scenario string, generation uint64) (remoteci.RunInput, remoteci.RunResult) {
	entrypoint, profile := gatecontract.CIEntrypointGitPreCommit, gatecontract.ProfileLocalFast
	if scenario == "push" {
		entrypoint, profile = gatecontract.CIEntrypointGitPrePush, gatecontract.ProfilePush
	}
	if scenario == "full" {
		entrypoint, profile = gatecontract.CIEntrypointRelease, gatecontract.ProfileRelease
	}
	tree := strings.Repeat("a", 40)
	input := remoteci.RunInput{AcceptedGeneration: generation, Tree: tree, Source: gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormatSHA1, Commit: &gatecontract.CommitSource{SHA: strings.Repeat("b", 40)}, SourceTreeSHA: tree}, Profile: profile, Entrypoint: entrypoint, Platform: "linux/amd64", ToolchainDigest: "sha256:" + strings.Repeat("c", 64), CandidateGateSourceSHA256: "sha256:" + strings.Repeat("d", 64), CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("e", 64), Calibration: true, RunnerIdentityDigest: "sha256:" + strings.Repeat("f", 64), RunnerImage: "registry.example/runner@sha256:" + strings.Repeat("0", 64), CalibrationResource: shardresource.Class{ID: "maximum", VCPU: 8, MemoryGiB: 32}}
	result := remoteci.RunResult{AcceptedGeneration: generation, JobID: "job-" + scenario, Entrypoint: entrypoint, Profile: profile, SourceTreeSHA: tree, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, Status: gatecontract.ResultStatusPassed, Authoritative: true, CleanupComplete: true, CompletedAt: time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC), DurationSamples: []gatecontract.DurationSample{{Bucket: gatecontract.DurationBucket{WorkloadID: "guard:test", CommandDigest: strings.Repeat("1", 64), Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest}, Succeeded: true, DurationMS: 1}}}
	return input, result
}

func calibrationRunsRecord(t *testing.T, store *gatecontract.DurationLedgerStore, input remoteci.RunInput, result remoteci.RunResult) remoteci.RunResult {
	t.Helper()
	catalog, digest, err := remoteCalibrationCatalog(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordWorkloadCatalog(catalog, gatecontract.WorkloadCatalogObservation{SourceTreeSHA: input.Tree, Entrypoint: input.Entrypoint, Profile: input.Profile, AcceptedGeneration: input.AcceptedGeneration, ObservedAt: result.CompletedAt.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	result.CatalogDigest, result.PlanDigest = digest, "sha256:plan-"+result.JobID
	executions := make([]gatecontract.PlanGateExecution, 0, len(catalog.Workloads))
	workloads := make([]gatecontract.GateID, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		startedAt := result.CompletedAt.Add(-997 * time.Millisecond)
		executions = append(executions, gatecontract.PlanGateExecution{GateID: gatecontract.GateID(workload.ID), ShardIdentity: "sha256:" + strings.Repeat("9", 64), Status: gatecontract.ResultStatusPassed, StartedAt: startedAt, CompletedAt: startedAt.Add(11 * time.Millisecond), ExecutionProfile: gatecontract.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 10, TotalMS: 11}})
		workloads = append(workloads, gatecontract.GateID(workload.ID))
	}
	startedAt := result.CompletedAt.Add(-time.Second)
	shard := gatecontract.RemoteCIShardRecord{ShardIdentity: "sha256:" + strings.Repeat("9", 64), ContainerGroup: "eci-calibration", ContainerStatus: "Succeeded", Workloads: workloads, MaterializationTiming: gatecontract.ShardMaterializationTiming{Measurement: gatecontract.MaterializationMeasurementMeasured, ShardIdentity: "sha256:" + strings.Repeat("9", 64), Source: gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.UnixMilli(), CompletedAtUnixMS: startedAt.Add(time.Millisecond).UnixMilli(), MaterializeMS: 1}, CandidateCompile: gatecontract.MaterializationPhaseTiming{StartedAtUnixMS: startedAt.Add(time.Millisecond).UnixMilli(), CompletedAtUnixMS: startedAt.Add(2 * time.Millisecond).UnixMilli(), MaterializeMS: 1}}}
	timings := remoteRunReceiptTestTimingObservations(result.JobID, shard, executions[0], startedAt)
	for _, execution := range executions[1:] {
		observations := remoteRunReceiptTestTimingObservations(result.JobID, shard, execution, startedAt)
		for _, observation := range observations {
			if observation.Scope == cicontract.TimingScopeWorkload {
				timings = append(timings, observation)
			}
		}
	}
	record := gatecontract.RemoteCIRunRecord{JobID: result.JobID, AcceptedGeneration: result.AcceptedGeneration, Entrypoint: result.Entrypoint, Profile: result.Profile, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest, SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, Status: result.Status, Authoritative: result.Authoritative, CleanupComplete: result.CleanupComplete, RunnerImage: input.RunnerImage, StartedAt: startedAt, CompletedAt: result.CompletedAt, Shards: []gatecontract.RemoteCIShardRecord{shard}, WorkloadExecutions: executions, TimingObservations: timings}
	if err := store.RecordRemoteCIRun(record); err != nil {
		t.Fatal(err)
	}
	return result
}

func calibrationRunsUpdateAuthorityRun(t *testing.T, store *gatecontract.DurationLedgerStore, query string, args ...any) {
	t.Helper()
	database, err := sql.Open("sqlite", store.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	result, err := database.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatalf("mutate authority run affected=%d err=%v", count, err)
	}
}
