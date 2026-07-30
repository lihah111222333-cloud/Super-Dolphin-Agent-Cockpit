package remoteci

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

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
