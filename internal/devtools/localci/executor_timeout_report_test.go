package localci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type timeoutKillFixture struct {
	runner      *FreshContainerRunner
	stub        *freshDockerRunnerStub
	request     FreshContainerRequest
	startedAt   time.Time
	deadline    time.Time
	exitedAt    time.Time
	completedAt time.Time
}

func TestRunFreshContainerTimeoutKillsAndRemoves(t *testing.T) {
	fixture := setupTimeoutKillFixture(t)
	result, runErr := runTimeoutKillFixture(fixture)
	assertTimeoutKillResult(t, result, runErr)
	assertTimeoutKillTimeline(t, fixture, result)
	assertTimeoutKillDockerCalls(t, fixture.stub)
}

func setupTimeoutKillFixture(t *testing.T) timeoutKillFixture {
	t.Helper()
	runner, stub, request := freshContainerFixture(t)
	fixture := timeoutKillFixture{
		runner: runner, stub: stub, request: request,
		startedAt: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
	fixture.deadline = fixture.startedAt.Add(executionTimeout(false))
	fixture.exitedAt = fixture.deadline.Add(time.Nanosecond)
	fixture.completedAt = fixture.exitedAt.Add(time.Second)
	clockCalls := 0
	runner.now = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return fixture.startedAt
		}
		return fixture.completedAt
	}
	stub.finishedAt = fixture.exitedAt.Format(time.RFC3339Nano)
	stub.waitForCancel = true
	return fixture
}

func runTimeoutKillFixture(fixture timeoutKillFixture) (FreshContainerResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	return fixture.runner.RunFreshContainer(ctx, fixture.request)
}

func assertTimeoutKillResult(t *testing.T, result FreshContainerResult, runErr error) {
	t.Helper()
	if !errors.Is(runErr, context.DeadlineExceeded) || result.Status != gate.ResultStatusTimeout {
		t.Fatalf("result = %#v, err = %v", result, runErr)
	}
	if !result.Killed || !result.Container.Removed || result.KillProofDigest == "" || result.GateResult != nil {
		t.Fatalf("timeout result = %#v", result)
	}
}

func assertTimeoutKillTimeline(t *testing.T, fixture timeoutKillFixture, result FreshContainerResult) {
	t.Helper()
	if !result.StartedAt.Equal(fixture.startedAt) || !result.Deadline.Equal(fixture.deadline) {
		t.Fatalf("timeout start/deadline = %s/%s", result.StartedAt, result.Deadline)
	}
	if !result.ExitedAt.Equal(fixture.exitedAt) || !result.CompletedAt.Equal(fixture.completedAt) ||
		result.ExitedAt.Before(fixture.deadline) || result.CompletedAt.Before(result.ExitedAt) {
		t.Fatalf("timeout timeline = started %s deadline %s exited %s completed %s", result.StartedAt, result.Deadline, result.ExitedAt, result.CompletedAt)
	}
}

func assertTimeoutKillDockerCalls(t *testing.T, stub *freshDockerRunnerStub) {
	t.Helper()
	if !calledDockerCommand(stub.calls, "kill", testContainerID) || !calledDockerCommand(stub.calls, "rm", "--force", testContainerID) {
		t.Fatalf("Docker calls = %#v", stub.calls)
	}
}

func TestRunFreshContainerTimeoutShardSynthesizesMissingReportCoverage(t *testing.T) {
	result, runErr, request := runTimeoutShard(t, nil)
	assertTimeoutShardCoverage(t, result, runErr, request)
}

func TestTimeoutShardMissingReportRejectsInvalidTerminalEvidence(t *testing.T) {
	result, _, request := runTimeoutShard(t, nil)
	result.PlanGateResults = nil
	tests := []struct {
		name   string
		mutate func(*FreshContainerResult)
	}{
		{name: "exit before deadline", mutate: func(value *FreshContainerResult) { value.ExitedAt = value.Deadline.Add(-time.Nanosecond) }},
		{name: "not killed", mutate: func(value *FreshContainerResult) { value.Killed = false }},
		{name: "not removed", mutate: func(value *FreshContainerResult) { value.Container.Removed = false }},
		{name: "raw log digest", mutate: func(value *FreshContainerResult) { value.LogDigest = digest("a") }},
		{name: "kill digest", mutate: func(value *FreshContainerResult) { value.KillProofDigest = digest("b") }},
		{name: "removal digest", mutate: func(value *FreshContainerResult) { value.RemovalProofDigest = digest("c") }},
		{name: "terminal inspect", mutate: func(value *FreshContainerResult) {
			value.Evidence[len(value.Evidence)-2].Digest = value.RemovalProofDigest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := result
			invalid.Evidence = append([]gate.Evidence(nil), result.Evidence...)
			test.mutate(&invalid)
			if err := collectTerminalPlanGateResults(&invalid, request); err == nil {
				t.Fatal("invalid timeout terminal evidence was accepted")
			}
			if len(invalid.PlanGateResults) != 0 {
				t.Fatalf("invalid timeout synthesized gate results: %#v", invalid.PlanGateResults)
			}
		})
	}
}

func TestRunFreshContainerTimeoutShardRejectsNonMissingReportErrors(t *testing.T) {
	tests := []struct {
		name string
		log  func(*testing.T, FreshContainerRequest) string
	}{
		{name: "forged", log: func(t *testing.T, request FreshContainerRequest) string {
			report := canonicalShardReport(request, []byte(strings.Repeat("forged timeout report\n", 200)))
			report.PlanDigest = digest("f")
			return string(timestampedPlanReportLog(t, report))
		}},
		{name: "malformed", log: func(*testing.T, FreshContainerRequest) string {
			return "2026-07-20T00:00:00Z " + gate.ExecutorPlanReportChunkPrefix + "malformed\n"
		}},
		{name: "partial", log: func(t *testing.T, request FreshContainerRequest) string {
			report := canonicalShardReport(request, []byte(strings.Repeat("partial timeout report\n", 200)))
			report.Gates = report.Gates[:len(report.Gates)-1]
			return timestampPlanReport(t, report)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, runErr, _ := runTimeoutShard(t, test.log)
			if runErr == nil || result.Status != gate.ResultStatusInfraFailed || !result.Killed || !result.Container.Removed {
				t.Fatalf("invalid timeout report result=%#v err=%v", result, runErr)
			}
			if len(result.PlanGateResults) != 0 {
				t.Fatalf("invalid timeout report synthesized gate results: %#v", result.PlanGateResults)
			}
		})
	}
}

func runTimeoutShard(t *testing.T, reportLog func(*testing.T, FreshContainerRequest) string) (FreshContainerResult, error, FreshContainerRequest) {
	t.Helper()
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	startedAt := time.Now().UTC()
	deadline := startedAt.Add(10 * time.Millisecond)
	exitedAt := deadline.Add(time.Millisecond)
	completedAt := exitedAt.Add(time.Millisecond)
	clockCalls := 0
	runner.now = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			return startedAt
		}
		return completedAt
	}
	request.ClaimDeadline = func(context.Context, time.Time) (time.Time, error) { return deadline, nil }
	stub.request = request
	stub.waitForCancel = true
	stub.finishedAt = exitedAt.Format(time.RFC3339Nano)
	if reportLog != nil {
		stub.logOutput = reportLog(t, request)
	}
	result, err := runner.RunFreshContainer(context.Background(), request)
	return result, err, request
}

func timestampPlanReport(t *testing.T, report gate.PlanExecutionReport) string {
	t.Helper()
	chunks, err := gate.EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for _, chunk := range chunks {
		output.WriteString("2026-07-20T00:00:00Z ")
		output.WriteString(chunk)
		output.WriteByte('\n')
	}
	return output.String()
}

func assertTimeoutShardCoverage(t *testing.T, result FreshContainerResult, runErr error, request FreshContainerRequest) {
	t.Helper()
	assertTimeoutShardTerminal(t, result, runErr)
	if len(result.PlanGateResults) != len(request.ShardGateIDs) {
		t.Fatalf("timeout shard coverage=%d want=%d", len(result.PlanGateResults), len(request.ShardGateIDs))
	}
	for index, gateID := range request.ShardGateIDs {
		assertTimeoutGateCoverage(t, gateID, result.PlanGateResults[index], result)
	}
}

func assertTimeoutShardTerminal(t *testing.T, result FreshContainerResult, runErr error) {
	t.Helper()
	if !errors.Is(runErr, context.DeadlineExceeded) || result.Status != gate.ResultStatusTimeout || !result.Killed || !result.Container.Removed {
		t.Fatalf("timeout shard result=%#v err=%v", result, runErr)
	}
}

func assertTimeoutGateCoverage(t *testing.T, gateID gate.GateID, observed FreshPlanGateResult, result FreshContainerResult) {
	t.Helper()
	if observed.GateResult.GateID != string(gateID) || observed.Status != gate.ResultStatusTimeout ||
		observed.GateResult.Status != gate.GateStatusTimeout || observed.GateResult.ExitCode != -1 {
		t.Fatalf("timeout gate %q=%#v", gateID, observed)
	}
	assertTimeoutGateLogEvidence(t, gateID, observed.LogOutput, result)
	if observed.GateResult.LogDigest != digestBytes(observed.LogOutput) {
		t.Fatalf("timeout gate %q log digest drifted", gateID)
	}
	if err := observed.GateResult.Validate(); err != nil {
		t.Fatalf("timeout gate %q: %v", gateID, err)
	}
}

func assertTimeoutGateLogEvidence(t *testing.T, gateID gate.GateID, logOutput []byte, result FreshContainerResult) {
	t.Helper()
	log := string(logOutput)
	for _, evidence := range []string{
		result.LogDigest,
		result.Deadline.Format(time.RFC3339Nano),
		result.ExitedAt.Format(time.RFC3339Nano),
		result.KillProofDigest,
		result.RemovalProofDigest,
	} {
		if !strings.Contains(log, evidence) {
			t.Fatalf("timeout gate %q log omitted %q: %s", gateID, evidence, log)
		}
	}
}
