package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func executorPlanTestNow() time.Time {
	return time.Date(2026, time.July, 18, 3, 4, 5, 6000000, time.UTC)
}

func TestPrepareExecutorPlanGoBuildCacheSkipsNonGoShard(t *testing.T) {
	workRoot := realTempDir(t)
	cacheRoot, seedRoot, err := prepareExecutorPlanGoBuildCacheAt(
		[]GateID{GateIDWhitespaceCheck, GateIDFrontendLint},
		workRoot,
		filepath.Join(workRoot, "missing-generations"),
	)
	if err != nil {
		t.Fatalf("prepare non-Go shard cache: %v", err)
	}
	if cacheRoot != "" {
		t.Fatalf("non-Go shard cache root = %q, want empty", cacheRoot)
	}
	if seedRoot != "" {
		t.Fatalf("non-Go shard seed root = %q, want empty", seedRoot)
	}
	if _, err := os.Stat(filepath.Join(workRoot, "plan-go-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-Go shard unexpectedly prepared a Go cache: %v", err)
	}
}

func TestPrepareExecutorPlanGoBuildCacheUsesSingleImageLayerSeed(t *testing.T) {
	workRoot := realTempDir(t)
	imageSeedRoot := realTempDir(t)
	writeTestFile(t, filepath.Join(imageSeedRoot, "prewarmed"), "runner-cache\n", 0o600)
	cacheRoot, seedRoot, err := prepareExecutorPlanGoBuildCacheAt(
		[]GateID{GateIDBackendTestWithGuard, GateIDWhitespaceCheck},
		workRoot,
		imageSeedRoot,
	)
	if err != nil {
		t.Fatalf("prepare Go shard cache: %v", err)
	}
	if cacheRoot != filepath.Join(workRoot, "plan-go-cache") {
		t.Fatalf("Go shard cache root = %q", cacheRoot)
	}
	if seedRoot != imageSeedRoot {
		t.Fatalf("Go shard seed root = %q, want immutable image layer", seedRoot)
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("private Go shard cache contains %d copied entries, want zero", len(entries))
	}
}

func TestPrepareExecutorPlanGoBuildCacheRejectsMissingImageLayerSeed(t *testing.T) {
	workRoot := realTempDir(t)
	_, _, err := prepareExecutorPlanGoBuildCacheAt(
		[]GateID{GateIDBackendTestWithGuard},
		workRoot,
		filepath.Join(workRoot, "missing-image-seed"),
	)
	if err == nil || !strings.Contains(err.Error(), "Go build cache seed") {
		t.Fatalf("prepare Go shard cache error = %v, want missing image seed", err)
	}
}

func TestPreparePlanGateExecutionMetricsFollowGoSeedContract(t *testing.T) {
	vitestID, err := targetWorkloadID(GateIDFrontendTest, workloadTargetVitest, "src/features/prompt-history/model/promptHistoryController.test.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		id        GateID
		wantCache bool
	}{
		{name: "Vitest selector may invoke Go", id: GateID(vitestID), wantCache: true},
		{name: "frontend lint", id: GateIDFrontendLint, wantCache: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, program, err := executorProgramForWorkload(test.id)
			if err != nil {
				t.Fatal(err)
			}
			workRoot := t.TempDir()
			cacheRoot := t.TempDir()
			_, _, _, metricsPath, err := preparePlanGateExecutionAt(
				workRoot,
				0,
				test.id,
				program,
				nil,
				cacheRoot,
				ExecutorOCIProjectGoBuildCacheSeedRoot,
				executorPlanTestNow,
			)
			if err != nil {
				t.Fatal(err)
			}
			if (metricsPath != "") != test.wantCache {
				t.Fatalf("metrics path = %q, want cache observation=%t", metricsPath, test.wantCache)
			}
			if test.wantCache && filepath.Dir(metricsPath) != cacheRoot {
				t.Fatalf("metrics path = %q, want private cache root %q", metricsPath, cacheRoot)
			}
		})
	}
}

func TestPlanExecutionReportJSONFieldCoverage(t *testing.T) {
	for _, registration := range []struct {
		name     string
		producer reflect.Type
		fields   []string
	}{
		{
			name:     "gate execution",
			producer: reflect.TypeFor[PlanGateExecution](),
			fields: []string{
				"argv_digest", "completed_at", "execution_profile", "exit_code", "gate_id", "log", "log_digest", "shard_identity",
				"started_at", "status", "test_timings",
			},
		},
		{
			name:     "test timing",
			producer: reflect.TypeFor[GoTestTiming](),
			fields:   []string{"duration_ms", "name", "status"},
		},
	} {
		t.Run(registration.name, func(t *testing.T) {
			producer, err := JSONFieldNames(registration.producer)
			if err != nil {
				t.Fatalf("JSONFieldNames() error = %v", err)
			}
			missing, stale := FieldCoverageDiff(producer, registration.fields)
			if len(missing) != 0 || len(stale) != 0 {
				t.Fatalf("plan report JSON field coverage missing=%v stale=%v", missing, stale)
			}
			failFirstMissing, failFirstStale := FieldCoverageDiff(
				producer,
				append(append([]string(nil), registration.fields[1:]...), "stale_field"),
			)
			if len(failFirstMissing) != 1 || failFirstMissing[0] != registration.fields[0] ||
				len(failFirstStale) != 1 || failFirstStale[0] != "stale_field" {
				t.Fatalf("fail-first coverage missing=%v stale=%v", failFirstMissing, failFirstStale)
			}
		})
	}
}

func TestShardExecutorCommandRequiresCanonicalManifestArguments(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	workloadPlan := testWorkloadExecutionPlan(t, plan)
	shards, err := BuildContainerShardSetFromWorkloadPlan(plan, workloadPlan, shardTestDigest('a'), shardTestDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	argv := testShardManifestArgv(t, plan, shards.Shards[0], workloadPlan)
	assertStandaloneWorkerArgvPrefix(t, argv)
	request, err := parseExecutorPlanCommand(argv[2:])
	if err != nil {
		t.Fatalf("parse canonical shard command: %v", err)
	}
	if request.profile != plan.Profile || request.planDigest != plan.PlanDigest {
		t.Fatalf("parsed shard identity = %#v, want profile=%q digest=%q", request, plan.Profile, plan.PlanDigest)
	}
	if request.manifestPath != ExecutorShardExecutionManifestPath || request.manifestDigest == "" {
		t.Fatalf("解析的 manifest 身份 = %#v", request)
	}
	bad := slices.Clone(argv[2:])
	bad[6] = "/tmp/shard-execution-manifest.json"
	if _, err := parseExecutorPlanCommand(bad); err == nil {
		t.Fatal("shard command accepted a non-gate-owned manifest path")
	}
}

func TestContainerShardExecutorCommandAcceptsCoordinatorFrozenRequiredSubset(t *testing.T) {
	assertContainerShardExecutorSubset(t, mustBuildPlan(t, ProfileRelease))
}

func assertStandaloneWorkerArgvPrefix(t *testing.T, argv []string) {
	t.Helper()
	if len(argv) < 2 || argv[0] != containerGateBinary || argv[1] != containerWorkerNamespace {
		t.Fatalf("argv prefix = %v, want standalone gate worker", argv)
	}
}

func TestExecutorPlanUsesTwoIsolatedLanesAndCanonicalResults(t *testing.T) {
	request := testExecutorPlanRequest(t)
	tracker := newPlanConcurrencyTracker()
	report, err := executeGatePlanWithRunner(context.Background(), request, tracker.run, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if tracker.peak != executorPlanLaneCount {
		t.Fatalf("plan concurrency peak = %d, want %d", tracker.peak, executorPlanLaneCount)
	}
	if tracker.roots[0] == tracker.roots[1] {
		t.Fatalf("parallel lanes share work root %q", tracker.roots[0])
	}
	if len(report.Gates) != len(request.gateIDs) {
		t.Fatalf("plan results = %d, want %d", len(report.Gates), len(request.gateIDs))
	}
	for index, id := range request.gateIDs {
		if report.Gates[index].GateID != id {
			t.Fatalf("result %d gate = %q, want %q", index, report.Gates[index].GateID, id)
		}
	}
}

func TestRunExecutorPlanLaneStoresResultBeforeProfileValidation(t *testing.T) {
	id := GateID("backend:test_with_guard_and_race::go-test::invalid-profile")
	result := successfulPlanGateResult(id)
	result.ExecutionProfile.CacheSource = "invalid"
	results := make(map[GateID]PlanGateExecution)
	var resultsMu sync.Mutex
	err := runExecutorPlanLane(context.Background(), 0, []GateID{id}, results, &resultsMu, func(context.Context, int, GateID) (PlanGateExecution, error) {
		return result, nil
	})
	if err == nil || !strings.Contains(err.Error(), "execution profile") {
		t.Fatalf("invalid profile error = %v", err)
	}
	stored, ok := results[id]
	if !ok || stored.GateID != id || stored.ExecutionProfile.CacheSource != "invalid" {
		t.Fatalf("runner result was lost before profile validation: %#v", results)
	}
}

func TestExecutorPlanDAGSchedulesCanonicalGatesWithoutDuplicates(t *testing.T) {
	assertExecutorPlanSchedule(t, ProfileLocalFast, localFastExecutorPlanLanes())
	assertExecutorPlanSchedule(t, ProfileRelease, releaseExecutorPlanLanes())
}

func TestReleaseAttestationRunsAfterCanonicalPrerequisitesWithoutCommandRunner(t *testing.T) {
	request := testExecutorPlanRequestForProfile(t, ProfileRelease)
	var mu sync.Mutex
	called := make(map[GateID]int)
	report, err := executeGatePlanWithRunner(context.Background(), request,
		func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
			mu.Lock()
			called[id]++
			mu.Unlock()
			if id == GateIDReleaseLayeredCheck {
				return PlanGateExecution{}, errors.New("release attestation reached command runner")
			}
			return successfulPlanGateResult(id), nil
		}, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if called[GateIDReleaseLayeredCheck] != 0 || len(called) != len(request.gateIDs)-1 {
		t.Fatalf("command runner calls = %v", called)
	}
	attestation := report.Gates[len(report.Gates)-1]
	if attestation.GateID != GateIDReleaseLayeredCheck || attestation.Status != ResultStatusPassed ||
		!strings.Contains(string(attestation.Log), "prerequisite_digest=sha256:") ||
		!strings.Contains(string(attestation.Log), "plan_digest="+request.planDigest) {
		t.Fatalf("release attestation result = %#v", attestation)
	}
	assertReleaseAttestationExecutionProfile(t, attestation)
	if err := validatePlanExecutionReportGates(report, nil); err != nil {
		t.Fatalf("release report is not canonical: %v", err)
	}
}

func TestReleaseAttestationRejectsMissingOrTamperedPrerequisiteEvidence(t *testing.T) {
	request, prerequisites, canonical := canonicalReleaseAttestationEvidence(t)
	first := prerequisites[0]
	missing := maps.Clone(canonical)
	delete(missing, first)
	unexpected := maps.Clone(canonical)
	unexpected[GateIDReleaseLayeredCheck] = successfulPlanGateResult(GateIDReleaseLayeredCheck)
	failed := maps.Clone(canonical)
	failedResult := failed[first]
	failedResult.Status, failedResult.ExitCode = ResultStatusFailed, 1
	failed[first] = failedResult
	tampered := maps.Clone(canonical)
	tamperedResult := tampered[first]
	tamperedResult.Log = append(tamperedResult.Log, 'x')
	tampered[first] = tamperedResult
	tests := map[string]map[GateID]PlanGateExecution{
		"missing": missing, "unexpected": unexpected, "failed": failed, "tampered log": tampered,
	}
	for name, results := range tests {
		t.Run(name, func(t *testing.T) {
			assertReleaseAttestationRejected(t, request, results)
		})
	}
}

func TestReleaseAttestationDigestIsDeterministic(t *testing.T) {
	request, _, canonical := canonicalReleaseAttestationEvidence(t)
	firstResult, err := executeReleaseLayerAttestation(request, canonical, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := executeReleaseLayerAttestation(request, canonical, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstResult.Log, secondResult.Log) || firstResult.LogDigest != secondResult.LogDigest {
		t.Fatal("canonical release attestation is not deterministic")
	}
}

func canonicalReleaseAttestationEvidence(
	t *testing.T,
) (executorPlanRequest, []GateID, map[GateID]PlanGateExecution) {
	t.Helper()
	request := testExecutorPlanRequestForProfile(t, ProfileRelease)
	prerequisites, required, err := planExecutionPrerequisites(request)
	if err != nil {
		t.Fatal(err)
	}
	if !required {
		t.Fatal("release plan did not require the final attestation")
	}
	canonical := make(map[GateID]PlanGateExecution, len(prerequisites))
	for _, id := range prerequisites {
		canonical[id] = successfulPlanGateResult(id)
	}
	return request, prerequisites, canonical
}

func assertReleaseAttestationRejected(
	t *testing.T,
	request executorPlanRequest,
	results map[GateID]PlanGateExecution,
) {
	t.Helper()
	attestation, err := executeReleaseLayerAttestation(request, results, executorPlanTestNow)
	if err == nil {
		t.Fatal("tampered prerequisite evidence was accepted")
	}
	if attestation.Status != ResultStatusFailed || attestation.ExitCode != 1 {
		t.Fatalf("rejected attestation outcome = %#v", attestation)
	}
	if attestation.LogDigest != digestPlanLog(attestation.Log) {
		t.Fatalf("rejected attestation log digest = %q", attestation.LogDigest)
	}
	assertReleaseAttestationExecutionProfile(t, attestation)
}

func assertReleaseAttestationExecutionProfile(t *testing.T, result PlanGateExecution) {
	t.Helper()
	if err := result.ExecutionProfile.Validate(); err != nil {
		t.Fatalf("release attestation execution profile is invalid: %v", err)
	}
	if err := result.ExecutionProfile.ValidateAggregate(); err != nil {
		t.Fatalf("release attestation aggregate profile is invalid: %v", err)
	}
	if result.ExecutionProfile.StartupMS != releaseAttestationStartupMS ||
		result.ExecutionProfile.TestBodyMS != releaseAttestationTestBodyMS {
		t.Fatalf("release attestation phases = startup:%d body:%d, want 1ms each", result.ExecutionProfile.StartupMS, result.ExecutionProfile.TestBodyMS)
	}
	wantTotalMS := result.CompletedAt.Sub(result.StartedAt).Milliseconds()
	if result.ExecutionProfile.TotalMS != wantTotalMS {
		t.Fatalf("release attestation total_ms = %d, want timestamp interval %d", result.ExecutionProfile.TotalMS, wantTotalMS)
	}
}

func assertExecutorPlanLaneExactSet(t *testing.T, wanted []GateID, lanes [][]GateID) {
	t.Helper()
	seen := make(map[GateID]bool, len(wanted))
	for _, lane := range lanes {
		for _, id := range lane {
			if seen[id] || !slices.Contains(wanted, id) {
				t.Fatalf("plan lane gate %q is duplicate or unexpected", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != len(wanted) {
		t.Fatalf("plan lanes cover %d gates, want %d", len(seen), len(wanted))
	}
}

func TestExecutorPlanFailureCancelsCompanionLane(t *testing.T) {
	request := testExecutorPlanRequest(t)
	failure := errors.New("gate failed")
	runner := func(ctx context.Context, lane int, id GateID) (PlanGateExecution, error) {
		result := successfulPlanGateResult(id)
		if lane == 0 {
			return result, failure
		}
		<-ctx.Done()
		result.Status, result.ExitCode = classifyPlanGateOutcome(ctx.Err(), ctx.Err())
		return result, ctx.Err()
	}
	report, err := executeGatePlanWithRunner(context.Background(), request, runner, executorPlanTestNow)
	if !errors.Is(err, failure) {
		t.Fatalf("plan error = %v, want primary gate failure", err)
	}
	if len(report.Gates) == 0 {
		t.Fatal("failed plan omitted observed gate results")
	}
	if len(report.Gates) != len(request.gateIDs) {
		t.Fatalf("failed plan results = %d, want exact set %d", len(report.Gates), len(request.gateIDs))
	}
	for _, result := range report.Gates {
		if result.Status == ResultStatusTimeout {
			t.Fatalf("peer cancellation was misclassified as timeout: %#v", result)
		}
		if result.GateID == GateIDFrontendLint &&
			(result.Status != ResultStatusCancelled || result.ExitCode != -1) {
			t.Fatalf("running companion cancellation = %#v, want cancelled exit -1", result)
		}
	}
}

func TestClassifyPlanGateOutcomeUsesContextAsCancellationAuthority(t *testing.T) {
	exitOne := errors.New("gate exited 1")
	tests := []struct {
		name       string
		gateErr    error
		contextErr error
		status     ResultStatus
		exitCode   int
	}{
		{name: "passed", status: ResultStatusPassed, exitCode: 0},
		{name: "gate failure", gateErr: exitOne, status: ResultStatusFailed, exitCode: 1},
		{name: "peer cancellation", gateErr: exitOne, contextErr: context.Canceled, status: ResultStatusCancelled, exitCode: -1},
		{name: "profile deadline", gateErr: exitOne, contextErr: context.DeadlineExceeded, status: ResultStatusTimeout, exitCode: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, exitCode := classifyPlanGateOutcome(test.gateErr, test.contextErr)
			if status != test.status || exitCode != test.exitCode {
				t.Fatalf("outcome = (%q,%d), want (%q,%d)", status, exitCode, test.status, test.exitCode)
			}
		})
	}
}

func TestPlanGateFailureSummaryIsBoundedAndDoesNotEchoRawError(t *testing.T) {
	secret := "SUPER_SECRET_TOKEN=must-not-appear"
	summary := planGateFailureSummary(errors.New(secret), nil, ResultStatusFailed, 1)
	if strings.Contains(string(summary), secret) {
		t.Fatalf("failure summary leaked raw error: %q", summary)
	}
	for _, want := range []string{"status=failed", "exit_code=1", "reason=execution-error"} {
		if !strings.Contains(string(summary), want) {
			t.Fatalf("failure summary = %q, want %q", summary, want)
		}
	}
	log := newBoundedPlanLog(24)
	if _, err := log.Write(summary); err != nil {
		t.Fatalf("write bounded summary: %v", err)
	}
	if got := len(log.Bytes()); got != 24 {
		t.Fatalf("bounded summary length = %d, want 24", got)
	}
}

func TestBoundedPlanLogKeepsLatestDiagnosticWindow(t *testing.T) {
	log := newBoundedPlanLog(8)
	if _, err := log.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Write([]byte("67890")); err != nil {
		t.Fatal(err)
	}
	if got := string(log.Bytes()); got != "34567890" {
		t.Fatalf("bounded tail = %q, want %q", got, "34567890")
	}

	lineLog := newBoundedPlanLog(32 << 10)
	for index := range executorPlanMaxLogLines + 10 {
		if _, err := fmt.Fprintf(lineLog, "line-%03d\n", index); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(lineLog.Bytes()), "\n"), "\n")
	wantLast := fmt.Sprintf("line-%03d", executorPlanMaxLogLines+9)
	if len(lines) != executorPlanMaxLogLines || lines[0] != "line-010" || lines[len(lines)-1] != wantLast {
		t.Fatalf("bounded lines = first %q last %q count %d", lines[0], lines[len(lines)-1], len(lines))
	}
}

func TestBoundedPlanLogNormalizesBinaryProcessOutput(t *testing.T) {
	log := newBoundedPlanLog(32)
	if _, err := log.Write([]byte("prefix\x00\xffsuffix")); err != nil {
		t.Fatal(err)
	}
	got := log.Bytes()
	if !bytes.Contains(got, []byte(`\x00`)) || !bytes.Contains(got, []byte("\uFFFD")) || bytes.IndexByte(got, 0) >= 0 {
		t.Fatalf("normalized process log = %q, want escaped NUL and replacement rune", got)
	}
	if len(got) > 32 {
		t.Fatalf("normalized process log length = %d, want at most 32", len(got))
	}
}

func TestExecutorPlanDistinguishesDeadlineFromPeerCancellation(t *testing.T) {
	request := testExecutorPlanRequest(t)
	ctx, cancel := context.WithDeadline(context.Background(), executorPlanTestNow().Add(-time.Second))
	defer cancel()
	report, err := executeGatePlanWithRunner(ctx, request,
		func(context.Context, int, GateID) (PlanGateExecution, error) {
			t.Fatal("expired plan started a gate")
			return PlanGateExecution{}, nil
		}, executorPlanTestNow)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired plan error = %v", err)
	}
	for _, result := range report.Gates {
		if result.Status != ResultStatusTimeout {
			t.Fatalf("deadline result = %#v, want timeout", result)
		}
		if !result.StartedAt.Equal(executorPlanTestNow()) || !result.CompletedAt.Equal(executorPlanTestNow()) {
			t.Fatalf("deadline timestamps = (%s,%s), want canonical %s", result.StartedAt, result.CompletedAt, executorPlanTestNow())
		}
	}
}

func TestDecodePlanExecutionReportRejectsGateSetAndEvidenceDrift(t *testing.T) {
	request := testExecutorPlanRequest(t)
	report, err := executeGatePlanWithRunner(context.Background(), request,
		func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
			return successfulPlanGateResult(id), nil
		}, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*PlanExecutionReport){
		"missing":   func(value *PlanExecutionReport) { value.Gates = value.Gates[:len(value.Gates)-1] },
		"duplicate": func(value *PlanExecutionReport) { value.Gates[1] = value.Gates[0] },
		"unknown":   func(value *PlanExecutionReport) { value.Gates[0].GateID = GateID("unknown:gate") },
		"order":     func(value *PlanExecutionReport) { value.Gates[0], value.Gates[1] = value.Gates[1], value.Gates[0] },
		"status":    func(value *PlanExecutionReport) { value.Gates[0].Status = ResultStatus("unknown") },
		"exit":      func(value *PlanExecutionReport) { value.Gates[0].ExitCode = 9 },
		"cancel exit": func(value *PlanExecutionReport) {
			value.Gates[0].Status = ResultStatusCancelled
			value.Gates[0].ExitCode = 1
		},
		"time": func(value *PlanExecutionReport) {
			value.Gates[0].CompletedAt = value.Gates[0].StartedAt.Add(-time.Second)
		},
		"digest": func(value *PlanExecutionReport) { value.Gates[0].LogDigest = testDigest },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := clonePlanReport(t, report)
			mutate(&changed)
			chunks, encodeErr := EncodePlanExecutionReportChunks(changed)
			if encodeErr == nil {
				if _, err := DecodePlanExecutionReport(strings.Join(chunks, "\n")); err == nil {
					t.Fatal("decoder accepted drifted plan report")
				}
			}
		})
	}
	if _, err := DecodePlanExecutionReport(encodePlanReportForTest(t, report) + "\ntrailing"); err == nil {
		t.Fatal("decoder accepted trailing text")
	}
	if decoded, err := DecodePlanExecutionReport(encodePlanReportForTest(t, report)); err != nil {
		t.Fatalf("decode canonical report: %v", err)
	} else if !slices.EqualFunc(decoded.Gates, report.Gates, func(left, right PlanGateExecution) bool {
		return left.GateID == right.GateID
	}) {
		t.Fatal("decoded report gate order drifted")
	}
}

func TestPlanExecutionReportRoundTripsCurrentTestTimings(t *testing.T) {
	request := testExecutorPlanRequest(t)
	report, err := executeGatePlanWithRunner(context.Background(), request,
		func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
			return successfulPlanGateResult(id), nil
		}, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	report.Gates[0].TestTimings = []GoTestTiming{
		{Name: "TestFast", Status: GoTestStatusPass, DurationMS: 125},
		{Name: "TestSlow/subcase", Status: GoTestStatusFail, DurationMS: 1500},
	}
	decoded, err := DecodePlanExecutionReport(encodePlanReportForTest(t, report))
	if err != nil {
		t.Fatalf("decode report with test timings: %v", err)
	}
	if !slices.Equal(decoded.Gates[0].TestTimings, report.Gates[0].TestTimings) {
		t.Fatalf("decoded timings = %#v, want %#v", decoded.Gates[0].TestTimings, report.Gates[0].TestTimings)
	}
}

func TestPlanExecutionReportPacksTwentyFiveWorkloadsWithinRemoteRecordBudget(t *testing.T) {
	const (
		workloadCount    = 25
		timingsPerTarget = 80
	)
	now := executorPlanTestNow()
	report := PlanExecutionReport{
		SchemaVersion:    ExecutorPlanReportSchemaVersion,
		Profile:          ProfileLocalFast,
		PlanDigest:       testExecutorPlanRequest(t).planDigest,
		ExecutionOutcome: SuccessfulWorkerExecutionOutcome(),
		Gates:            make([]PlanGateExecution, 0, workloadCount),
	}
	expected := make([]GateID, 0, workloadCount)
	for workloadIndex := range workloadCount {
		workloadID, err := targetWorkloadID(
			GateIDBackendTestWithGuard,
			workloadTargetGoPackage,
			fmt.Sprintf("./internal/reportfixture/pkg%02d", workloadIndex),
		)
		if err != nil {
			t.Fatal(err)
		}
		gateID := GateID(workloadID)
		timings := make([]GoTestTiming, 0, timingsPerTarget)
		for timingIndex := range timingsPerTarget {
			timings = append(timings, GoTestTiming{
				Name:       fmt.Sprintf("TestShard%02dCase%03d", workloadIndex, timingIndex),
				Status:     GoTestStatusPass,
				DurationMS: int64(timingIndex + 1),
			})
		}
		report.Gates = append(report.Gates, PlanGateExecution{
			GateID: gateID, Status: ResultStatusPassed, ExitCode: 0,
			StartedAt: now, CompletedAt: now.Add(time.Second),
			LogDigest: digestPlanLog(nil), TestTimings: timings,
			ExecutionProfile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", TestBodyMS: 1000, TotalMS: 1000},
		})
		expected = append(expected, gateID)
	}
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) >= workloadCount*timingsPerTarget {
		t.Fatalf("packed report records = %d, want fewer than one record per timing", len(chunks))
	}
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, expected)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(decoded.Gates); got != workloadCount {
		t.Fatalf("decoded gates = %d, want %d", got, workloadCount)
	}
	for index, result := range decoded.Gates {
		if got := len(result.TestTimings); got != timingsPerTarget {
			t.Fatalf("decoded gate %d timings = %d, want %d", index, got, timingsPerTarget)
		}
	}
}

func TestPlanExecutionReportAllowsBoundedSuccessfulAttestationEvidence(t *testing.T) {
	result := successfulPlanGateResult(GateIDReleaseLayeredCheck)
	result.Log = []byte("prerequisite_digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n")
	result.LogDigest = digestPlanLog(result.Log)
	if !validPlanGateResult(result, ExecutorPlanReportSchemaVersion) {
		t.Fatal("bounded successful attestation evidence was rejected")
	}
}

func testExecutorPlanRequest(t *testing.T) executorPlanRequest {
	return testExecutorPlanRequestForProfile(t, ProfileLocalFast)
}

func testExecutorPlanRequestForProfile(t *testing.T, profile Profile) executorPlanRequest {
	t.Helper()
	plan := mustBuildPlan(t, profile)
	gateIDs := make([]GateID, len(plan.Gates))
	for index, spec := range plan.Gates {
		gateIDs[index] = spec.ID
	}
	return executorPlanRequest{profile: plan.Profile, planDigest: plan.PlanDigest, gateIDs: gateIDs}
}

type planConcurrencyTracker struct {
	mu      sync.Mutex
	running int
	peak    int
	roots   [executorPlanLaneCount]string
}

func newPlanConcurrencyTracker() *planConcurrencyTracker {
	return &planConcurrencyTracker{}
}

func (tracker *planConcurrencyTracker) run(_ context.Context, lane int, id GateID) (PlanGateExecution, error) {
	tracker.mu.Lock()
	tracker.running++
	if tracker.running > tracker.peak {
		tracker.peak = tracker.running
	}
	tracker.roots[lane] = executorPlanLaneRoot(ExecutorWorkRoot, lane)
	tracker.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	tracker.mu.Lock()
	tracker.running--
	tracker.mu.Unlock()
	return successfulPlanGateResult(id), nil
}

func successfulPlanGateResult(id GateID) PlanGateExecution {
	now := executorPlanTestNow()
	return PlanGateExecution{
		GateID: id, Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: now, CompletedAt: now.Add(time.Millisecond),
		LogDigest:        digestPlanLog(nil),
		ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "measured", TestBodyMS: 1, TotalMS: 1},
	}
}

func clonePlanReport(t *testing.T, report PlanExecutionReport) PlanExecutionReport {
	t.Helper()
	cloned, err := DecodePlanExecutionReport(encodePlanReportForTest(t, report))
	if err != nil {
		t.Fatal(err)
	}
	return cloned
}

func encodePlanReportForTest(t *testing.T, report PlanExecutionReport) string {
	t.Helper()
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(chunks, "\n")
}
