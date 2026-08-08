package gate

import (
	"strings"
	"testing"
	"time"
)

// TestExactGoTestTimingEvidenceRejectsWrapperMismatch 保证 test2json elapsed 不得脱离同一 execution 的实测区间。
func TestExactGoTestTimingEvidenceRejectsWrapperMismatch(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestCompileGroupSelector000", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		event      string
		wantError  bool
		wantMillis int64
	}{
		{name: "remote elapsed mismatch", event: `{"Action":"fail","Time":"2026-08-05T00:00:11.521Z","Test":"TestCompileGroupSelector000","Elapsed":229.952}`, wantError: true, wantMillis: 229952},
		{name: "failed assertion with matching elapsed", event: `{"Action":"fail","Time":"2026-08-05T00:00:11.521Z","Test":"TestCompileGroupSelector000","Elapsed":5}`, wantMillis: 5000},
	} {
		t.Run(test.name, func(t *testing.T) {
			execution, timing := syntheticExactTimingExecution(t, workload, test.event)
			if len(timing) != 1 || timing[0].DurationMS != test.wantMillis || timing[0].Status != GoTestStatusFail {
				t.Fatalf("synthetic test2json timing = %#v, want %dms failed", timing, test.wantMillis)
			}
			validationErr := ValidatePlanGateTimingEvidence(execution)
			if test.wantError {
				if validationErr == nil || !strings.Contains(validationErr.Error(), "exceeds measured total interval") {
					t.Fatalf("mismatched timing validation = %v, want total-interval rejection", validationErr)
				}
				assertExactTimingReportRejects(t, execution)
				return
			}
			if validationErr != nil {
				t.Fatalf("valid failed assertion timing rejected: %v", validationErr)
			}
			assertExactTimingReportRoundTrip(t, execution)
		})
	}
}

// syntheticExactTimingExecution 将合成 test2json 事件绑定到真实 exact selector execution。
func syntheticExactTimingExecution(t *testing.T, workload Workload, terminalEvent string) (PlanGateExecution, []GoTestTiming) {
	t.Helper()
	writer := newCompiledSelectorBatchEventWriter(newBoundedPlanLog(executorPlanMaxLogBytes), map[string]struct{}{"TestCompileGroupSelector000": {}})
	writeBatchTestEvent(t, writer, `{"Action":"run","Time":"2026-08-05T00:00:00Z","Test":"TestCompileGroupSelector000"}`)
	writeBatchTestEvent(t, writer, terminalEvent)
	base := time.UnixMilli(1_700_000_000_000).UTC()
	execution := PlanGateExecution{
		GateID: GateID(workload.ID), Status: ResultStatusFailed, ExitCode: 1,
		StartedAt: base, CompletedAt: base.Add(11_521 * time.Millisecond),
		LogDigest: digestPlanLog(nil), TestTimings: writer.selectorTimings["TestCompileGroupSelector000"],
		ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 11_520, TotalMS: 11_521},
	}
	return execution, execution.TestTimings
}

// assertExactTimingReportRejects 通过绕过编码前检查验证 decoder 仍拒绝矛盾 wire。
func assertExactTimingReportRejects(t *testing.T, execution PlanGateExecution) {
	t.Helper()
	report := PlanExecutionReport{SchemaVersion: ExecutorPlanReportSchemaVersion, Profile: ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("a", 64), ExecutionOutcome: SuccessfulWorkerExecutionOutcome(), Gates: []PlanGateExecution{execution}}
	records, err := encodePlanReportRecords(report)
	if err != nil {
		t.Fatalf("encode synthetic invalid records for decoder: %v", err)
	}
	chunks, err := framePlanReportRecords(records, digestPlanExecutionReport(report))
	if err != nil {
		t.Fatalf("frame synthetic invalid report: %v", err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(chunks, []GateID{execution.GateID}); err == nil || !strings.Contains(err.Error(), "timing evidence is invalid") || !strings.Contains(err.Error(), "exceeds measured total interval") {
		t.Fatalf("decoder accepted mismatched timing: %v", err)
	}
}

// assertExactTimingReportRoundTrip 确认真实 failed assertion timing 可以完整编码、解码和投影。
func assertExactTimingReportRoundTrip(t *testing.T, execution PlanGateExecution) {
	t.Helper()
	report := PlanExecutionReport{SchemaVersion: ExecutorPlanReportSchemaVersion, Profile: ProfileLocalFast, PlanDigest: "sha256:" + strings.Repeat("a", 64), ExecutionOutcome: SuccessfulWorkerExecutionOutcome(), Gates: []PlanGateExecution{execution}}
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatalf("encode valid failed assertion report: %v", err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(chunks, []GateID{execution.GateID}); err != nil {
		t.Fatalf("decode valid failed assertion report: %v", err)
	}
}

// TestExactGoTestTimingEvidenceAllowsMissingTimingForCancelledExecution 保留未启动 selector 的取消诊断，不伪造测试耗时。
func TestExactGoTestTimingEvidenceAllowsMissingTimingForCancelledExecution(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestCompileGroupSelector000", 1)
	if err != nil {
		t.Fatal(err)
	}
	execution := PlanGateExecution{GateID: GateID(workload.ID), Status: ResultStatusCancelled, ExitCode: -1, TestTimings: nil}
	if err := ValidatePlanGateTimingEvidence(execution); err != nil {
		t.Fatalf("missing timing for cancelled execution rejected: %v", err)
	}
}
