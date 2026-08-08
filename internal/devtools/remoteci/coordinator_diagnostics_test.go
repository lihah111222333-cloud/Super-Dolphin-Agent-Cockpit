package remoteci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestFailedRemoteGateErrorCapturesProviderFailureWithPassingReport(t *testing.T) {
	exitCode := int64(137)
	evidence := &gate.RemoteCITerminalEvidence{
		Containers:     []gate.RemoteCIContainerTerminalEvidence{{Name: "worker", State: "Terminated", ExitCode: &exitCode, Reason: "OOMKilled", Message: "memory limit exceeded"}},
		InitContainers: []gate.RemoteCIContainerTerminalEvidence{{Name: "materializer", State: "Terminated", Reason: "Completed"}},
		Events:         []gate.RemoteCIEventEvidence{{Type: "Warning", Reason: "BackOff", Message: "worker exited", Count: 2, LastTimestamp: "2026-08-07T00:00:00Z"}},
	}
	shard := ShardResult{
		ShardIdentity:    "sha256:provider-failed",
		ContainerStatus:  "Failed",
		TerminalEvidence: evidence,
		Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{
			GateID: "gate:all-pass", Status: gate.ResultStatusPassed,
			ExecutionProfile: gate.ExecutionProfile{StartupMS: 2, TestBodyMS: 8, TotalMS: 10},
		}}},
	}
	err := failedRemoteGateError([]ShardResult{shard})
	if !errors.Is(err, ErrGateFailed) {
		t.Fatalf("error chain = %v", err)
	}
	message := err.Error()
	for _, want := range []string{"container_status=\"Failed\"", "exit_code=137", "OOMKilled", "BackOff", "failed_shards=1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("provider failure diagnostic %q does not contain %q", message, want)
		}
	}
	receipt, err := json.Marshal(shard)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"terminal_evidence", "OOMKilled", "BackOff"} {
		if !strings.Contains(string(receipt), want) {
			t.Fatalf("receipt %q does not contain %q", receipt, want)
		}
	}
}

func TestRemoteECITerminalEvidenceBoundsUTF8ProviderFields(t *testing.T) {
	value := strings.Repeat("失败", 1_000)
	bounded := boundedRemoteECIField(value)
	if !utf8.ValidString(bounded) || len(bounded) > 1024 {
		t.Fatalf("bounded provider field is invalid: bytes=%d utf8=%v", len(bounded), utf8.ValidString(bounded))
	}
}

func TestFailedRemoteGateErrorPreservesFailedTestIdentity(t *testing.T) {
	err := failedRemoteGateError([]ShardResult{{
		ShardIdentity: "sha256:failed-shard",
		Report: gate.PlanExecutionReport{
			Gates: []gate.PlanGateExecution{{
				GateID:   gate.GateIDBackendTestWithGuard,
				Status:   gate.ResultStatusFailed,
				ExitCode: 1,
				Log:      gate.PlainTextLog("bounded gate output"),
				TestTimings: []gate.GoTestTiming{
					{Name: "TestPassedSibling", Status: gate.GoTestStatusPass, DurationMS: 2},
					{Name: "TestActualFailure", Status: gate.GoTestStatusFail, DurationMS: 50},
				},
			}},
		},
		workerDiagnostic: "worker tail",
	}})
	if !errors.Is(err, ErrGateFailed) {
		t.Fatalf("error chain = %v", err)
	}
	message := err.Error()
	for _, want := range []string{
		"sha256:failed-shard",
		"TestActualFailure(50ms)",
		"bounded gate output",
		"worker tail",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "TestPassedSibling") {
		t.Fatalf("error includes passed test: %q", message)
	}
}

func TestFailedRemoteGateErrorSummarizesCancelledShardsAndKeepsRoot(t *testing.T) {
	shards := make([]ShardResult, 0, 12)
	for index := range 11 {
		shards = append(shards, ShardResult{
			ShardIdentity:   fmt.Sprintf("sha256:cancelled-%d", index),
			ContainerStatus: "Failed",
			Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{
				GateID: gate.GateID(fmt.Sprintf("cancelled-%d", index)), Status: gate.ResultStatusCancelled,
			}}},
			workerDiagnostic: strings.Repeat("cancelled noise", 500),
		})
	}
	shards = append(shards, ShardResult{
		ShardIdentity:   "sha256:root",
		ContainerStatus: "Failed",
		Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{
			GateID: "root-gate", Status: gate.ResultStatusFailed, ExitCode: 7,
		}}},
		workerDiagnostic: "root worker tail",
	})

	message := failedRemoteGateError(shards).Error()
	for _, want := range []string{"sha256:root", "root-gate", "root worker tail", "failed_shards=12", "cancelled_workloads=11"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "cancelled noise") || strings.Contains(message, "sha256:cancelled-") {
		t.Fatalf("error expands cancelled shard noise: %q", message)
	}
}

func TestFailedRemoteGateErrorKeepsOneCancelledInfrastructureDiagnostic(t *testing.T) {
	shards := []ShardResult{
		{
			ShardIdentity: "sha256:first", ContainerStatus: "Failed", workerDiagnostic: "first infrastructure failure",
			Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{GateID: "first", Status: gate.ResultStatusCancelled}}},
		},
		{
			ShardIdentity: "sha256:second", ContainerStatus: "Failed", workerDiagnostic: "second noise",
			Report: gate.PlanExecutionReport{Gates: []gate.PlanGateExecution{{GateID: "second", Status: gate.ResultStatusCancelled}}},
		},
	}

	message := failedRemoteGateError(shards).Error()
	for _, want := range []string{"sha256:first", "first infrastructure failure", "failed_shards=2", "cancelled_workloads=2"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
	if strings.Contains(message, "second noise") {
		t.Fatalf("error expands more than one cancelled infrastructure diagnostic: %q", message)
	}
}

func TestFailedObservedWorkloadErrorBoundsConcurrentFailures(t *testing.T) {
	observed := make(map[string]gate.PlanGateExecution, 20)
	for index := range 20 {
		id := fmt.Sprintf("failed-%02d", index)
		observed[id] = gate.PlanGateExecution{GateID: gate.GateID(id), Status: gate.ResultStatusFailed, ExitCode: 1}
	}

	message := failedObservedWorkloadError(observed).Error()
	if !strings.Contains(message, "omitted_failed_workloads=12") {
		t.Fatalf("bounded observed failure summary = %q", message)
	}
	if strings.Contains(message, "failed-19") {
		t.Fatalf("bounded observed failure summary includes an omitted workload: %q", message)
	}
}

func TestBoundedRemoteRunErrorTextPreservesFailureHeadAndFinalLog(t *testing.T) {
	failureHead := "failed_tests=TestActualFailure(50ms)\n"
	finalLog := "\ncleanup_complete=false: delete temporary OSS prefix failed"
	runErr := errors.New(
		failureHead +
			strings.Repeat("SUPER_DOLPHIN_CI_TEST_TIMING status=pass\n", 500) +
			finalLog,
	)

	errorText := boundedRemoteRunErrorText(runErr)

	if len(errorText) > remoteShardDiagnosticMaxBytes {
		t.Fatalf("bounded error bytes = %d, max = %d", len(errorText), remoteShardDiagnosticMaxBytes)
	}
	for _, want := range []string{
		failureHead,
		finalLog,
		"remote CI error truncated",
		"full_bytes=",
		"sha256:",
	} {
		if !strings.Contains(errorText, want) {
			t.Fatalf("bounded error %q does not contain %q", errorText, want)
		}
	}
	if errorText == runErr.Error() {
		t.Fatal("bounded error unexpectedly retained oversized error verbatim")
	}
}

func TestBoundedRemoteRunErrorTextPreservesUTF8(t *testing.T) {
	runErr := errors.New(strings.Repeat("失败日志", remoteShardDiagnosticMaxBytes))

	errorText := boundedRemoteRunErrorText(runErr)

	if !utf8.ValidString(errorText) {
		t.Fatalf("bounded error is not valid UTF-8: %q", errorText)
	}
	if len(errorText) > remoteShardDiagnosticMaxBytes {
		t.Fatalf("bounded error bytes = %d, max = %d", len(errorText), remoteShardDiagnosticMaxBytes)
	}
}

func TestCoordinatorRunRejectsMissingWorkerReport(t *testing.T) {
	repository, input := remoteRunFixture(t)
	store := &coordinatorStore{}
	exitCode := int64(137)
	runtime := &coordinatorRuntime{
		tamperLog: true,
		status:    "Failed",
		initLog:   "materialize exploded",
		groupState: eci.ContainerGroup{
			Containers: []eci.ContainerStatus{{
				Name: "worker",
				CurrentState: eci.ContainerState{
					State:    "Terminated",
					ExitCode: &exitCode,
					Reason:   "OOMKilled",
					Message:  "memory limit exceeded",
				},
			}},
			Events: []eci.ContainerGroupEvent{{
				Type:          "Warning",
				Reason:        "DeadlineExceeded",
				Message:       "worker exceeded active deadline",
				Count:         1,
				LastTimestamp: "2026-07-27T08:04:00Z",
			}, {
				Type:          "Warning",
				Reason:        "BackOff",
				Message:       "worker exited",
				Count:         2,
				LastTimestamp: "2026-07-27T08:03:00Z",
			}, {
				Type:          "Normal",
				Reason:        "Pulled",
				Message:       "image ready",
				Count:         1,
				LastTimestamp: "2026-07-27T08:02:00Z",
			}, {
				Type:          "Normal",
				Reason:        "Scheduled",
				Message:       "worker scheduled",
				Count:         1,
				LastTimestamp: "2026-07-27T08:01:00Z",
			}},
		},
	}
	coordinator := newTestCoordinator(t, store, runtime)
	input.RepositoryRoot = repository
	result, err := coordinator.Run(context.Background(), input)
	if err == nil || result.Status == gate.ResultStatusPassed || !result.CleanupComplete {
		t.Fatalf("Run() result=%+v error=%v", result, err)
	}
	for _, fragment := range []string{"status=Failed", "materialize exploded", "exit_code=137", "OOMKilled", "BackOff", "DeadlineExceeded", "index=", "estimated_duration_ms=", "gates="} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Run() diagnostic error = %v, missing %q", err, fragment)
		}
	}
}

func TestCoordinatorRunKeepsProviderFailedCauseWithValidPassingReport(t *testing.T) {
	repository, input := remoteRunFixture(t)
	store := &coordinatorStore{}
	exitCode := int64(137)
	runtime := &coordinatorRuntime{
		status: "Failed",
		groupState: eci.ContainerGroup{
			Containers: []eci.ContainerStatus{{Name: "worker", CurrentState: eci.ContainerState{
				State: "Terminated", ExitCode: &exitCode, Reason: "OOMKilled", Message: "memory limit exceeded",
			}}},
			Events: []eci.ContainerGroupEvent{{Type: "Warning", Reason: "BackOff", Message: "worker exited", Count: 2, LastTimestamp: "2026-08-07T00:00:00Z"}},
		},
	}
	coordinator := newTestCoordinator(t, store, runtime)
	input.RepositoryRoot = repository
	result, err := coordinator.Run(context.Background(), input)
	assertProviderFailedRunResult(t, result, err)
	loaded := persistAndLoadProviderFailedRun(t, input, result, err)
	assertPersistedProviderFailure(t, loaded)
}

func assertProviderFailedRunResult(t *testing.T, result RunResult, err error) {
	t.Helper()
	if err == nil || result.Status == gate.ResultStatusPassed || len(result.Shards) == 0 {
		t.Fatalf("Run() result=%+v error=%v, want provider failure", result, err)
	}
	assertProviderFailedTerminalEvidence(t, result.Shards)
	for _, fragment := range []string{"exit_code=137", "OOMKilled", "BackOff", "failed_shards="} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Run() provider failure error = %v, missing %q", err, fragment)
		}
	}
}

func assertProviderFailedTerminalEvidence(t *testing.T, shards []ShardResult) {
	t.Helper()
	if len(shards) == 0 {
		t.Fatalf("Run() terminal evidence = <none>, want provider cause")
	}
	evidence := shards[0].TerminalEvidence
	if evidence == nil || len(evidence.Containers) == 0 || evidence.Containers[0].Reason != "OOMKilled" {
		t.Fatalf("Run() terminal evidence = %#v, want provider cause", evidence)
	}
}

func persistAndLoadProviderFailedRun(t *testing.T, input RunInput, result RunResult, err error) gate.RemoteCIRunRecord {
	t.Helper()
	if persistErr := recordRemoteCIRun(input.LedgerStore, result, err); persistErr != nil {
		t.Fatalf("persist provider-failed receipt projection: %v", persistErr)
	}
	loaded, loadErr := input.LedgerStore.LoadRemoteCIRun(result.JobID)
	if loadErr != nil {
		t.Fatalf("load provider-failed receipt projection: %v", loadErr)
	}
	return loaded
}

func assertPersistedProviderFailure(t *testing.T, loaded gate.RemoteCIRunRecord) {
	t.Helper()
	if len(loaded.Shards) == 0 {
		t.Fatalf("loaded provider terminal evidence = <none>, want OOMKilled")
	}
	evidence := loaded.Shards[0].TerminalEvidence
	if evidence == nil || len(evidence.Containers) == 0 || evidence.Containers[0].Reason != "OOMKilled" {
		t.Fatalf("loaded provider terminal evidence = %#v, want OOMKilled", loaded.Shards)
	}
	if !strings.Contains(loaded.ErrorText, "OOMKilled") || !strings.Contains(loaded.ErrorText, "exit_code=137") {
		t.Fatalf("loaded provider first-cause error text = %q", loaded.ErrorText)
	}
}
