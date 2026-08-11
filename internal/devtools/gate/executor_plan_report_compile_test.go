package gate

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestCompileGroupReportRoundTrip(t *testing.T) {
	report := compileGroupReportFixture(t, successfulCompileGroupExecutionFixture())
	decoded := roundTripCompileGroupReport(t, report)
	assertCompileGroupArtifactRoundTrip(t, report, decoded)
}

func TestCompileGroupReportRoundTripUnstartedFailureHasNoObservation(t *testing.T) {
	report := compileGroupReportFixture(t, unstartedFailureCompileGroupExecutionFixture())
	assertValidCompileGroupExecution(t, report.CompileGroupExecutions[0])
	decoded := roundTripCompileGroupReport(t, report)
	assertUnstartedFailureHasNoObservation(t, decoded)
}

func TestCompileGroupReportMultiGroupAggregateOversizeFailsClosed(t *testing.T) {
	now := executorPlanTestNow()
	const groupCount = 33
	report := PlanExecutionReport{Gates: make([]PlanGateExecution, 0, groupCount), CompileGroupExecutions: make([]CompileGroupExecution, 0, groupCount)}
	for index := range groupCount {
		gateID, gate := compileGroupSelectorGateForTarget(t, now, "./internal/archtest", "TestAggregate", index)
		gate.Status, gate.ExitCode = ResultStatusFailed, 1
		gate.TestTimings[0].Status = GoTestStatusFail
		gate.Log = PlainTextLog(bytes.Repeat([]byte{'x'}, executorPlanCompileGroupFullFailureLogBytes))
		gate.LogDigest = digestPlanLog(gate.Log)
		execution := successfulCompileGroupExecutionFixture()
		execution.GroupID = digestPlanLog(fmt.Appendf(nil, "aggregate-group-%d", index))
		execution.WorkloadIDs = []GateID{gateID}
		report.Gates = append(report.Gates, gate)
		report.CompileGroupExecutions = append(report.CompileGroupExecutions, execution)
	}
	if _, err := normalizeCompileGroupReportLogs(report); err == nil || !strings.Contains(err.Error(), "aggregate logs exceed") {
		t.Fatalf("multi-group aggregate oversize error = %v, want deterministic aggregate rejection", err)
	}
}

func TestCompileGroupReportDigestBindsAggregatePolicyAndGroupOrder(t *testing.T) {
	firstID := GateID("group-order-first")
	secondID := GateID("group-order-second")
	first := successfulCompileGroupExecutionFixture()
	first.GroupID = digestPlanLog([]byte("order-first"))
	first.WorkloadIDs = []GateID{firstID}
	second := successfulCompileGroupExecutionFixture()
	second.GroupID = digestPlanLog([]byte("order-second"))
	second.WorkloadIDs = []GateID{secondID}
	report := PlanExecutionReport{CompileGroupExecutions: []CompileGroupExecution{first, second}}
	orderedDigest := digestPlanExecutionReport(report)
	swapped := report
	swapped.CompileGroupExecutions = []CompileGroupExecution{second, first}
	if orderedDigest == digestPlanExecutionReport(swapped) {
		t.Fatal("compile-group report digest ignored canonical group order")
	}
	policy := appendCompileGroupLogBudgetDigest(nil, 2)
	if !bytes.Contains(policy, []byte("compile-group-aggregate-log-bytes")) {
		t.Fatalf("compile-group policy digest omitted aggregate budget: %q", policy)
	}
}

// compileGroupReportFixture 构造带指定编译组记录的计划报告。
func compileGroupReportFixture(t *testing.T, execution CompileGroupExecution) PlanExecutionReport {
	t.Helper()
	request := testExecutorPlanRequest(t)
	report, err := executeGatePlanWithRunner(context.Background(), request, func(_ context.Context, _ int, id GateID) (PlanGateExecution, error) {
		return successfulPlanGateResult(id), nil
	}, executorPlanTestNow)
	if err != nil {
		t.Fatal(err)
	}
	report.CompileGroupExecutions = []CompileGroupExecution{execution}
	return report
}

// roundTripCompileGroupReport 通过计划报告编解码边界返回解码结果。
func roundTripCompileGroupReport(t *testing.T, report PlanExecutionReport) PlanExecutionReport {
	t.Helper()
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, planReportGateIDs(report))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

// successfulCompileGroupExecutionFixture 提供覆盖 timing、artifact 和缓存身份的通过记录。
func successfulCompileGroupExecutionFixture() CompileGroupExecution {
	started := time.UnixMilli(10_000)
	return CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digestPlanLog([]byte("group")), ArtifactKey: digestPlanLog([]byte("artifact")), PackageTarget: "./internal/devtools/gate",
		WorkloadIDs: []GateID{"guard::go-test::e30uY2h0ZXN0"}, StartedAtUnixMS: started.UnixMilli(), CompletedAtUnixMS: started.Add(10 * time.Millisecond).UnixMilli(), DurationMS: 10,
		ArtifactSHA256: digestPlanLog([]byte("binary")), ArtifactSize: 12, CacheHits: 1, CacheMisses: 2, CachePuts: 1,
		Status: ResultStatusPassed, ExitCode: 0, CompileCommandDigest: digestPlanLog([]byte("argv")), ProfileDigest: digestPlanLog([]byte("profile")), ResourceClassID: "normal-medium",
	}
}

// unstartedFailureCompileGroupExecutionFixture 提供没有编译观察字段的预检失败记录。
func unstartedFailureCompileGroupExecutionFixture() CompileGroupExecution {
	return CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digestPlanLog([]byte("unstarted-group")), ArtifactKey: digestPlanLog([]byte("unstarted-artifact")), PackageTarget: "./internal/devtools/gate",
		WorkloadIDs: []GateID{"guard::go-test::e30uY2h0ZXN0"}, Status: ResultStatusFailed, ExitCode: 1,
		ErrorText: "preflight failed", ProfileDigest: digestPlanLog([]byte("profile")), ResourceClassID: "normal-medium",
	}
}

// assertCompileGroupArtifactRoundTrip 断言编译组数量及 artifact identity 完整往返。
func assertCompileGroupArtifactRoundTrip(t *testing.T, report, decoded PlanExecutionReport) {
	t.Helper()
	if len(decoded.CompileGroupExecutions) != 1 || decoded.CompileGroupExecutions[0].ArtifactSHA256 != report.CompileGroupExecutions[0].ArtifactSHA256 {
		t.Fatalf("compile group ledger did not round-trip: %#v", decoded.CompileGroupExecutions)
	}
}

// assertValidCompileGroupExecution 断言预检失败记录仍满足有界结果契约。
func assertValidCompileGroupExecution(t *testing.T, execution CompileGroupExecution) {
	t.Helper()
	if err := execution.Validate(); err != nil {
		t.Fatalf("unstarted failure should remain a valid bounded result: %v", err)
	}
}

// assertUnstartedFailureHasNoObservation 断言预检失败往返后没有伪造编译观察。
func assertUnstartedFailureHasNoObservation(t *testing.T, decoded PlanExecutionReport) {
	t.Helper()
	if len(decoded.CompileGroupExecutions) != 1 {
		t.Fatalf("decoded compile group count = %d, want 1", len(decoded.CompileGroupExecutions))
	}
	got := decoded.CompileGroupExecutions[0]
	if got.Status != ResultStatusFailed || got.ErrorText != "preflight failed" {
		t.Fatalf("decoded unstarted failure = %#v", got)
	}
	if got.StartedAtUnixMS != 0 || got.CompletedAtUnixMS != 0 || got.DurationMS != 0 || got.CompileCommandDigest != "" {
		t.Fatalf("unstarted failure fabricated a compile observation: %#v", got)
	}
}

func TestCompileGroupUnstartedFailureRejectsForgedEvidence(t *testing.T) {
	base := CompileGroupExecution{
		Scope: cicontract.TimingScopeCompileGroup, Phase: cicontract.TimingTestBinaryCompile,
		GroupID: digestPlanLog([]byte("forged-group")), ArtifactKey: digestPlanLog([]byte("forged-artifact")), PackageTarget: "./internal/devtools/gate",
		WorkloadIDs: []GateID{"guard::go-test::e30uY2h0ZXN0"}, Status: ResultStatusFailed, ExitCode: 1,
		ErrorText: "preflight failed", ProfileDigest: digestPlanLog([]byte("profile")), ResourceClassID: "normal-medium",
	}
	report := compileGroupReportFixture(t, base)
	mutations := []struct {
		name   string
		mutate func(*CompileGroupExecution)
	}{
		{name: "artifact digest", mutate: func(execution *CompileGroupExecution) { execution.ArtifactSHA256 = digestPlanLog([]byte("binary")) }},
		{name: "artifact size", mutate: func(execution *CompileGroupExecution) { execution.ArtifactSize = 1 }},
		{name: "cache hits", mutate: func(execution *CompileGroupExecution) { execution.CacheHits = 1 }},
		{name: "cache misses", mutate: func(execution *CompileGroupExecution) { execution.CacheMisses = 1 }},
		{name: "cache puts", mutate: func(execution *CompileGroupExecution) { execution.CachePuts = 1 }},
		{name: "command digest", mutate: func(execution *CompileGroupExecution) { execution.CompileCommandDigest = digestPlanLog([]byte("argv")) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			execution := base
			mutation.mutate(&execution)
			if err := execution.Validate(); err == nil {
				t.Fatal("forged unstarted compile evidence passed execution validation")
			}
			if err := validateCompileGroupExecutionList([]CompileGroupExecution{execution}); err == nil {
				t.Fatal("forged unstarted compile evidence passed report validation")
			}
			report.CompileGroupExecutions[0] = execution
			if _, err := EncodePlanExecutionReportChunks(report); err == nil {
				t.Fatal("forged unstarted compile evidence passed wire encoding")
			}
		})
	}
}

// TestCompileGroupReportPacksFourHundredElevenWorkloadsWithinTransportBudget 验证长 selector 集合在单编译组内完整往返。
func TestCompileGroupReportPacksFourHundredElevenWorkloadsWithinTransportBudget(t *testing.T) {
	report, expected := fourHundredElevenCompileGroupReport(t)
	assertPackedWorkloadRecords(t, report.CompileGroupExecutions[0].WorkloadIDs)
	chunks := encodePackedCompileGroupReport(t, report)
	assertPackedCompileGroupTransportBudget(t, report, chunks)
	assertPackedCompileGroupRoundTrip(t, report, expected, chunks)
}

// TestCompileGroupReportPacksFourHundredElevenArchtestAndCodexappGates 验证完整混合报告保持在 ECI 传输上限内。
func TestCompileGroupReportPacksFourHundredElevenArchtestAndCodexappGates(t *testing.T) {
	report, expected := fourHundredElevenArchtestAndCodexappReport(t)
	chunks := encodePackedCompileGroupReport(t, report)
	assertPackedCompileGroupTransportBudget(t, report, chunks)
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, expected)
	if err != nil {
		t.Fatalf("decode mixed provider report: %v", err)
	}
	if len(decoded.Gates) != len(expected) || len(decoded.CompileGroupExecutions) != 2 {
		t.Fatalf("decoded mixed provider report gates=%d compile_groups=%d", len(decoded.Gates), len(decoded.CompileGroupExecutions))
	}
	for index, gate := range decoded.Gates {
		if gate.GateID != expected[index] || len(gate.TestTimings) != 1 || len(gate.Log) == 0 {
			t.Fatalf("decoded mixed provider gate %d drifted: %#v", index, gate)
		}
	}
	for groupIndex, group := range decoded.CompileGroupExecutions {
		if !slices.Equal(group.WorkloadIDs, report.CompileGroupExecutions[groupIndex].WorkloadIDs) {
			t.Fatalf("decoded mixed provider workload IDs drifted for group %d", groupIndex)
		}
	}
}

func TestCompileGroupReportPacksFiveHundredThirtyNineCodexappFailuresWithinTransportBudget(t *testing.T) {
	report, expected := compileGroupReportWithBoundedFailureLogs(t, 539, 14)
	chunks := encodePackedCompileGroupReport(t, report)
	assertPackedCompileGroupTransportBudget(t, report, chunks)
	assertPackedCompileGroupRoundTrip(t, report, expected, chunks)
}

func compileGroupReportWithBoundedFailureLogs(t *testing.T, workloadCount, failureCount int) (PlanExecutionReport, []GateID) {
	t.Helper()
	now := executorPlanTestNow()
	report := PlanExecutionReport{SchemaVersion: ExecutorPlanReportSchemaVersion, Profile: testExecutorPlanRequest(t).profile, PlanDigest: testExecutorPlanRequest(t).planDigest, ExecutionOutcome: SuccessfulWorkerExecutionOutcome()}
	execution := successfulCompileGroupExecutionFixture()
	execution.PackageTarget = AtomicCodexAppPackageTarget
	execution.WorkloadIDs = make([]GateID, 0, workloadCount)
	expected := make([]GateID, 0, workloadCount)
	for index := range workloadCount {
		gateID, result := compileGroupSelectorGateForTarget(t, now, AtomicCodexAppPackageTarget, "TestCodexappFailure", index)
		logBytes := executorPlanSuccessfulSelectorLogBytes
		if index == 0 {
			logBytes = executorPlanMaxLogBytes
		}
		result.Log = bytes.Repeat([]byte{'x'}, logBytes)
		result.LogDigest = digestPlanLog(result.Log)
		if index < failureCount {
			result.Status, result.ExitCode = ResultStatusFailed, 1
			result.TestTimings[0].Status = GoTestStatusFail
		}
		execution.WorkloadIDs = append(execution.WorkloadIDs, gateID)
		expected = append(expected, gateID)
		report.Gates = append(report.Gates, result)
	}
	report.CompileGroupExecutions = []CompileGroupExecution{execution}
	return report, expected
}

// fourHundredElevenCompileGroupReport 构造 411 个真实 Go 测试 workload 及合法 gate 证据。
func fourHundredElevenCompileGroupReport(t *testing.T) (PlanExecutionReport, []GateID) {
	t.Helper()
	const workloadCount = 411
	request := testExecutorPlanRequest(t)
	now := executorPlanTestNow()
	report := PlanExecutionReport{
		SchemaVersion:    ExecutorPlanReportSchemaVersion,
		Profile:          request.profile,
		PlanDigest:       request.planDigest,
		ExecutionOutcome: SuccessfulWorkerExecutionOutcome(),
		Gates:            make([]PlanGateExecution, 0, workloadCount),
	}
	expected := make([]GateID, 0, workloadCount)
	execution := successfulCompileGroupExecutionFixture()
	execution.PackageTarget = "./internal/archtest"
	execution.WorkloadIDs = make([]GateID, 0, workloadCount)
	for index := range workloadCount {
		gateID, gate := compileGroupSelectorGate(t, now, index)
		execution.WorkloadIDs = append(execution.WorkloadIDs, gateID)
		expected = append(expected, gateID)
		report.Gates = append(report.Gates, gate)
	}
	report.CompileGroupExecutions = []CompileGroupExecution{execution}
	return report, expected
}

// fourHundredElevenArchtestAndCodexappReport builds two realistic 411-selector compile groups.
func fourHundredElevenArchtestAndCodexappReport(t *testing.T) (PlanExecutionReport, []GateID) {
	t.Helper()
	request := testExecutorPlanRequest(t)
	now := executorPlanTestNow()
	report := PlanExecutionReport{SchemaVersion: ExecutorPlanReportSchemaVersion, Profile: request.profile, PlanDigest: request.planDigest, ExecutionOutcome: SuccessfulWorkerExecutionOutcome()}
	segments := []struct {
		packageTarget string
		namePrefix    string
	}{
		{packageTarget: "./internal/archtest", namePrefix: "TestArchtestMixed"},
		{packageTarget: "./internal/provider/codexapp", namePrefix: "TestCodexappMixed"},
	}
	report.Gates = make([]PlanGateExecution, 0, 822)
	report.CompileGroupExecutions = make([]CompileGroupExecution, 0, len(segments))
	expected := make([]GateID, 0, 822)
	for segmentIndex, segment := range segments {
		execution := successfulCompileGroupExecutionFixture()
		execution.GroupID = digestPlanLog(fmt.Appendf(nil, "mixed-group-%d", segmentIndex))
		execution.ArtifactKey = digestPlanLog(fmt.Appendf(nil, "mixed-artifact-%d", segmentIndex))
		execution.PackageTarget = segment.packageTarget
		execution.WorkloadIDs = make([]GateID, 0, 411)
		for index := range 411 {
			gateID, gate := compileGroupSelectorGateForTarget(t, now, segment.packageTarget, segment.namePrefix, index)
			execution.WorkloadIDs = append(execution.WorkloadIDs, gateID)
			expected = append(expected, gateID)
			report.Gates = append(report.Gates, gate)
		}
		report.CompileGroupExecutions = append(report.CompileGroupExecutions, execution)
	}
	return report, expected
}

// compileGroupSelectorGate 构造一个带长 canonical workload ID 的最小合法 gate 结果。
func compileGroupSelectorGate(t *testing.T, now time.Time, index int) (GateID, PlanGateExecution) {
	return compileGroupSelectorGateForTarget(t, now, "./internal/archtest", "TestCompileGroupSelector", index)
}

// compileGroupSelectorGateForTarget creates one bounded selector result for a realistic package target.
func compileGroupSelectorGateForTarget(t *testing.T, now time.Time, packageTarget, namePrefix string, index int) (GateID, PlanGateExecution) {
	t.Helper()
	testName := namePrefix + formatCompileGroupWorkloadIndex(index)
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, packageTarget, testName, 1)
	if err != nil {
		t.Fatalf("new Go test workload %d: %v", index, err)
	}
	gateID := GateID(workload.ID)
	log := PlainTextLog("ok: " + testName + " passed\n")
	return gateID, PlanGateExecution{
		GateID: gateID, Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: now, CompletedAt: now.Add(3 * time.Millisecond),
		Log: log, LogDigest: digestPlanLog(log),
		TestTimings: []GoTestTiming{{Name: testName, Status: GoTestStatusPass, DurationMS: 2}},
		ExecutionProfile: ExecutionProfile{
			CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured",
			CacheMissCount: 1, StartupMS: 1, TestBodyMS: 2, TotalMS: 3,
		},
	}
}

// assertPackedWorkloadRecords 断言长 workload ID 没有退化为逐 ID 单记录编码。
func assertPackedWorkloadRecords(t *testing.T, workloadIDs []GateID) {
	t.Helper()
	workloadRecords, err := encodePlanCompileGroupWorkloadRecords(1, workloadIDs)
	if err != nil {
		t.Fatalf("encode packed workload records: %v", err)
	}
	if len(workloadRecords) >= len(workloadIDs)/2 {
		t.Fatalf("packed workload records = %d, want less than half of %d long selector IDs", len(workloadRecords), len(workloadIDs))
	}
}

// encodePackedCompileGroupReport 编码完整报告，失败时直接终止测试。
func encodePackedCompileGroupReport(t *testing.T, report PlanExecutionReport) []string {
	t.Helper()
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	return chunks
}

// assertPackedCompileGroupTransportBudget 校验记录数、单行长度和总输出上限。
func assertPackedCompileGroupTransportBudget(t *testing.T, report PlanExecutionReport, chunks []string) {
	t.Helper()
	recordLimit, err := planExecutionReportRecordLimit(len(report.Gates), len(report.CompileGroupExecutions))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) >= executorPlanMaxTransportRecords || len(chunks) > recordLimit {
		t.Fatalf("report records = %d, want <= %d", len(chunks), recordLimit)
	}
	totalBytes := 0
	for index, chunk := range chunks {
		if len(chunk)+1 > executorPlanReportMaxLineBytes {
			t.Fatalf("report record %d line bytes = %d, want <= %d", index, len(chunk)+1, executorPlanReportMaxLineBytes)
		}
		totalBytes += len(chunk) + 1
	}
	if totalBytes > executorPlanReportMaxOutputBytes {
		t.Fatalf("report output bytes = %d, want <= %d", totalBytes, executorPlanReportMaxOutputBytes)
	}
}

// assertPackedCompileGroupRoundTrip 校验 Gate 证据和 workload 顺序在完整报告往返后保持不变。
func assertPackedCompileGroupRoundTrip(t *testing.T, report PlanExecutionReport, expected []GateID, chunks []string) {
	t.Helper()
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, expected)
	if err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !slices.EqualFunc(decoded.Gates, report.Gates, func(left, right PlanGateExecution) bool {
		return left.GateID == right.GateID && slices.Equal(left.TestTimings, right.TestTimings) && slices.Equal(left.Log, right.Log)
	}) {
		t.Fatal("decoded gate order, test timings, or log evidence drifted")
	}
	if len(decoded.CompileGroupExecutions) != 1 || !slices.Equal(decoded.CompileGroupExecutions[0].WorkloadIDs, report.CompileGroupExecutions[0].WorkloadIDs) {
		t.Fatalf("decoded packed workloads = %#v, want %d ordered IDs", decoded.CompileGroupExecutions, len(expected))
	}
}

func TestDecodeCompileGroupWorkloadsRejectsMissingDuplicateOrMixedPackedRecords(t *testing.T) {
	tests := []struct {
		name    string
		records []planReportRecord
	}{
		{
			name: "out of order",
			records: []planReportRecord{{kind: planReportCompileWorkloadRecord,
				payload: "000001 000002 000003 000003 workload-a workload-b workload-c"}},
		},
		{
			name: "total mismatch",
			records: []planReportRecord{{kind: planReportCompileWorkloadRecord,
				payload: "000001 000001 000004 000003 workload-a workload-b workload-c"}},
		},
		{
			name: "missing",
			records: []planReportRecord{{kind: planReportCompileWorkloadRecord,
				payload: "000001 000001 000003 000002 workload-a workload-b"}},
		},
		{
			name: "duplicate",
			records: []planReportRecord{
				{kind: planReportCompileWorkloadRecord, payload: "000001 000001 000003 000002 workload-a workload-b"},
				{kind: planReportCompileWorkloadRecord, payload: "000001 000003 000003 000001 workload-b"},
			},
		},
		{
			name: "mixed group",
			records: []planReportRecord{
				{kind: planReportCompileWorkloadRecord, payload: "000001 000001 000003 000002 workload-a workload-b"},
				{kind: planReportCompileWorkloadRecord, payload: "000002 000003 000003 000001 workload-c"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := decodeCompileGroupWorkloads(test.records, 0, 1, 3, make(map[GateID]struct{})); err == nil {
				t.Fatal("decoder accepted malformed packed workload records")
			}
		})
	}
}

func formatCompileGroupWorkloadIndex(index int) string {
	return fmt.Sprintf("%03d", index)
}
