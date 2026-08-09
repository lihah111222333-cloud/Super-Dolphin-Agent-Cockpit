package gate

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCompileGroupReportBoundsAllFailuresToOneFullLog 验证 539 个失败 selector 仍只保留一个完整诊断窗口。
func TestCompileGroupReportBoundsAllFailuresToOneFullLog(t *testing.T) {
	report, expected := compileGroupReportWithBoundedFailureLogs(t, 539, 539)
	for index := range report.Gates {
		report.Gates[index].Log = PlainTextLog(bytes.Repeat([]byte{'x'}, executorPlanMaxLogBytes))
		report.Gates[index].LogDigest = digestPlanLog(report.Gates[index].Log)
	}
	chunks := encodePackedCompileGroupReport(t, report)
	assertPackedCompileGroupTransportBudget(t, report, chunks)
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, expected)
	if err != nil {
		t.Fatalf("decode all-failure report: %v", err)
	}
	fullLogs := 0
	for index, result := range decoded.Gates {
		if index == 0 {
			if len(result.Log) != executorPlanMaxLogBytes {
				t.Fatalf("first failure log bytes = %d, want %d", len(result.Log), executorPlanMaxLogBytes)
			}
			fullLogs++
			continue
		}
		if len(result.Log) > executorPlanSuccessfulSelectorLogBytes {
			t.Fatalf("failure %d log bytes = %d, want <= %d", index, len(result.Log), executorPlanSuccessfulSelectorLogBytes)
		}
	}
	if fullLogs != 1 {
		t.Fatalf("full failure logs = %d, want 1", fullLogs)
	}
}

// TestCompileGroupReportBoundsPassLogsToSelectorBudget 验证 PASS selector 不会占用失败诊断窗口。
func TestCompileGroupReportBoundsPassLogsToSelectorBudget(t *testing.T) {
	report, expected := compileGroupReportWithBoundedFailureLogs(t, 539, 0)
	for index := range report.Gates {
		report.Gates[index].Log = PlainTextLog(bytes.Repeat([]byte{'p'}, executorPlanMaxLogBytes))
		report.Gates[index].LogDigest = digestPlanLog(report.Gates[index].Log)
	}
	chunks := encodePackedCompileGroupReport(t, report)
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, expected)
	if err != nil {
		t.Fatalf("decode all-pass report: %v", err)
	}
	for index, result := range decoded.Gates {
		if len(result.Log) > executorPlanSuccessfulSelectorLogBytes {
			t.Fatalf("pass %d log bytes = %d, want <= %d", index, len(result.Log), executorPlanSuccessfulSelectorLogBytes)
		}
	}
}

// TestDecodePlanExecutionReportRejectsOversizeResponse 验证解码端同样执行 1 MiB 累计输出预算。
func TestDecodePlanExecutionReportRejectsOversizeResponse(t *testing.T) {
	const recordCount = 1200
	digest := "sha256:" + strings.Repeat("a", 64)
	reportID := strings.TrimPrefix(digest, "sha256:")[:32]
	chunks := make([]string, recordCount)
	totalBytes := 0
	for index := range chunks {
		payload := "H " + strings.Repeat("x", 800)
		chunks[index] = fmt.Sprintf("%s%s %s %06d %06d %s", ExecutorPlanReportChunkPrefix, reportID, digest, index+1, recordCount, payload)
		if len(chunks[index])+1 > executorPlanReportMaxLineBytes {
			t.Fatalf("oversize fixture record %d exceeds line budget", index)
		}
		totalBytes += len(chunks[index]) + 1
	}
	if totalBytes <= executorPlanReportMaxOutputBytes {
		t.Fatalf("oversize fixture bytes = %d, want > %d", totalBytes, executorPlanReportMaxOutputBytes)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(chunks, []GateID{"oversize"}); err == nil || !strings.Contains(err.Error(), "remote log response limit") {
		t.Fatalf("DecodePlanExecutionReportChunksForGateSet() error = %v, want response-limit rejection", err)
	}
}

// TestDecodePlanExecutionReportRejectsBackendExecutionProfileTamper 验证 wire 中任一后端画像字段都受报告摘要保护。
func TestDecodePlanExecutionReportRejectsBackendExecutionProfileTamper(t *testing.T) {
	report, expected := fourHundredElevenCompileGroupReport(t)
	chunks := encodePackedCompileGroupReport(t, report)
	recordLimit, err := planExecutionReportRecordLimit(len(report.Gates), len(report.CompileGroupExecutions))
	if err != nil {
		t.Fatal(err)
	}
	records, _, digest, err := parsePlanReportRecords(chunks, recordLimit)
	if err != nil {
		t.Fatal(err)
	}
	index := firstPackedRecordIndex(records)
	if index < 0 {
		t.Fatal("packed gate record is missing")
	}
	fields := strings.SplitN(records[index].payload, " ", 15)
	if len(fields) != 15 {
		t.Fatalf("packed gate fields = %d, want 15", len(fields))
	}
	profileFields := strings.Split(fields[10], ",")
	if len(profileFields) != 15 {
		t.Fatalf("packed execution profile fields = %d, want 15", len(profileFields))
	}
	profileFields[6] = "2" // CacheMissCount: still a valid profile, but different from the signed report.
	fields[10] = strings.Join(profileFields, ",")
	records[index].payload = strings.Join(fields, " ")
	wireRecords := make([]string, len(records))
	for recordIndex, record := range records {
		wireRecords[recordIndex] = record.kind + " " + record.payload
	}
	tampered, err := framePlanReportRecords(wireRecords, digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(tampered, expected); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered backend profile error = %v, want digest rejection", err)
	}
}

func TestDecodePlanExecutionReportRejectsRaceGoFlagsTamper(t *testing.T) {
	report, expected := fourHundredElevenCompileGroupReport(t)
	chunks := encodePackedCompileGroupReport(t, report)
	recordLimit, err := planExecutionReportRecordLimit(len(report.Gates), len(report.CompileGroupExecutions))
	if err != nil {
		t.Fatal(err)
	}
	records, _, digest, err := parsePlanReportRecords(chunks, recordLimit)
	if err != nil {
		t.Fatal(err)
	}
	index := firstPackedRecordIndex(records)
	if index < 0 {
		t.Fatal("packed gate record is missing")
	}
	fields := strings.SplitN(records[index].payload, " ", 15)
	profileFields := strings.Split(fields[10], ",")
	if len(profileFields) != 15 {
		t.Fatalf("packed execution profile fields = %d, want 15", len(profileFields))
	}
	profileFields[0] = hex.EncodeToString([]byte(CanonicalGoFlags(true)))
	fields[10] = strings.Join(profileFields, ",")
	records[index].payload = strings.Join(fields, " ")
	wireRecords := make([]string, len(records))
	for recordIndex, record := range records {
		wireRecords[recordIndex] = record.kind + " " + record.payload
	}
	tampered, err := framePlanReportRecords(wireRecords, digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(tampered, expected); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered race GoFlags profile error = %v, want digest rejection", err)
	}
}

func TestDecodePlanExecutionReportRoundTripsRaceGoFlags(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestGuardWithRace, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	result := PlanGateExecution{
		GateID: GateID(workload.ID), Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: started, CompletedAt: started.Add(2 * time.Millisecond),
		ArgvDigest: "sha256:" + strings.Repeat("a", 64), LogDigest: digestPlanLog(nil),
		TestTimings: []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 1}},
		ExecutionProfile: ExecutionProfile{
			GoFlags: CanonicalGoFlags(true), CacheSource: "none", CacheStatus: CacheObservationNotApplicable,
			CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 1, TotalMS: 2,
		},
	}
	report := PlanExecutionReport{
		SchemaVersion: ExecutorPlanReportSchemaVersion, Profile: ProfileRelease,
		PlanDigest: "sha256:" + strings.Repeat("b", 64), ExecutionOutcome: SuccessfulWorkerExecutionOutcome(),
		Gates: []PlanGateExecution{result},
	}
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatalf("race report encode failed: %v", err)
	}
	decoded, err := DecodePlanExecutionReportChunksForGateSet(chunks, []GateID{GateID(workload.ID)})
	if err != nil {
		t.Fatalf("race report decode failed: %v", err)
	}
	if len(decoded.Gates) != 1 || decoded.Gates[0].ExecutionProfile.GoFlags != CanonicalGoFlags(true) {
		t.Fatalf("decoded race GoFlags = %#v, want %q", decoded.Gates, CanonicalGoFlags(true))
	}
}
