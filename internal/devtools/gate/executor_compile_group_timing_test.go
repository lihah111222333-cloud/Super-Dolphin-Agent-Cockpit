package gate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRunCompileGroupCommandSuccessfulGoTestCompileProducesValidObservation(t *testing.T) {
	t.Setenv("GOCACHEPROG", "/definitely-not-an-inherited-go-cache-proxy")
	goBinary, workDir, binaryPath := compileGroupTestModule(t)
	if filepath.Base(binaryPath) != "test-binary.test" {
		t.Fatalf("compiled test binary path = %q", binaryPath)
	}
	argv := []string{"go", "test", "-c", "-o", binaryPath, "./sample"}
	now := incrementingSubmillisecondTestClock(time.UnixMilli(1_000_000))
	started, completed, runErr := runCompileGroupCommand(context.Background(), goBinary, argv, workDir, compileGroupTestEnvironment(t), now, &bytes.Buffer{})
	if runErr != nil {
		t.Fatalf("go test -c failed: %v", runErr)
	}
	if started.IsZero() || completed.IsZero() || !completed.After(started) {
		t.Fatalf("compile observation interval = %v..%v", started, completed)
	}
	assertSuccessfulCompileGroupObservation(t, started, completed, binaryPath, argv)
}

func TestCompiledSelectorReadsPackageRelativeFixture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("compiled test binary working-directory test is Unix-specific")
	}
	goBinary, workDir, binaryPath := compileGroupTestModule(t)
	compileArgv := []string{"go", "test", "-c", "-o", binaryPath, "./sample"}
	if _, _, err := runCompileGroupCommand(context.Background(), goBinary, compileArgv, workDir, compileGroupTestEnvironment(t), time.Now, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	packageDir, err := filepath.EvalSymlinks(filepath.Join(workDir, "sample"))
	if err != nil {
		t.Fatal(err)
	}
	selectorArgv := []string{"go", "tool", "test2json", "-t", "-p", "./sample", binaryPath, "-test.v", "-test.run=^TestCompileGroup$", "-test.count=1"}
	observation := runCompiledSelectorProcess(context.Background(), compiledGroupArtifact{
		layout: executorLayout{sourceCopy: workDir}, goBinary: goBinary, binaryPath: binaryPath, packageDir: packageDir,
	}, selectorArgv, incrementingTestClock(time.UnixMilli(6_000_000)))
	if observation.err() != nil {
		t.Fatal(observation.err())
	}
	workload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	result, err := compiledSelectorResult(GateID(workload.ID), selectorArgv, observation)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ResultStatusPassed {
		t.Fatalf("compiled selector status = %s, want passed; log=%q", result.Status, result.Log)
	}
}

func TestCompileGroupBatchCommandArgvUsesSingleAnchoredSelector(t *testing.T) {
	group := compileGroupBatchTestGroup(t)
	argv, specs := mustCompileGroupBatchCommand(t, group)
	if len(specs) != 2 || argv[0] != "go" || argv[1] != "tool" || argv[2] != "test2json" {
		t.Fatalf("batch argv/specs = %#v/%#v", argv, specs)
	}
	if !strings.Contains(argv[8], "^(TestCompileGroup|TestCompileGroupSecond)$") {
		t.Fatalf("batch selector regex = %q", argv[8])
	}
	if strings.Contains(argv[8], "(?:") {
		t.Fatalf("batch selector regex used unsupported non-capturing syntax: %q", argv[8])
	}
	raceGroup := group
	raceGroup.SemanticKey = CompileGroupSemanticGoTestRace
	raceArgv, _ := mustCompileGroupBatchCommand(t, raceGroup)
	if len(raceArgv) != 11 || raceArgv[7] != "-test.short" {
		t.Fatalf("race batch argv = %#v, want -test.short", raceArgv)
	}
}

func TestCompiledSelectorBatchMapsEveryTerminalResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell working-directory test is Unix-specific")
	}
	group, results := runCompileGroupBatchFixture(t)
	if len(results) != len(group.WorkloadIDs) {
		t.Fatalf("batch results = %#v", results)
	}
	for _, id := range group.WorkloadIDs {
		assertCompiledSelectorBatchResult(t, results, id)
	}
}

func TestCompiledSelectorBatchKeepsSharedCompileWallOutOfConcurrentSelectorProfiles(t *testing.T) {
	group := compileGroupBatchTestGroup(t)
	_, specs := mustCompileGroupBatchCommand(t, group)
	base := time.UnixMilli(7_000_000)
	compileCompleted := base.Add(200 * time.Second)
	processStarted := compileCompleted
	bodyStarted := processStarted.Add(time.Millisecond)
	first := group.WorkloadIDs[0]
	second := group.WorkloadIDs[1]
	firstName, secondName := specs[first].name, specs[second].name
	firstRun := bodyStarted.Add(100 * time.Millisecond)
	secondRun := bodyStarted.Add(200 * time.Millisecond)
	observation := compiledSelectorBatchObservation{
		started: processStarted, bodyStarted: bodyStarted, completed: secondRun.Add(time.Second),
		selectorTimings: map[string][]GoTestTiming{
			firstName:  {{Name: firstName, Status: GoTestStatusPass, DurationMS: 1_000}},
			secondName: {{Name: secondName, Status: GoTestStatusPass, DurationMS: 1_000}},
		},
		selectorIntervals: map[string]compiledSelectorBatchInterval{
			firstName:  {runAt: firstRun, completedAt: firstRun.Add(time.Second)},
			secondName: {runAt: secondRun, completedAt: secondRun.Add(time.Second)},
		},
	}
	results, err := compiledSelectorBatchResults(group, []string{"go", "tool", "test2json"}, specs, observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range group.WorkloadIDs {
		result := results[id]
		if result.ExecutionProfile.StartupMS != 1 || result.ExecutionProfile.TestBodyMS != 1_000 || result.ExecutionProfile.TotalMS != 1_001 {
			t.Fatalf("selector %q profile = %#v, shared compile wall must stay outside selector timing", id, result.ExecutionProfile)
		}
		if got := result.CompletedAt.Sub(processStarted); got >= 200*time.Second {
			t.Fatalf("selector %q includes shared compile wall in completion: %v", id, got)
		}
	}
	if !firstRun.Before(secondRun.Add(time.Second)) || !secondRun.Before(firstRun.Add(time.Second)) {
		t.Fatal("selector intervals are not concurrent")
	}
}

func compileGroupBatchTestGroup(t *testing.T) CompileGroup {
	t.Helper()
	return compileGroupBatchTestGroupForPackage(t, "./sample")
}

func compileGroupBatchTestGroupForPackage(t *testing.T, packageTarget string) CompileGroup {
	t.Helper()
	first := mustCompileGroupBatchWorkloadForPackage(t, packageTarget, "TestCompileGroup")
	second := mustCompileGroupBatchWorkloadForPackage(t, packageTarget, "TestCompileGroupSecond")
	return CompileGroup{PackageTarget: packageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, WorkloadIDs: []GateID{GateID(first.ID), GateID(second.ID)}}
}

func mustCompileGroupBatchCommand(t *testing.T, group CompileGroup) ([]string, map[GateID]compiledSelectorBatchSpec) {
	t.Helper()
	batch := CompileGroupBatch{BatchID: "batch-000", Wave: 0, SelectorIDs: append([]GateID(nil), group.WorkloadIDs...), EstimatedBodyMS: 1}
	argv, specs, err := compileGroupBatchCommandArgvForBatch(group, batch, "/work/test-binary")
	if err != nil {
		t.Fatal(err)
	}
	return argv, specs
}

func runCompileGroupBatchFixture(t *testing.T) (CompileGroup, map[GateID]PlanGateExecution) {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	binaryPath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workDir := canonicalCompileGroupTestTempDir(t)
	if err := os.WriteFile(filepath.Join(workDir, "fixture.txt"), []byte("compile fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	group := compileGroupBatchTestGroupForPackage(t, "./internal/devtools/gate")
	environment := append(compileGroupTestEnvironment(t), "GO_WANT_COMPILE_GROUP_BATCH_HELPER=1")
	if err := os.Chmod(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	batch := CompileGroupBatch{BatchID: "batch-000", Wave: 0, SelectorIDs: append([]GateID(nil), group.WorkloadIDs...), EstimatedBodyMS: 1}
	results, err := executeCompiledSelectorBatchForBatch(context.Background(), compiledGroupArtifact{
		group: group, layout: executorLayout{sourceCopy: workDir}, environment: environment,
		goBinary: goBinary, binaryPath: binaryPath, packageDir: workDir,
	}, group, batch, incrementingTestClock(time.UnixMilli(7_000_000)))
	if err != nil {
		t.Fatal(err)
	}
	return group, results
}

func assertCompiledSelectorBatchResult(t *testing.T, results map[GateID]PlanGateExecution, id GateID) {
	t.Helper()
	result, ok := results[id]
	if !ok || result.Status != ResultStatusPassed || result.ExitCode != 0 {
		t.Fatalf("batch result for %q = %#v", id, result)
	}
	if len(result.TestTimings) == 0 || result.ExecutionProfile.TestBodyMS <= 0 || result.ArgvDigest == "" {
		t.Fatalf("batch result lacks canonical timing/identity for %q: %#v", id, result)
	}
}

func TestCompiledSelectorBatchRejectsMissingExtraAndDuplicateResults(t *testing.T) {
	specs := map[GateID]compiledSelectorBatchSpec{
		GateID("first"):  {id: GateID("first"), name: "TestCompileGroup"},
		GateID("second"): {id: GateID("second"), name: "TestCompileGroupSecond"},
	}

	t.Run("missing", func(t *testing.T) {
		writer := newCompiledSelectorBatchEventWriter(newBoundedPlanLog(executorPlanMaxLogBytes), batchTestSelectorNames(specs))
		writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00Z","Test":"TestCompileGroup"}`)
		writeBatchTestEvent(t, writer, `{"Action":"pass","Time":"2026-08-05T00:00:00.001Z","Test":"TestCompileGroup","Elapsed":0.001}`)
		observation := compiledSelectorBatchObservation{
			selectorTimings:   writer.selectorTimings,
			selectorIntervals: writer.selectorIntervals,
			extraTopLevel:     writer.extraTopLevel,
		}
		if err := observation.validateSelectorResults(specs); err == nil || !strings.Contains(err.Error(), "TestCompileGroupSecond") {
			t.Fatalf("missing selector validation error = %v", err)
		}
	})

	t.Run("extra", func(t *testing.T) {
		writer := newCompiledSelectorBatchEventWriter(newBoundedPlanLog(executorPlanMaxLogBytes), batchTestSelectorNames(specs))
		writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00Z","Test":"TestCompileGroup"}`)
		writeBatchTestEvent(t, writer, `{"Action":"pass","Time":"2026-08-05T00:00:00.001Z","Test":"TestCompileGroup","Elapsed":0.001}`)
		writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00.002Z","Test":"TestCompileGroupSecond"}`)
		writeBatchTestEvent(t, writer, `{"Action":"pass","Time":"2026-08-05T00:00:00.003Z","Test":"TestCompileGroupSecond","Elapsed":0.001}`)
		writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00.004Z","Test":"TestUnexpected"}`)
		writeBatchTestEvent(t, writer, `{"Action":"pass","Time":"2026-08-05T00:00:00.005Z","Test":"TestUnexpected","Elapsed":0.001}`)
		observation := compiledSelectorBatchObservation{
			selectorTimings:   writer.selectorTimings,
			selectorIntervals: writer.selectorIntervals,
			extraTopLevel:     writer.extraTopLevel,
		}
		if err := observation.validateSelectorResults(specs); err == nil || !strings.Contains(err.Error(), "unexpected top-level") {
			t.Fatalf("extra selector validation error = %v", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		writer := newCompiledSelectorBatchEventWriter(newBoundedPlanLog(executorPlanMaxLogBytes), batchTestSelectorNames(specs))
		writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00Z","Test":"TestCompileGroup"}`)
		writeBatchTestEvent(t, writer, `{"Action":"pass","Time":"2026-08-05T00:00:00.001Z","Test":"TestCompileGroup","Elapsed":0.001}`)
		if _, err := writer.Write([]byte(`{"Action":"run","Time":"2026-08-05T00:00:00.002Z","Test":"TestCompileGroup"}` + "\n")); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("duplicate selector write error = %v", err)
		}
	})

	t.Run("missing time", func(t *testing.T) {
		writer := newCompiledSelectorBatchEventWriter(newBoundedPlanLog(executorPlanMaxLogBytes), batchTestSelectorNames(specs))
		if _, err := writer.Write([]byte(`{"Action":"run","Test":"TestCompileGroup"}` + "\n")); err == nil || !strings.Contains(err.Error(), "event time is required") {
			t.Fatalf("missing event time error = %v", err)
		}
	})
}

func TestCompiledSelectorBatchUsesPerSelectorEventIntervals(t *testing.T) {
	base := time.UnixMilli(8_000_000).UTC()
	observation := compiledSelectorBatchObservation{
		started: base, bodyStarted: base.Add(time.Millisecond), completed: base.Add(25 * time.Millisecond),
		log: newBoundedPlanLog(executorPlanMaxLogBytes), selectorLogs: map[string][]byte{},
		selectorIntervals: map[string]compiledSelectorBatchInterval{
			"TestCompileGroup":       {runAt: base.Add(time.Millisecond), completedAt: base.Add(8 * time.Millisecond)},
			"TestCompileGroupSecond": {runAt: base.Add(4 * time.Millisecond), completedAt: base.Add(20 * time.Millisecond)},
		},
	}
	timings := map[string][]GoTestTiming{
		"TestCompileGroup":       {{Name: "TestCompileGroup", Status: GoTestStatusPass, DurationMS: 7}},
		"TestCompileGroupSecond": {{Name: "TestCompileGroupSecond", Status: GoTestStatusPass, DurationMS: 16}},
	}
	firstWorkload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	secondWorkload := mustCompileGroupBatchWorkload(t, "TestCompileGroupSecond")
	first, err := compiledSelectorResultWithLog(GateID(firstWorkload.ID), []string{"go", "tool", "test2json"}, observation, "TestCompileGroup", timings["TestCompileGroup"], observation.selectorIntervals["TestCompileGroup"], nil, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compiledSelectorResultWithLog(GateID(secondWorkload.ID), []string{"go", "tool", "test2json"}, observation, "TestCompileGroupSecond", timings["TestCompileGroupSecond"], observation.selectorIntervals["TestCompileGroupSecond"], nil, false)
	if err != nil {
		t.Fatal(err)
	}
	assertPerSelectorExecutionProfiles(t, first, second, observation.completed.Sub(observation.started).Milliseconds())
}

func assertPerSelectorExecutionProfiles(t *testing.T, first, second PlanGateExecution, batchTotalMS int64) {
	t.Helper()
	if first.ExecutionProfile.TotalMS != 8 || second.ExecutionProfile.TotalMS != 17 || first.ExecutionProfile.TotalMS == batchTotalMS || second.ExecutionProfile.TotalMS == batchTotalMS {
		t.Fatalf("per-selector totals = %d/%d, batch total = %d", first.ExecutionProfile.TotalMS, second.ExecutionProfile.TotalMS, batchTotalMS)
	}
	if first.ExecutionProfile.StartupMS != 1 || second.ExecutionProfile.StartupMS != 1 {
		t.Fatalf("per-selector startups = %d/%d", first.ExecutionProfile.StartupMS, second.ExecutionProfile.StartupMS)
	}
	if first.ExecutionProfile.TestBodyMS != 7 || second.ExecutionProfile.TestBodyMS != 16 {
		t.Fatalf("per-selector bodies = %d/%d", first.ExecutionProfile.TestBodyMS, second.ExecutionProfile.TestBodyMS)
	}
}

func TestFailedCompiledSelectorBatchCancelsUnobservedCompanions(t *testing.T) {
	first := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	second := mustCompileGroupBatchWorkload(t, "TestCompileGroupSecond")
	group := CompileGroup{PackageTarget: "./sample", WorkloadIDs: []GateID{GateID(first.ID), GateID(second.ID)}}
	base := time.UnixMilli(8_500_000).UTC()
	observation := compiledSelectorBatchObservation{
		started: base, bodyStarted: base.Add(2 * time.Millisecond), completed: base.Add(100 * time.Millisecond),
		log: newBoundedPlanLog(executorPlanMaxLogBytes), selectorLogs: map[string][]byte{},
		selectorTimings: map[string][]GoTestTiming{
			"TestCompileGroup": {{Name: "TestCompileGroup", Status: GoTestStatusFail, DurationMS: 12}},
		},
		selectorIntervals: map[string]compiledSelectorBatchInterval{
			"TestCompileGroup": {runAt: base.Add(3 * time.Millisecond), completedAt: base.Add(9 * time.Millisecond)},
		},
	}
	results, err := failedCompiledSelectorBatchResults(group, []string{"go", "tool", "test2json"}, &observation, errors.New("batch process exited"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	firstResult := results[group.WorkloadIDs[0]]
	secondResult := results[group.WorkloadIDs[1]]
	if firstResult.Status != ResultStatusFailed || firstResult.ExecutionProfile.TotalMS == observation.completed.Sub(observation.started).Milliseconds() {
		t.Fatalf("observed failed selector result = %#v, want its own interval", firstResult)
	}
	if firstResult.ExecutionProfile.TestBodyMS != 12 || firstResult.ExecutionProfile.TotalMS != 13 {
		t.Fatalf("failed selector normalized profile = %#v, want body=12 total=13", firstResult.ExecutionProfile)
	}
	if err := ValidatePlanGateTimingEvidence(firstResult); err != nil {
		t.Fatalf("failed selector normalized timing rejected: %v", err)
	}
	if secondResult.Status != ResultStatusCancelled || secondResult.ExecutionProfile.TotalMS != 0 || len(secondResult.TestTimings) != 0 {
		t.Fatalf("unobserved companion result = %#v, want cancelled without timing", secondResult)
	}
}

func TestCompiledSelectorBatchExcludesParallelPauseWaitFromBodyTiming(t *testing.T) {
	selector := "TestCompileGroup"
	writer := newCompiledSelectorBatchEventWriter(newBoundedPlanLog(executorPlanMaxLogBytes), map[string]struct{}{selector: {}})
	writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00.001Z","Test":"TestCompileGroup"}`)
	writeBatchTestEvent(t, writer, `{"Action":"pause","Time":"2026-08-05T00:00:00.002Z","Test":"TestCompileGroup"}`)
	writeBatchTestEvent(t, writer, `{"Action":"cont","Time":"2026-08-05T00:01:40Z","Test":"TestCompileGroup"}`)
	writeBatchTestEvent(t, writer, `{"Action":"pass","Time":"2026-08-05T00:01:40.005Z","Test":"TestCompileGroup","Elapsed":0.005}`)

	interval := writer.selectorIntervals[selector]
	if got := interval.completedAt.Sub(interval.runAt); got != 5*time.Millisecond {
		t.Fatalf("parallel selector body interval = %s, want 5ms", got)
	}
	if err := (compiledSelectorBatchObservation{selectorTimings: writer.selectorTimings, selectorIntervals: writer.selectorIntervals}).validateSelectorResults(map[GateID]compiledSelectorBatchSpec{"selector": {id: "selector", name: selector}}); err != nil {
		t.Fatalf("validate parallel selector result: %v", err)
	}
}

func TestCompiledSelectorBatchCanonicalizesTerminalDuration(t *testing.T) {
	base := time.UnixMilli(9_000_000).UTC()
	interval := compiledSelectorBatchInterval{runAt: base.Add(time.Millisecond), completedAt: base.Add(11 * time.Millisecond)}
	observation := compiledSelectorBatchObservation{started: base, log: newBoundedPlanLog(executorPlanMaxLogBytes)}
	timings := []GoTestTiming{{Name: "TestCompileGroup", Status: GoTestStatusPass, DurationMS: 7}}
	workload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	result, err := compiledSelectorResultWithLog(GateID(workload.ID), []string{"go", "tool", "test2json"}, observation, "TestCompileGroup", timings, interval, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ResultStatusPassed || len(result.TestTimings) != 1 || result.TestTimings[0].DurationMS != 7 {
		t.Fatalf("rounded selector result = status %s timings %#v", result.Status, result.TestTimings)
	}
	if result.ExecutionProfile.StartupMS != 1 || result.ExecutionProfile.TestBodyMS != 7 || result.ExecutionProfile.TotalMS != 8 {
		t.Fatalf("rounded selector profile = %#v", result.ExecutionProfile)
	}
}

func TestCompiledSelectorDiagnosticLogBoundsPassAndPreservesFailure(t *testing.T) {
	data := []byte(strings.Repeat("diagnostic-line\n", 256))
	passed := compiledSelectorDiagnosticLog(data, nil, false)
	if len(passed) == 0 || len(passed) > executorPlanSuccessfulSelectorLogBytes {
		t.Fatalf("passed selector log bytes = %d, want 1..%d", len(passed), executorPlanSuccessfulSelectorLogBytes)
	}
	failed := compiledSelectorDiagnosticLog(data, errors.New("failed"), true)
	if !bytes.Equal(failed, data) {
		t.Fatalf("failed selector log bytes = %d, want full %d-byte diagnostic window", len(failed), len(data))
	}
	additionalFailure := compiledSelectorDiagnosticLog(data, errors.New("failed"), false)
	if len(additionalFailure) == 0 || len(additionalFailure) > executorPlanSuccessfulSelectorLogBytes {
		t.Fatalf("additional failed selector log bytes = %d, want bounded diagnostic tail", len(additionalFailure))
	}
}

func TestCompileGroupBatchProcessEnvironmentBoundsArchtestHeap(t *testing.T) {
	base := []string{"PATH=/bin", "GOMEMLIMIT=off"}
	archtest := compileGroupBatchProcessEnvironment(append([]string(nil), base...), AtomicArchtestPackageTarget)
	if got := environmentValue(archtest, "GOMEMLIMIT"); got != "3GiB" {
		t.Fatalf("archtest GOMEMLIMIT = %q, want 3GiB", got)
	}
	superGate := compileGroupBatchProcessEnvironment(append([]string(nil), base...), AtomicSuperDolphinGatePackageTarget)
	if got := environmentValue(superGate, "GOMEMLIMIT"); got != "3GiB" {
		t.Fatalf("super-dolphin-gate GOMEMLIMIT = %q, want 3GiB", got)
	}
	ordinary := compileGroupBatchProcessEnvironment(append([]string(nil), base...), "./internal/example")
	if got := environmentValue(ordinary, "GOMEMLIMIT"); got != "off" {
		t.Fatalf("ordinary GOMEMLIMIT = %q, want inherited off", got)
	}
}

func TestEnsureCompileGroupBatchDirectoriesCreatesProviderHome(t *testing.T) {
	batchRoot := t.TempDir()
	if err := ensureCompileGroupBatchDirectories(batchRoot); err != nil {
		t.Fatalf("ensure compile group batch directories: %v", err)
	}
	providerHome := filepath.Join(batchRoot, "home", ".codex")
	info, err := os.Stat(providerHome)
	if err != nil {
		t.Fatalf("stat provider home %q: %v", providerHome, err)
	}
	if !info.IsDir() {
		t.Fatalf("provider home %q is not a directory", providerHome)
	}
}

func TestReplaceBatchedGateResultsPreservesResultsAfterCrossLaneFailure(t *testing.T) {
	first := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	second := mustCompileGroupBatchWorkload(t, "TestCompileGroupSecond")
	request := executorPlanRequest{
		profile: ProfileLocalFast, planDigest: "sha256:" + strings.Repeat("a", 64), shard: true,
		gateIDs: []GateID{GateID(first.ID), GateID(second.ID), GateIDFrontendLint},
	}
	firstResult := successfulPlanGateResult(GateID(first.ID))
	secondResult := successfulPlanGateResult(GateID(second.ID))
	laneFailureStarted := make(chan struct{})
	report, executionErr := executeGatePlanWithRunner(context.Background(), request, func(ctx context.Context, _ int, id GateID) (PlanGateExecution, error) {
		if id == GateIDFrontendLint {
			close(laneFailureStarted)
			failed := successfulPlanGateResult(id)
			failed.Status, failed.ExitCode = ResultStatusFailed, 1
			return failed, errors.New("cross-lane failure")
		}
		if id == firstResult.GateID {
			<-laneFailureStarted
			return firstResult, nil
		}
		return secondResult, nil
	}, time.Now)
	if executionErr == nil {
		t.Fatal("cross-lane failure was not returned")
	}
	mergedGates, mergeErr := replaceBatchedGateResults(request.gateIDs, report.Gates, map[GateID]PlanGateExecution{
		firstResult.GateID: firstResult, secondResult.GateID: secondResult,
	})
	if mergeErr != nil {
		t.Fatal(mergeErr)
	}
	if len(mergedGates) != len(request.gateIDs) || mergedGates[0].Status != ResultStatusPassed || mergedGates[1].Status != ResultStatusPassed {
		t.Fatalf("merged cross-lane batch results = %#v", mergedGates)
	}
}

func TestReplaceBatchedGateResultsDoesNotOverwriteObservedFailure(t *testing.T) {
	id := GateID("backend:test_with_guard_and_race::go-test::observed-failure")
	failed := successfulPlanGateResult(id)
	failed.Status, failed.ExitCode = ResultStatusFailed, 1
	passed := successfulPlanGateResult(id)
	merged, err := replaceBatchedGateResults([]GateID{id}, []PlanGateExecution{failed}, map[GateID]PlanGateExecution{id: passed})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].Status != ResultStatusFailed || merged[0].ExitCode != 1 {
		t.Fatalf("observed failure was overwritten by batched PASS: %#v", merged)
	}
}

func batchTestSelectorNames(specs map[GateID]compiledSelectorBatchSpec) map[string]struct{} {
	names := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		names[spec.name] = struct{}{}
	}
	return names
}

func writeBatchTestEvent(t *testing.T, writer *compiledSelectorBatchEventWriter, event string) {
	t.Helper()
	if _, err := writer.Write([]byte(event + "\n")); err != nil {
		t.Fatal(err)
	}
}

func mustCompileGroupBatchWorkload(t *testing.T, name string) Workload {
	t.Helper()
	return mustCompileGroupBatchWorkloadForPackage(t, "./sample", name)
}

func mustCompileGroupBatchWorkloadForPackage(t *testing.T, packageTarget, name string) Workload {
	t.Helper()
	target := GoTestTarget{Package: packageTarget, Name: name}
	if IsCanonicalGoTestHelper(target) {
		encoded, err := encodeGoTestTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		id, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoTest, encoded)
		if err != nil {
			t.Fatal(err)
		}
		return Workload{ID: id, Kind: WorkloadKindGoTest, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 10, Shardable: true}
	}
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, packageTarget, name, 10)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

// TestCompileGroup 是批量 selector fixture 的显式 helper 入口。
// super-dolphin-ci: helper
func TestCompileGroup(t *testing.T) {
	if os.Getenv("GO_WANT_COMPILE_GROUP_BATCH_HELPER") != "1" {
		t.Skip("compile group batch helper is only a subprocess fixture")
	}
	data, err := os.ReadFile("fixture.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "compile fixture\n" {
		t.Fatalf("fixture = %q", data)
	}
}

// TestCompileGroupSecond 是批量 selector fixture 的第二个 terminal helper。
// super-dolphin-ci: helper
func TestCompileGroupSecond(t *testing.T) {
	if os.Getenv("GO_WANT_COMPILE_GROUP_BATCH_HELPER") != "1" {
		t.Skip("compile group batch helper is only a subprocess fixture")
	}
}

func TestPrepareCompileGroupWorkRootCreatesTrustedEmptyDirectory(t *testing.T) {
	baseRoot := filepath.Join(canonicalCompileGroupTestTempDir(t), "work")
	groupID := digestPlanLog([]byte("compile-group-work-root"))
	workRoot, err := prepareCompileGroupWorkRoot(baseRoot, 3, groupID)
	if err != nil {
		t.Fatalf("prepareCompileGroupWorkRoot() error = %v", err)
	}
	expected := filepath.Join(baseRoot, "compile-groups", "group-000003-"+groupID[len("sha256:"):])
	if workRoot != expected {
		t.Fatalf("work root = %q, want %q", workRoot, expected)
	}
	if _, err := trustedDirectory(workRoot, true, os.Geteuid()); err != nil {
		t.Fatalf("created compile group work root is not trusted and empty: %v", err)
	}
}

func TestRunCompileGroupPreflightParsesPackageDirFromStdoutOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test command is Unix-specific")
	}
	sourceRoot := canonicalCompileGroupTestTempDir(t)
	packageDir := filepath.Join(sourceRoot, "sample")
	if err := os.Mkdir(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "fake-go")
	scriptBody := "#!/bin/sh\nif [ \"$1\" = \"list\" ]; then\n  printf '%s\\n' \"$FAKE_PACKAGE_DIR\"\n  printf '%s\\n' 'go list warning' >&2\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	log := new(bytes.Buffer)
	environment := append(os.Environ(), "FAKE_PACKAGE_DIR="+packageDir)
	got, err := runCompileGroupPreflight(context.Background(), script, "./sample", sourceRoot, environment, log)
	if err != nil {
		t.Fatal(err)
	}
	if got != packageDir {
		t.Fatalf("package directory = %q, want %q", got, packageDir)
	}
	if !strings.Contains(log.String(), "go list warning") {
		t.Fatalf("stderr diagnostic was not retained in bounded log: %q", log.String())
	}
}

func TestRunCompileGroupCommandSerializesSharedOutputWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test command is Unix-specific")
	}
	script := filepath.Join(t.TempDir(), "fake-go")
	scriptBody := "#!/bin/sh\nfor i in $(seq 1 128); do\n  printf 'stdout-%s\\n' \"$i\"\n  printf 'stderr-%s\\n' \"$i\" >&2\ndone\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	log := new(bytes.Buffer)
	started, completed, err := runCompileGroupCommand(context.Background(), script, []string{"fake-go", "compile"}, t.TempDir(), compileGroupTestEnvironment(t), time.Now, log)
	if err != nil {
		t.Fatalf("runCompileGroupCommand() error = %v", err)
	}
	if started.IsZero() || completed.Before(started) {
		t.Fatalf("compile command timing = %v..%v", started, completed)
	}
	for _, marker := range []string{"stdout-1", "stdout-128", "stderr-1", "stderr-128"} {
		if !strings.Contains(log.String(), marker) {
			t.Fatalf("shared output is missing %q: %q", marker, log.String())
		}
	}
}

func compileGroupTestModule(t *testing.T) (string, string, string) {
	t.Helper()
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	workDir := canonicalCompileGroupTestTempDir(t)
	module := []byte("module example.com/compilegroup\n\ngo 1.26.5\n")
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), module, 0o644); err != nil {
		t.Fatal(err)
	}
	sampleDir := filepath.Join(workDir, "sample")
	if err := os.Mkdir(sampleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testSource := []byte("package sample\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestCompileGroup(t *testing.T) {\n\tdata, err := os.ReadFile(\"fixture.txt\")\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif string(data) != \"compile fixture\\n\" {\n\t\tt.Fatalf(\"fixture = %q\", data)\n\t}\n}\n\nfunc TestCompileGroupSecond(t *testing.T) {}\n")
	if err := os.WriteFile(filepath.Join(sampleDir, "sample_test.go"), testSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sampleDir, "fixture.txt"), []byte("compile fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return goBinary, workDir, compileGroupTestBinaryPath(workDir)
}

func compileGroupTestEnvironment(t *testing.T) []string {
	t.Helper()
	goCache := filepath.Join(t.TempDir(), "go-cache")
	goModCache := filepath.Join(t.TempDir(), "go-mod-cache")
	goTemp := filepath.Join(t.TempDir(), "go-tmp")
	for _, directory := range []string{goCache, goModCache, goTemp} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return []string{
		"GOCACHE=" + goCache,
		"GOENV=off",
		"GOMODCACHE=" + goModCache,
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOTMPDIR=" + goTemp,
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
	}
}

func incrementingTestClock(base time.Time) func() time.Time {
	clockCalls := 0
	return func() time.Time {
		clockCalls++
		return base.Add(time.Duration(clockCalls) * time.Millisecond)
	}
}

func incrementingSubmillisecondTestClock(base time.Time) func() time.Time {
	clockCalls := 0
	return func() time.Time {
		clockCalls++
		return base.Add(time.Duration(clockCalls) * time.Microsecond)
	}
}

func assertSuccessfulCompileGroupObservation(t *testing.T, started, completed time.Time, binaryPath string, argv []string) {
	t.Helper()
	info, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		t.Fatalf("compiled test binary = mode %s size %d", info.Mode(), info.Size())
	}
	artifactDigest, err := fileSHA256(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	execution := CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digestPlanLog([]byte("successful-group")), ArtifactKey: digestPlanLog([]byte("successful-artifact")), PackageTarget: "./sample",
		WorkloadIDs:    []GateID{"guard::go-test::e30uY2h0ZXN0"},
		ArtifactSHA256: artifactDigest, ArtifactSize: info.Size(), Status: ResultStatusPassed, ExitCode: 0,
		CompileCommandDigest: digestCommandArgv(argv), ProfileDigest: digestPlanLog([]byte("profile")), ResourceClassID: "normal-medium",
	}
	setCompileGroupExecutionTiming(&execution, started, completed)
	if err := execution.Validate(); err != nil {
		t.Fatalf("successful go test -c observation is invalid: %v", err)
	}
	if execution.DurationMS != 1 || execution.CompletedAtUnixMS != execution.StartedAtUnixMS+1 {
		t.Fatalf("sub-millisecond compile interval = %#v, want one canonical millisecond", execution)
	}
}

func TestRunCompileGroupCommandStartFailureHasNoObservation(t *testing.T) {
	missingBinary := filepath.Join(t.TempDir(), "missing-go")
	started, completed, err := runCompileGroupCommand(context.Background(), missingBinary, []string{"go", "test", "-c", "."}, t.TempDir(), compileGroupTestEnvironment(t), time.Now, &bytes.Buffer{})
	if err == nil {
		t.Fatal("missing go binary unexpectedly started")
	}
	if !started.IsZero() || !completed.IsZero() {
		t.Fatalf("failed Cmd.Start fabricated observation %v..%v", started, completed)
	}
}

func TestRunCompileGroupCommandRejectsMissingClosedEnvironment(t *testing.T) {
	started, completed, err := runCompileGroupCommand(context.Background(), "/missing/go", []string{"go", "test", "-c", "."}, t.TempDir(), nil, time.Now, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "environment is required") {
		t.Fatalf("missing environment error = %v", err)
	}
	if !started.IsZero() || !completed.IsZero() {
		t.Fatalf("missing environment fabricated observation %v..%v", started, completed)
	}
}

func TestRunCompileGroupToolRejectsMissingClosedEnvironment(t *testing.T) {
	err := runCompileGroupToolWithStreams(context.Background(), "/missing/go", []string{"go", "list", "."}, t.TempDir(), nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "environment is required") {
		t.Fatalf("missing tool environment error = %v", err)
	}
}

func TestFinishFailedCompileGroupExecutionNormalizesSubmillisecondTiming(t *testing.T) {
	started := time.UnixMilli(4_000_000).Add(time.Microsecond)
	execution := CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digestPlanLog([]byte("failed-group")), ArtifactKey: digestPlanLog([]byte("failed-artifact")), PackageTarget: "./sample",
		WorkloadIDs: []GateID{"guard::go-test::e30uY2h0ZXN0"}, CompileCommandDigest: digestPlanLog([]byte("argv")), ProfileDigest: digestPlanLog([]byte("profile")), ResourceClassID: "normal-medium",
	}
	got := finishFailedCompileGroupExecution(execution, started, started.Add(time.Microsecond), errors.New("compile failed"))
	if err := got.Validate(); err != nil {
		t.Fatalf("normalized failed compile execution is invalid: %v", err)
	}
	if got.DurationMS != 1 || got.CompletedAtUnixMS != got.StartedAtUnixMS+1 {
		t.Fatalf("failed compile interval = %#v, want one canonical millisecond", got)
	}
}

func TestCompiledSelectorBodyStartsOnlyAfterSuccessfulStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test command is Unix-specific")
	}
	workDir := canonicalCompileGroupTestTempDir(t)
	script := filepath.Join(workDir, "selector")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := incrementingTestClock(time.UnixMilli(2_000_000))
	observation := runCompiledSelectorProcess(context.Background(), compiledGroupArtifact{
		layout: executorLayout{sourceCopy: workDir}, goBinary: script, packageDir: workDir,
	}, []string{"go", "tool", "test2json"}, now)
	if observation.err() != nil {
		t.Fatal(observation.err())
	}
	if observation.bodyStarted.IsZero() || !observation.bodyStarted.After(observation.started) {
		t.Fatalf("selector body started before successful process start: %v..%v", observation.started, observation.bodyStarted)
	}
	workload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	result, err := compiledSelectorResult(GateID(workload.ID), observation.argv, observation)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionProfile.StartupMS != 1 || result.ExecutionProfile.TestBodyMS != 1 || result.ExecutionProfile.TotalMS != 2 {
		t.Fatalf("selector timing = %#v, want startup=1 body=1 total=2", result.ExecutionProfile)
	}
}

func TestCompiledSelectorStartFailureDoesNotInventTestBody(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-start failure timing test is Unix-specific")
	}
	now := incrementingTestClock(time.UnixMilli(3_000_000))
	workDir := t.TempDir()
	observation := runCompiledSelectorProcess(context.Background(), compiledGroupArtifact{
		layout: executorLayout{sourceCopy: workDir}, goBinary: filepath.Join(t.TempDir(), "missing-selector"), packageDir: workDir,
	}, []string{"go", "tool", "test2json"}, now)
	if observation.err() == nil {
		t.Fatal("missing selector binary unexpectedly started")
	}
	if !observation.bodyStarted.IsZero() {
		t.Fatalf("failed selector start fabricated body start %v", observation.bodyStarted)
	}
	workload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	result, err := compiledSelectorResult(GateID(workload.ID), observation.argv, observation)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionProfile.TestBodyMS != 0 || result.ExecutionProfile.StartupMS != result.ExecutionProfile.TotalMS {
		t.Fatalf("failed selector timing invented test body: %#v", result.ExecutionProfile)
	}
}

func TestCompiledSelectorRejectsEmptyPackageDirectory(t *testing.T) {
	now := incrementingTestClock(time.UnixMilli(3_500_000))
	observation := runCompiledSelectorProcess(context.Background(), compiledGroupArtifact{
		layout: executorLayout{sourceCopy: t.TempDir()}, goBinary: filepath.Join(t.TempDir(), "missing-selector"),
	}, []string{"go", "tool", "test2json"}, now)
	if observation.err() == nil || !strings.Contains(observation.err().Error(), "package directory is required") {
		t.Fatalf("empty package directory error = %v", observation.err())
	}
	if !observation.bodyStarted.IsZero() {
		t.Fatalf("empty package directory fabricated body start %v", observation.bodyStarted)
	}
}

func TestCompiledSelectorRunsFromGoPackageDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell working-directory test is Unix-specific")
	}
	sourceRoot := canonicalCompileGroupTestTempDir(t)
	packageDir := filepath.Join(sourceRoot, "internal", "sample")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := compileGroupPackageDirectory(sourceRoot, packageDir)
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "pwd")
	script := filepath.Join(t.TempDir(), "selector")
	if err := os.WriteFile(script, []byte("#!/bin/sh\npwd > \"$CAPTURE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	observation := runCompiledSelectorProcess(context.Background(), compiledGroupArtifact{
		layout: executorLayout{sourceCopy: sourceRoot}, goBinary: script, packageDir: resolved,
		environment: append(os.Environ(), "CAPTURE="+capture),
	}, []string{"go", "tool", "test2json"}, incrementingTestClock(time.UnixMilli(5_000_000)))
	if observation.err() != nil {
		t.Fatal(observation.err())
	}
	workingDirectory, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(workingDirectory)) != resolved {
		t.Fatalf("compiled selector cwd = %q, want %q", strings.TrimSpace(string(workingDirectory)), resolved)
	}
}

func canonicalCompileGroupTestTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
