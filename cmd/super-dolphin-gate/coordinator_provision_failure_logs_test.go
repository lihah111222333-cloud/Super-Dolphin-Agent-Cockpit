package main

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func productionProvisionFailureLogs(
	t *testing.T,
	client *coordinatorTransportClient,
	terminal jobStatus,
) []coordinatorGateLog {
	t.Helper()
	failed := productionProvisionFailedGateResults(terminal.GateResults)
	logs := make([]coordinatorGateLog, 0, len(failed))
	for _, gateResult := range failed {
		gateID := gatecontract.GateID(gateResult.GateID)
		result, err := client.GateLog(context.Background(), terminal.JobID, gateID)
		if err != nil {
			t.Fatalf("query persisted failure log for job %q gate %q: %v", terminal.JobID, gateID, err)
		}
		fullLogDigest := coordinatorLogDigest([]byte(result.Log))
		if result.JobID != terminal.JobID || result.GateID != gateID ||
			result.LogDigest != gateResult.LogDigest || fullLogDigest != result.LogDigest {
			t.Fatalf(
				"persisted failure log evidence = job=%q gate=%q digest=%q full_log_digest=%q; want job=%q gate=%q digest=%q",
				result.JobID,
				result.GateID,
				result.LogDigest,
				fullLogDigest,
				terminal.JobID,
				gateID,
				gateResult.LogDigest,
			)
		}
		result.Log = productionProvisionFailureLogTail(result.Log)
		logs = append(logs, result)
	}
	return logs
}

func productionProvisionFailureLogTail(log string) string {
	if len(log) <= productionProvisionFailureLogDisplayMaxBytes {
		return log
	}
	contentBytes := productionProvisionFailureLogDisplayMaxBytes - len(productionProvisionFailureLogTailMarker)
	failure := productionProvisionFailureBlock(log, contentBytes/2)
	tailBytes := contentBytes - len(failure)
	tail := utf8LogSuffix(log, tailBytes)
	separator := ""
	if failure != "" && !strings.HasSuffix(failure, "\n") {
		separator = "\n"
		tail = utf8LogSuffix(log, tailBytes-1)
	}
	return productionProvisionFailureLogTailMarker + failure + separator + tail
}

func productionProvisionFailureBlock(log string, limit int) string {
	start := strings.Index(log, "--- FAIL:")
	if start < 0 {
		start = productionProvisionNonTestFailureStart(log)
	}
	if start < 0 || limit <= 0 {
		return ""
	}
	end := len(log)
	if offset := strings.Index(log[start:], "\nFAIL\t"); offset >= 0 {
		end = start + offset + len("\nFAIL\t")
		if lineEnd := strings.IndexByte(log[end:], '\n'); lineEnd >= 0 {
			end += lineEnd + 1
		}
	}
	block := log[start:end]
	if len(block) > limit {
		block = block[:limit]
		for len(block) > 0 && !utf8.ValidString(block) {
			block = block[:len(block)-1]
		}
	}
	return block
}

func productionProvisionNonTestFailureStart(log string) int {
	for searchOffset := 0; searchOffset < len(log); {
		offset := strings.Index(log[searchOffset:], "\nFAIL\t")
		if offset < 0 {
			return -1
		}
		failureStart := searchOffset + offset
		failureLine := log[failureStart+1:]
		if lineEnd := strings.IndexByte(failureLine, '\n'); lineEnd >= 0 {
			failureLine = failureLine[:lineEnd]
		}
		if strings.Contains(failureLine, "[setup failed]") || strings.Contains(failureLine, "[build failed]") {
			if packageStart := strings.LastIndex(log[:failureStart], "\n# "); packageStart >= 0 {
				return packageStart + 1
			}
			if strings.HasPrefix(log, "# ") {
				return 0
			}
		}
		if raceStart := strings.LastIndex(log[:failureStart], "\nWARNING: DATA RACE"); raceStart >= 0 {
			return raceStart + 1
		}
		if strings.HasPrefix(log, "WARNING: DATA RACE") {
			return 0
		}
		searchOffset = failureStart + len("\nFAIL\t")
	}
	return -1
}

func utf8LogSuffix(log string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(log) <= limit {
		return log
	}
	start := len(log) - limit
	for start < len(log) && !utf8.RuneStart(log[start]) {
		start++
	}
	return log[start:]
}

func TestProductionProvisionFailureLogTail(t *testing.T) {
	t.Run("short log is unchanged", testProductionProvisionShortFailureLogTail)
	t.Run("long log includes failing tail", testProductionProvisionLongFailureLogTail)
	t.Run("long log preserves failure before tail", testProductionProvisionFailureBeforeTail)
	t.Run("long log preserves non-test failure before tail", testProductionProvisionNonTestFailureBeforeTail)
	t.Run("multibyte boundary remains valid UTF-8", testProductionProvisionUTF8FailureLogTail)
}

func testProductionProvisionShortFailureLogTail(t *testing.T) {
	log := "go test ./cmd/super-dolphin-gate\nPASS\n"
	got := productionProvisionFailureLogTail(log)
	if len(got) > productionProvisionFailureLogDisplayMaxBytes {
		t.Fatalf("display log length = %d, want at most %d", len(got), productionProvisionFailureLogDisplayMaxBytes)
	}
	if got != log {
		t.Fatalf("short display log = %q, want %q", got, log)
	}
}

func testProductionProvisionLongFailureLogTail(t *testing.T) {
	tail := "\n--- FAIL: TestBackendGate (0.01s)\n"
	log := "early frontend output\n" +
		strings.Repeat("x", productionProvisionFailureLogDisplayMaxBytes) + tail
	got := productionProvisionFailureLogTail(log)
	if len(got) > productionProvisionFailureLogDisplayMaxBytes {
		t.Fatalf("display log length = %d, want at most %d", len(got), productionProvisionFailureLogDisplayMaxBytes)
	}
	if !strings.HasPrefix(got, productionProvisionFailureLogTailMarker) {
		t.Fatalf("long display log = %q, want truncation marker", got)
	}
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("long display log does not retain failure tail %q", tail)
	}
	if strings.Contains(got, "early frontend output") {
		t.Fatalf("long display log retained early output: %q", got)
	}
	if len(got) != productionProvisionFailureLogDisplayMaxBytes {
		t.Fatalf("long display log length = %d, want %d", len(got), productionProvisionFailureLogDisplayMaxBytes)
	}
}

func testProductionProvisionFailureBeforeTail(t *testing.T) {
	failure := "--- FAIL: TestBackendGate (0.01s)\n    backend_test.go:42: retained detail\nFAIL\texample.invalid/backend\t0.01s\n"
	log := strings.Repeat("prefix\n", productionProvisionFailureLogDisplayMaxBytes) + failure +
		strings.Repeat("passing package\n", productionProvisionFailureLogDisplayMaxBytes) + "final context\n"
	got := productionProvisionFailureLogTail(log)
	if len(got) > productionProvisionFailureLogDisplayMaxBytes {
		t.Fatalf("display log length = %d, want at most %d", len(got), productionProvisionFailureLogDisplayMaxBytes)
	}
	for _, want := range []string{"--- FAIL: TestBackendGate", "retained detail", "FAIL\texample.invalid/backend", "final context"} {
		if !strings.Contains(got, want) {
			t.Fatalf("display log missing %q: %q", want, got)
		}
	}
}

func testProductionProvisionNonTestFailureBeforeTail(t *testing.T) {
	tests := []struct {
		name    string
		failure string
		want    []string
	}{
		{
			name: "setup failure",
			failure: "# example.invalid/embed\n" +
				"assets.go:12: go:embed assets/*.json: no matching files found\n" +
				"FAIL\texample.invalid/embed [setup failed]\n",
			want: []string{"# example.invalid/embed", "go:embed assets/*.json: no matching files found", "FAIL\texample.invalid/embed [setup failed]"},
		},
		{
			name: "race detector",
			failure: "WARNING: DATA RACE\n" +
				"Read at 0x00c0000 by goroutine 7:\n" +
				"  example.invalid/race.TestConcurrent()\n" +
				"Found 1 data race(s)\n" +
				"FAIL\texample.invalid/race\t0.01s\n",
			want: []string{"WARNING: DATA RACE", "Read at 0x00c0000 by goroutine 7", "Found 1 data race(s)", "FAIL\texample.invalid/race\t0.01s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := strings.Repeat("prefix\n", productionProvisionFailureLogDisplayMaxBytes) + tt.failure +
				strings.Repeat("passing package\n", productionProvisionFailureLogDisplayMaxBytes) + "final context\n"
			got := productionProvisionFailureLogTail(log)
			if len(got) > productionProvisionFailureLogDisplayMaxBytes {
				t.Fatalf("display log length = %d, want at most %d", len(got), productionProvisionFailureLogDisplayMaxBytes)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("display log is not valid UTF-8: %q", got)
			}
			for _, want := range append(tt.want, "final context") {
				if !strings.Contains(got, want) {
					t.Fatalf("display log missing %q: %q", want, got)
				}
			}
		})
	}
}

func testProductionProvisionUTF8FailureLogTail(t *testing.T) {
	tailBytes := (productionProvisionFailureLogDisplayMaxBytes - len(productionProvisionFailureLogTailMarker))
	failureTail := "\n最终 Go test 失败\n"
	fillerBytes := tailBytes - 2 - len(failureTail)
	log := strings.Repeat("p", len(productionProvisionFailureLogTailMarker)+10) +
		"中" + strings.Repeat("x", fillerBytes) + failureTail
	rawTailStart := len(log) - tailBytes
	if utf8.RuneStart(log[rawTailStart]) {
		t.Fatalf("test fixture tail start %d is already a rune boundary", rawTailStart)
	}

	got := productionProvisionFailureLogTail(log)
	if !utf8.ValidString(got) {
		t.Fatalf("display log is not valid UTF-8: %q", got)
	}
	if len(got) > productionProvisionFailureLogDisplayMaxBytes {
		t.Fatalf("display log length = %d, want at most %d", len(got), productionProvisionFailureLogDisplayMaxBytes)
	}
	if !strings.HasSuffix(got, failureTail) {
		t.Fatalf("display log does not retain UTF-8 failure tail %q", failureTail)
	}
}

func productionProvisionFailedGateResults(results []gatecontract.GateResult) []gatecontract.GateResult {
	failed := make([]gatecontract.GateResult, 0, len(results))
	for _, result := range results {
		if result.Status != gatecontract.GateStatusPassed {
			failed = append(failed, result)
		}
	}
	return failed
}

func TestProductionProvisionFailureLogSelectionIncludesEveryNonPassedGate(t *testing.T) {
	results := []gatecontract.GateResult{
		{GateID: string(gatecontract.GateIDAIMaintenanceSelfTest), Status: gatecontract.GateStatusPassed},
		{GateID: string(gatecontract.GateIDBackendTestWithGuard), Status: gatecontract.GateStatusFailed},
		{GateID: string(gatecontract.GateIDFrontendLint), Status: gatecontract.GateStatusFailed},
	}
	failed := productionProvisionFailedGateResults(results)
	if len(failed) != 2 || failed[0].GateID != string(gatecontract.GateIDBackendTestWithGuard) ||
		failed[1].GateID != string(gatecontract.GateIDFrontendLint) {
		t.Fatalf("failed gate log selection = %#v", failed)
	}
}
