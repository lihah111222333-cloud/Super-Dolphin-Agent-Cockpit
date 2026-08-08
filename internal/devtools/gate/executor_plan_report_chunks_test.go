package gate

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestPlanExecutionReportChunksRejectIncompleteOrForgedFrames 保证分片报告拒绝不完整和伪造帧。
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

// TestPlanExecutionReportRejectsMismatchedAgentTokenDigest 保证报告与协调器分配的 agent 摘要一致。
func TestPlanExecutionReportRejectsMismatchedAgentTokenDigest(t *testing.T) {
	report, _ := multiChunkPlanReport(t)
	report.AgentTokenDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]GateID, len(report.Gates))
	for index, execution := range report.Gates {
		expected[index] = execution.GateID
	}
	if _, err := DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest(chunks, expected, report.AgentTokenDigest); err != nil {
		t.Fatalf("decode matching agent token digest: %v", err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSetAndAgentTokenDigest(chunks, expected, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err == nil {
		t.Fatal("decode mismatched agent token digest error = nil")
	}
}

// TestPlanExecutionReportChunksStayBelowRemoteLogLineLimit 保证每个远程日志分片不超过行长度限制。
func TestPlanExecutionReportChunksStayBelowRemoteLogLineLimit(t *testing.T) {
	_, chunks := multiChunkPlanReport(t)
	for index, chunk := range chunks {
		if len(chunk)+1 > executorPlanReportMaxLineBytes {
			t.Fatalf("chunk %d line bytes = %d, want <= %d", index, len(chunk)+1, executorPlanReportMaxLineBytes)
		}
	}
}

// TestPlanExecutionReportUsesPlainTextLogRecords 保证报告日志保持可读的纯文本记录。
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

// TestPlanExecutionReportChunksAcceptOnlyCoordinatorFrozenDynamicGateSet 保证动态 gate 集合必须与协调器冻结结果相同。
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

func TestWorkerExecutionOutcomeRoundTripRetainsNonzeroFailureWithPassingGates(t *testing.T) {
	request := testExecutorPlanRequest(t)
	report, err := executeGatePlanWithRunner(context.Background(), request, func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
		return successfulPlanGateResult(id), nil
	}, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	workerErr := errors.New("worker failed at /private/secret/path")
	if err := writeExecutorPlanReportWithCompileGroups(request, report, workerErr, &stdout); err == nil {
		t.Fatal("worker report writer returned nil for nonzero execution error")
	}
	wire := stdout.String()
	if strings.Contains(wire, "private/secret/path") || strings.Contains(wire, "worker failed") {
		t.Fatalf("worker report wire leaked raw execution error: %q", wire)
	}
	decoded, err := DecodePlanExecutionReport(wire)
	if err != nil {
		t.Fatalf("decode worker failure report: %v", err)
	}
	want := WorkerExecutionOutcome{Status: WorkerExecutionStatusFailed, ExitCode: 1, ReasonCode: WorkerExecutionReasonExecutionError}
	if decoded.ExecutionOutcome != want {
		t.Fatalf("decoded execution outcome = %#v, want %#v", decoded.ExecutionOutcome, want)
	}
	for _, gate := range decoded.Gates {
		if gate.Status != ResultStatusPassed || gate.ExitCode != 0 {
			t.Fatalf("worker failure should preserve passing gate evidence: %#v", gate)
		}
	}
}

// multiChunkPlanReport 构造覆盖多分片边界的冻结计划执行报告。
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
