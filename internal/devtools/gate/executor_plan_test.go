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
	cacheRoot, seedRoots, err := prepareExecutorPlanGoBuildCacheAt(
		[]GateID{GateIDWhitespaceCheck, GateIDFrontendLint},
		workRoot,
		filepath.Join(workRoot, "missing-generations"),
		filepath.Join(workRoot, "missing-legacy"),
	)
	if err != nil {
		t.Fatalf("prepare non-Go shard cache: %v", err)
	}
	if cacheRoot != "" {
		t.Fatalf("non-Go shard cache root = %q, want empty", cacheRoot)
	}
	if len(seedRoots) != 0 {
		t.Fatalf("non-Go shard seed roots = %q, want empty", seedRoots)
	}
	if _, err := os.Stat(filepath.Join(workRoot, "plan-go-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-Go shard unexpectedly prepared a Go cache: %v", err)
	}
}

func TestPrepareExecutorPlanGoBuildCacheUsesGenerationSeeds(t *testing.T) {
	workRoot := realTempDir(t)
	seedGenerationsRoot := realTempDir(t)
	oldest := filepath.Join(seedGenerationsRoot, "00000000000000000034")
	newest := filepath.Join(seedGenerationsRoot, "00000000000000000035")
	for _, root := range []string{oldest, newest} {
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(root, "prewarmed"), "runner-cache\n", 0o600)
	}
	cacheRoot, seedRoots, err := prepareExecutorPlanGoBuildCacheAt(
		[]GateID{GateIDBackendTestWithGuard, GateIDWhitespaceCheck},
		workRoot,
		seedGenerationsRoot,
		filepath.Join(workRoot, "missing-legacy"),
	)
	if err != nil {
		t.Fatalf("prepare Go shard cache: %v", err)
	}
	if cacheRoot != filepath.Join(workRoot, "plan-go-cache") {
		t.Fatalf("Go shard cache root = %q", cacheRoot)
	}
	if !slices.Equal(seedRoots, []string{newest, oldest}) {
		t.Fatalf("Go shard seed roots = %q, want newest-to-oldest", seedRoots)
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("private Go shard cache contains %d copied entries, want zero", len(entries))
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
				"argv_digest", "completed_at", "execution_profile", "exit_code", "gate_id", "log", "log_digest",
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

func TestPlanExecutorCommandRequiresCanonicalProfileGateSet(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	argv, err := PlanExecutorArgv(plan)
	if err != nil {
		t.Fatal(err)
	}
	assertStandaloneWorkerArgvPrefix(t, argv)
	request, err := parseExecutorPlanCommand(argv[2:])
	if err != nil {
		t.Fatalf("parse canonical plan command: %v", err)
	}
	if request.profile != plan.Profile || request.planDigest != plan.PlanDigest {
		t.Fatalf("parsed plan identity = %#v, want profile=%q digest=%q", request, plan.Profile, plan.PlanDigest)
	}
	want := make([]GateID, len(plan.Gates))
	for index, spec := range plan.Gates {
		want[index] = spec.ID
	}
	if !slices.Equal(request.gateIDs, want) {
		t.Fatalf("parsed gate IDs = %v, want %v", request.gateIDs, want)
	}
	bad := slices.Clone(argv[2:])
	bad[6] = string(GateIDWhitespaceCheck)
	if _, err := parseExecutorPlanCommand(bad); err == nil {
		t.Fatal("plan command accepted incomplete gate set")
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

func TestPlanExecutionReportRoundTripsTestTimingsAndAcceptsLegacyV1(t *testing.T) {
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

	legacyTiming := clonePlanReport(t, report)
	legacyTiming.SchemaVersion = executorPlanTimingSchemaVersion
	if _, err := DecodePlanExecutionReport(encodePlanReportForTest(t, legacyTiming)); err != nil {
		t.Fatalf("decode legacy v2 timing report: %v", err)
	}

	legacy := clonePlanReport(t, report)
	legacy.SchemaVersion = 1
	for index := range legacy.Gates {
		legacy.Gates[index].TestTimings = nil
	}
	if _, err := DecodePlanExecutionReport(encodePlanReportForTest(t, legacy)); err != nil {
		t.Fatalf("decode legacy v1 report: %v", err)
	}
}

func TestPlanExecutionReportPacksTwentyFiveWorkloadsWithinRemoteRecordBudget(t *testing.T) {
	const (
		workloadCount    = 25
		timingsPerTarget = 80
	)
	now := executorPlanTestNow()
	report := PlanExecutionReport{
		SchemaVersion: executorPlanReportSchemaVersion,
		Profile:       ProfileLocalFast,
		PlanDigest:    testExecutorPlanRequest(t).planDigest,
		Gates:         make([]PlanGateExecution, 0, workloadCount),
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
	if !validPlanGateResult(result, executorPlanReportSchemaVersion) {
		t.Fatal("bounded successful attestation evidence was rejected")
	}
}

func TestPlanExecutionReportChunksRejectIncompleteOrForgedFrames(t *testing.T) {
	report, chunks := multiChunkPlanReport(t)
	if decoded, err := DecodePlanExecutionReportChunks(chunks); err != nil || decoded.PlanDigest != report.PlanDigest {
		t.Fatalf("decode canonical chunks: report=%#v err=%v", decoded, err)
	}
	other := clonePlanReport(t, report)
	other.Gates[0].Log = append(other.Gates[0].Log, 'x')
	other.Gates[0].LogDigest = digestPlanLog(other.Gates[0].Log)
	otherChunks, err := EncodePlanExecutionReportChunks(other)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func([]string) []string{
		"missing": func(value []string) []string { return value[:len(value)-1] },
		"duplicate": func(value []string) []string {
			return append(value[:1], append([]string{value[0]}, value[1:]...)...)
		},
		"reorder": func(value []string) []string {
			value[0], value[1] = value[1], value[0]
			return value
		},
		"mixed": func(value []string) []string {
			value[1] = otherChunks[1]
			return value
		},
		"tamper": func(value []string) []string {
			last := len(value) - 1
			replacement := "A"
			if strings.HasSuffix(value[last], replacement) {
				replacement = "B"
			}
			value[last] = value[last][:len(value[last])-1] + replacement
			return value
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePlanExecutionReportChunks(mutate(slices.Clone(chunks))); err == nil {
				t.Fatal("decoder accepted forged report chunks")
			}
		})
	}
}

func TestPlanExecutionReportChunksStayBelowRemoteLogLineLimit(t *testing.T) {
	_, chunks := multiChunkPlanReport(t)
	for index, chunk := range chunks {
		if len(chunk)+1 > executorPlanReportMaxLineBytes {
			t.Fatalf("chunk %d line bytes = %d, want <= %d", index, len(chunk)+1, executorPlanReportMaxLineBytes)
		}
	}
}

func TestPlanExecutionReportUsesPlainTextLogRecords(t *testing.T) {
	report, _ := multiChunkPlanReport(t)
	log := []byte("plain text log /tmp/work\\cache\r\n下一行\n")
	report.Gates[0].Log = log
	report.Gates[0].LogDigest = digestPlanLog(log)
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	wire := strings.Join(chunks, "\n")
	if !strings.Contains(wire, `plain text log /tmp/work\\cache\r\n下一行\n`) {
		t.Fatalf("plain log text is not visible in wire records: %q", wire)
	}
	if strings.Contains(wire, `"schema_version"`) || strings.Contains(wire, `"log"`) {
		t.Fatalf("wire report unexpectedly contains JSON fields: %q", wire)
	}
	decoded, err := DecodePlanExecutionReportChunks(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.Gates[0].Log, log) {
		t.Fatalf("decoded log = %q, want %q", decoded.Gates[0].Log, log)
	}
}

func TestPlanExecutionReportChunksAcceptOnlyCoordinatorFrozenDynamicGateSet(t *testing.T) {
	report, _ := multiChunkPlanReport(t)
	report.Gates = []PlanGateExecution{report.Gates[0], report.Gates[len(report.Gates)-1]}
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	expected := []GateID{report.Gates[0].GateID, report.Gates[1].GateID}
	if _, err := DecodePlanExecutionReportChunks(chunks); err == nil {
		t.Fatal("legacy decoder accepted a non-canonical dynamic shard")
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(chunks, expected); err != nil {
		t.Fatalf("dynamic shard decoder error = %v", err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(chunks, slices.Clone(expected[:1])); err == nil {
		t.Fatal("dynamic shard decoder accepted a different expected gate set")
	}
}

func multiChunkPlanReport(t *testing.T) (PlanExecutionReport, []string) {
	t.Helper()
	request := testExecutorPlanRequest(t)
	report, err := executeGatePlanWithRunner(context.Background(), request,
		func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
			result := successfulPlanGateResult(id)
			result.Status = ResultStatusFailed
			result.ExitCode = 1
			result.Log = bytes.Repeat([]byte(string(id)+"\n"), 400)
			result.LogDigest = digestPlanLog(result.Log)
			return result, nil
		}, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("plan report chunks = %d, want at least 3", len(chunks))
	}
	return report, chunks
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
		ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured", TestBodyMS: 1, TotalMS: 1},
	}
}

func TestExecutionProfileUsesOnlyExactTopLevelTestTiming(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	started := executorPlanTestNow()
	completed := started.Add(1500 * time.Millisecond)
	profile, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 400}, {Name: "TestBoundary/subcase", Status: GoTestStatusPass, DurationMS: 900}}, started, completed, nil)
	if err != nil || profile.TestBodyMS != 400 || profile.StartupMS != 1100 {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	if _, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 400}, {Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 401}}, started, completed, nil); err == nil {
		t.Fatal("duplicate top-level timing was accepted")
	}
	if _, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 1501}}, started, completed, nil); err == nil {
		t.Fatal("overlong top-level timing was accepted")
	}
}

func TestFrontendExecutionProfileDoesNotInferNPMCacheHit(t *testing.T) {
	started := executorPlanTestNow()
	completed := started.Add(1500 * time.Millisecond)
	profile, err := executionProfileForGate(
		GateIDFrontendLint,
		ExecutorProgram{NeedsFrontendSeed: true},
		nil,
		started,
		completed,
		&executorExecutionTiming{setupMS: 500, bodyMS: 1000, totalMS: 1500},
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Frontend == nil || profile.Frontend.NPMCacheHit ||
		profile.Frontend.NPMCacheNotApplicableReason != "npm_cache_lookup_not_observed" {
		t.Fatalf("frontend npm cache evidence = %#v", profile.Frontend)
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
