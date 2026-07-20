package gate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func executorPlanTestNow() time.Time {
	return time.Date(2026, time.July, 18, 3, 4, 5, 6000000, time.UTC)
}

func TestPlanExecutorCommandRequiresCanonicalProfileGateSet(t *testing.T) {
	plan := mustBuildPlan(t, ProfileLocalFast)
	argv, err := PlanExecutorArgv(plan)
	if err != nil {
		t.Fatal(err)
	}
	request, err := parseExecutorPlanCommand(argv[1:])
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
	bad := slices.Clone(argv[1:])
	bad[6] = string(GateIDWhitespaceCheck)
	if _, err := parseExecutorPlanCommand(bad); err == nil {
		t.Fatal("plan command accepted incomplete gate set")
	}
}

func TestContainerShardExecutorCommandRequiresOneExactCanonicalShard(t *testing.T) {
	plan := mustBuildPlan(t, ProfileRelease)
	shards, err := BuildContainerShardSet(plan, shardTestDigest('a'), shardTestDigest('b'))
	if err != nil {
		t.Fatal(err)
	}
	argv, err := ContainerShardExecutorArgv(plan, shards.Shards[0].GateIDs)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseExecutorPlanCommand(argv[1:])
	if err != nil || !parsed.shard || !slices.Equal(parsed.gateIDs, shards.Shards[0].GateIDs) {
		t.Fatalf("parse shard argv = %#v, %v", parsed, err)
	}
	badRawShard := slices.Clone(argv[1:])
	badRawShard[6] = string(GateIDWhitespaceCheck)
	if _, err := parseExecutorPlanCommand(badRawShard); err == nil {
		t.Fatal("parser accepted a forged shard gate list")
	}
	for _, gates := range [][]GateID{
		append(slices.Clone(shards.Shards[0].GateIDs), GateIDReleaseLayeredCheck),
		shards.Shards[0].GateIDs[1:],
		append(slices.Clone(shards.Shards[0].GateIDs), shards.Shards[0].GateIDs[0]),
	} {
		if _, err := ContainerShardExecutorArgv(plan, gates); err == nil {
			t.Fatalf("ContainerShardExecutorArgv accepted forged shard gates %v", gates)
		}
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

func TestExecutorPlanDAGSchedulesBackendAfterLSPWithoutDuplicates(t *testing.T) {
	tests := []struct {
		profile Profile
		lanes   [][]GateID
	}{
		{ProfileLocalFast, [][]GateID{
			{GateIDAIMaintenanceSelfTest, GateIDFrontendTest, GateIDLSPChangedDiagnostics, GateIDBackendTestWithGuard},
			{GateIDFrontendLint, GateIDFrontendBuild, GateIDFrontendEmbedVerify, GateIDSQLCVerify, GateIDCodemapCheck,
				GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
		}},
		{ProfileRelease, [][]GateID{
			{GateIDAIMaintenanceSelfTest, GateIDFrontendTest, GateIDLSPChangedDiagnostics, GateIDBackendTestWithGuard,
				GateIDBackendTestGuardWithRace, GateIDBackendNilness},
			{GateIDFrontendLint, GateIDFrontendBuild, GateIDFrontendEmbedVerify, GateIDSQLCVerify, GateIDCodemapCheck,
				GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
		}},
	}
	for _, test := range tests {
		t.Run(string(test.profile), func(t *testing.T) {
			request := testExecutorPlanRequestForProfile(t, test.profile)
			prerequisites, requiresAttestation, err := planExecutionPrerequisites(request)
			if err != nil {
				t.Fatal(err)
			}
			if requiresAttestation != (test.profile == ProfileRelease) {
				t.Fatalf("release attestation requirement = %t", requiresAttestation)
			}
			lanes, err := executorPlanLanes(prerequisites)
			if err != nil {
				t.Fatal(err)
			}
			assertExecutorPlanLaneExactSet(t, prerequisites, lanes)
			if len(lanes) != len(test.lanes) {
				t.Fatalf("lane count = %d, want %d", len(lanes), len(test.lanes))
			}
			for index, want := range test.lanes {
				if !slices.Equal(lanes[index], want) {
					t.Fatalf("lane %d = %v, want %v", index, lanes[index], want)
				}
			}
		})
	}
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
	if err := validatePlanExecutionReportGates(report); err != nil {
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
			if _, err := DecodePlanExecutionReport(encodePlanReportForTest(t, changed)); err == nil {
				t.Fatal("decoder accepted drifted plan report")
			}
		})
	}
	trailing := encodePlanReportForTest(t, report)
	decoded, err := base64.StdEncoding.DecodeString(trailing)
	if err != nil {
		t.Fatal(err)
	}
	decoded = append(decoded, []byte("{}")...)
	if _, err := DecodePlanExecutionReport(base64.StdEncoding.EncodeToString(decoded)); err == nil {
		t.Fatal("decoder accepted trailing JSON")
	}
	if decoded, err := DecodePlanExecutionReport(encodePlanReportForTest(t, report)); err != nil {
		t.Fatalf("decode canonical report: %v", err)
	} else if !slices.EqualFunc(decoded.Gates, report.Gates, func(left, right PlanGateExecution) bool {
		return left.GateID == right.GateID
	}) {
		t.Fatal("decoded report gate order drifted")
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

func multiChunkPlanReport(t *testing.T) (PlanExecutionReport, []string) {
	t.Helper()
	request := testExecutorPlanRequest(t)
	report, err := executeGatePlanWithRunner(context.Background(), request,
		func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
			result := successfulPlanGateResult(id)
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
	log := []byte("passed\n")
	return PlanGateExecution{
		GateID: id, Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: now, CompletedAt: now.Add(time.Millisecond),
		Log: log, LogDigest: digestPlanLog(log),
	}
}

func clonePlanReport(t *testing.T, report PlanExecutionReport) PlanExecutionReport {
	t.Helper()
	encoded := encodePlanReportForTest(t, report)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var cloned PlanExecutionReport
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func encodePlanReportForTest(t *testing.T, report PlanExecutionReport) string {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(data)
}
