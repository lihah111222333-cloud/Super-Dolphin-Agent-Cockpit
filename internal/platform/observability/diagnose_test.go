package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDiagnoseTraceRequiresTraceID(t *testing.T) {
	svc := NewService(diagnosisTestConfig())
	_, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{})
	if err == nil {
		t.Fatal("DiagnoseTrace returned nil error for empty trace id")
	}
	if !errors.Is(err, ErrTraceDiagnosisMissingTraceID) {
		t.Fatalf("DiagnoseTrace error = %v, want ErrTraceDiagnosisMissingTraceID", err)
	}
}

func TestDiagnoseTraceFailsFastOnNilService(t *testing.T) {
	var svc *Service
	_, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace"})
	if err == nil {
		t.Fatal("DiagnoseTrace returned nil error for nil service")
	}
	if !errors.Is(err, ErrTraceDiagnosisServiceUnavailable) {
		t.Fatalf("DiagnoseTrace error = %v, want ErrTraceDiagnosisServiceUnavailable", err)
	}
}

func TestDiagnoseTraceReportsDisabledServiceAsDegraded(t *testing.T) {
	svc := NewDisabledService(Config{DisabledReason: "tracing off", QueryTailTimeoutMS: 100, QueryTailMaxConcurrency: 1})
	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: strings.Repeat("t", TraceDiagnosisMaxStringBytes+64)})
	if err != nil {
		t.Fatalf("DiagnoseTrace disabled: %v", err)
	}
	if diagnosis.TraceID != strings.Repeat("t", TraceDiagnosisMaxStringBytes) {
		t.Fatalf("TraceID bytes = %d, want capped to %d", len(diagnosis.TraceID), TraceDiagnosisMaxStringBytes)
	}
	if diagnosis.Source != TraceDiagnosisSourceNone || !diagnosis.Degraded || diagnosis.TailError != "tracing off" {
		t.Fatalf("disabled diagnosis = %+v, want degraded source none with disabled reason", diagnosis)
	}
	payload, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatalf("Marshal disabled diagnosis: %v", err)
	}
	if len(payload) > TraceDiagnosisMaxSerializedBytes {
		t.Fatalf("disabled payload bytes = %d, want <= %d", len(payload), TraceDiagnosisMaxSerializedBytes)
	}
}

func TestDiagnoseTraceContractBoundsAndSummaryShape(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	base := time.Unix(1700000000, 0)
	for _, event := range []TraceEvent{
		{
			Timestamp:  base,
			TraceID:    "trace-contract",
			SpanID:     "span-ok",
			Method:     "GET /ok",
			ThreadID:   "thread-1",
			AgentID:    "agent-1",
			TurnID:     "turn-1",
			CallID:     "call-1",
			ToolName:   "tool-1",
			DurationMS: 12,
			Status:     StatusOK,
			Metadata:   map[string]any{"raw": "metadata"},
		},
		{
			Timestamp:  base.Add(time.Second),
			TraceID:    "trace-contract",
			SpanID:     "span-slow",
			Method:     "POST /slow",
			DurationMS: 2500,
			Status:     StatusSlow,
		},
		{
			Timestamp:  base.Add(2 * time.Second),
			TraceID:    "trace-contract",
			SpanID:     "span-error",
			Method:     "POST /error",
			DurationMS: 99,
			Status:     StatusError,
			Error:      "request failed",
			Stack:      []StackFrame{{File: "internal/platform/observability/service.go", Function: "observability.Service.Query", Line: 152}},
		},
	} {
		if err := svc.Record(context.Background(), event); err != nil {
			t.Fatalf("Record(%s): %v", event.SpanID, err)
		}
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{
		TraceID:       "trace-contract",
		Limit:         TraceDiagnosisMaxLimit + 1,
		IncludeStack:  true,
		CWD:           "/workspace/project",
		WorkspaceRoot: "/workspace/project",
	})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}

	assertTraceDiagnosisContractSummary(t, diagnosis)
	assertTraceDiagnosisRelatedIDs(t, diagnosis)
	assertTraceDiagnosisMemoryTailFlags(t, diagnosis)
}

func TestDiagnoseTraceDefaultLimitAndNoRawEvents(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "trace-default", Method: "GET /default", Status: StatusOK, Metadata: map[string]any{"raw": "metadata"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-default"})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	assertTraceDiagnosisDefaultLimitAndNoRawEvents(t, diagnosis)
}

func TestDiagnoseTraceEnforcesPayloadBound(t *testing.T) {
	cfg := diagnosisTestConfig()
	cfg.IndexMaxEvents = 256
	cfg.IndexMaxTraceEvents = 256
	cfg.StringMaxBytes = 8192
	svc := NewService(cfg, WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	for n := 0; n < 220; n++ {
		event := TraceEvent{TraceID: "trace-large", SpanID: "span-" + strings.Repeat("x", 32), Method: strings.Repeat("m", 8192), Status: StatusOK}
		if err := svc.Record(context.Background(), event); err != nil {
			t.Fatalf("Record large event %d: %v", n, err)
		}
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-large", Limit: TraceDiagnosisMaxLimit})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	payload, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatalf("Marshal diagnosis: %v", err)
	}
	if len(payload) > TraceDiagnosisMaxSerializedBytes {
		t.Fatalf("payload bytes = %d, want <= %d", len(payload), TraceDiagnosisMaxSerializedBytes)
	}
	if !diagnosis.Truncated {
		t.Fatal("Truncated = false, want true after payload-bound trimming")
	}
}

func TestDiagnoseTraceBoundsTopLevelTraceID(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	longTraceID := strings.Repeat("t", TraceDiagnosisMaxStringBytes+64)
	if err := svc.Record(context.Background(), TraceEvent{TraceID: longTraceID, Method: "GET /large-trace", Status: StatusOK}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: longTraceID})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	if len(diagnosis.TraceID) != TraceDiagnosisMaxStringBytes {
		t.Fatalf("TraceID bytes = %d, want %d", len(diagnosis.TraceID), TraceDiagnosisMaxStringBytes)
	}
	if len(diagnosis.Timeline) != 1 {
		t.Fatalf("timeline len = %d, want 1", len(diagnosis.Timeline))
	}
}

func TestDiagnoseTraceBoundsStatusStrings(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	largeStatus := Status(strings.Repeat("s", TraceDiagnosisMaxStringBytes+64))
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "trace-status", Method: "GET /large-status", Status: largeStatus}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-status"})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	status := string(diagnosis.Timeline[0].Status)
	if len(status) != TraceDiagnosisMaxStringBytes {
		t.Fatalf("status bytes = %d, want %d", len(status), TraceDiagnosisMaxStringBytes)
	}
}

func TestDiagnoseTraceCapsRelatedIDsAndStackFrames(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	for n := 0; n < TraceDiagnosisMaxRelatedIDs+5; n++ {
		event := TraceEvent{TraceID: "trace-caps", ThreadID: fmt.Sprintf("thread-%d", n), ToolName: fmt.Sprintf("tool-%d", n), Status: StatusOK}
		if err := svc.Record(context.Background(), event); err != nil {
			t.Fatalf("Record caps event %d: %v", n, err)
		}
	}
	stack := make([]StackFrame, TraceDiagnosisMaxStackFrames+5)
	for n := range stack {
		stack[n] = StackFrame{File: "internal/platform/observability/diagnose.go", Function: "fn", Line: n + 1}
	}
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "trace-caps", Status: StatusError, Error: "boom", Stack: stack}); err != nil {
		t.Fatalf("Record stack event: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-caps", Limit: TraceDiagnosisMaxLimit, IncludeStack: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	if len(diagnosis.RelatedIDs.ThreadIDs) > TraceDiagnosisMaxRelatedIDs || len(diagnosis.RelatedIDs.ToolNames) > TraceDiagnosisMaxRelatedIDs {
		t.Fatalf("related ids = %+v, want capped to %d", diagnosis.RelatedIDs, TraceDiagnosisMaxRelatedIDs)
	}
	if got := len(diagnosis.ErrorSummaries[0].Stack); got != TraceDiagnosisMaxStackFrames {
		t.Fatalf("stack frames = %d, want %d", got, TraceDiagnosisMaxStackFrames)
	}
}

func TestDiagnoseTraceCapsSummaryCounts(t *testing.T) {
	cfg := diagnosisTestConfig()
	cfg.IndexMaxEvents = 256
	cfg.IndexMaxTraceEvents = 256
	cfg.IndexMaxSlowEvents = 256
	cfg.IndexMaxErrorEvents = 256
	svc := NewService(cfg, WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	for n := 0; n < TraceDiagnosisMaxLimit; n++ {
		status := StatusSlow
		if n >= 80 && n < 160 {
			status = StatusError
		}
		if n >= 160 {
			status = StatusPanic
		}
		event := TraceEvent{TraceID: "trace-summary-caps", SpanID: fmt.Sprintf("span-%d", n), Method: fmt.Sprintf("method-%d", n), Status: status}
		if err := svc.Record(context.Background(), event); err != nil {
			t.Fatalf("Record summary event %d: %v", n, err)
		}
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-summary-caps", Limit: TraceDiagnosisMaxLimit})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	if len(diagnosis.SlowSummaries) != TraceDiagnosisMaxSlowSummaries {
		t.Fatalf("slow summaries = %d, want %d", len(diagnosis.SlowSummaries), TraceDiagnosisMaxSlowSummaries)
	}
	if len(diagnosis.ErrorSummaries) != TraceDiagnosisMaxErrorSummaries {
		t.Fatalf("error summaries = %d, want %d", len(diagnosis.ErrorSummaries), TraceDiagnosisMaxErrorSummaries)
	}
	if len(diagnosis.PanicSummaries) != TraceDiagnosisMaxPanicSummaries {
		t.Fatalf("panic summaries = %d, want %d", len(diagnosis.PanicSummaries), TraceDiagnosisMaxPanicSummaries)
	}
}

func TestDiagnoseTraceAppliesBaselineRedactionAndPathScrub(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	if err := svc.Record(context.Background(), TraceEvent{
		TraceID: "trace-redact",
		Method:  "open /home/alice/secret.txt token=abc123 alice@example.com",
		Status:  StatusError,
		Error:   "failed at /tmp/private.json from 10.0.0.1",
		Code:    CodeAnchor{File: "/workspace/project/internal/app.go", Function: "handler", Line: 7},
		Stack:   []StackFrame{{File: "/home/alice/outside.go", Function: "panicFn", Line: 9}},
	}); err != nil {
		t.Fatalf("Record redaction event: %v", err)
	}
	if err := svc.Record(context.Background(), TraceEvent{
		TraceID: "trace-redact",
		Method:  "relative-path-redaction",
		Status:  StatusError,
		Code:    CodeAnchor{File: "relative/alice@example.com/token=abc123.go", Function: "handler", Line: 8},
		Stack:   []StackFrame{{File: "stack/10.0.0.1/phone-555-111-2222.go", Function: "panicFn", Line: 10}},
	}); err != nil {
		t.Fatalf("Record relative redaction event: %v", err)
	}
	if err := svc.Record(context.Background(), TraceEvent{
		TraceID: "trace-redact",
		Method:  `foreign paths C:\Users\alice\secret.go C:/Users/alice/secret.go /srv/alice/private.log ~/secret.txt \\host\share\file.txt`,
		Status:  StatusError,
		Code:    CodeAnchor{File: `C:\Users\alice\secret.go`, Function: "handler", Line: 9},
		Stack:   []StackFrame{{File: "/srv/alice/private.log", Function: "panicFn", Line: 11}},
	}); err != nil {
		t.Fatalf("Record foreign path event: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-redact", IncludeStack: true, WorkspaceRoot: "/workspace/project"})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	payload, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatalf("Marshal diagnosis: %v", err)
	}
	text := string(payload)
	assertDiagnosisDoesNotLeak(t, text, []string{"/home/alice", "/tmp/private", "/srv/alice", "C:\\Users\\alice", "C:/Users/alice", "\\\\host\\share", "~/secret", "alice@example.com", "token=abc123", "10.0.0.1"})
	if strings.Contains(text, "555-111-2222") {
		t.Fatalf("diagnosis leaked phone-like relative path text: %s", text)
	}
	if diagnosis.Timeline[0].Code.File != "internal/app.go" {
		t.Fatalf("code file = %q, want repo-relative internal/app.go", diagnosis.Timeline[0].Code.File)
	}
	if diagnosis.ErrorSummaries[0].Stack[0].File != redactedPath {
		t.Fatalf("stack file = %q, want %s", diagnosis.ErrorSummaries[0].Stack[0].File, redactedPath)
	}
}

func TestDiagnoseTraceRedactsExtendedModelFacingPII(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	if err := svc.Record(context.Background(), TraceEvent{
		TraceID: "trace-extended-redact",
		Method:  "provider.turn.run dial github.com API.Internal.Example.COM api.internal alice-mbp.local from /workspace/project-secret/private.go",
		Status:  StatusError,
		Error:   "host github.com API.Internal.Example.COM api.internal alice-mbp.local failed token=abc123",
		Code:    CodeAnchor{File: "/workspace/project-secret/private.go", Function: "handler", Line: 12},
		Stack:   []StackFrame{{File: "/workspace/project/../project-secret/stack.go", Function: "panicFn", Line: 13}},
		Metadata: map[string]any{
			"component":        "observability",
			"user_email":       "alice@example.com",
			"session_token":    "token=abc123",
			"unsafe key\nname": "api.internal.example.com",
		},
	}); err != nil {
		t.Fatalf("Record extended redaction event: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-extended-redact", IncludeStack: true, WorkspaceRoot: "/workspace/project"})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	text := marshalDiagnosisText(t, diagnosis)
	assertDiagnosisDoesNotLeak(t, text, []string{"/workspace/project-secret", "github.com", "API.Internal.Example.COM", "api.internal", "alice-mbp.local", "token=abc123", "alice@example.com", "session_token", "user_email", "unsafe key", "\"metadata\"", "component"})
	if !strings.Contains(text, "provider.turn.run") {
		t.Fatalf("diagnosis redacted dotted operation name unexpectedly: %s", text)
	}
	if diagnosis.Timeline[0].Code.File != redactedPath {
		t.Fatalf("sibling-prefix code file = %q, want %s", diagnosis.Timeline[0].Code.File, redactedPath)
	}
	if diagnosis.ErrorSummaries[0].Stack[0].File != redactedPath {
		t.Fatalf("dotdot stack file = %q, want %s", diagnosis.ErrorSummaries[0].Stack[0].File, redactedPath)
	}
}

func TestDiagnoseTraceRedactsTailDiagnosticsPII(t *testing.T) {
	tail := QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		return QueryResult{
			Source: QuerySourceJSONLTail,
			TailDecodeErrors: []TailDecodeError{{
				File:     "/workspace/project-secret/trace.jsonl",
				Line:     7,
				Trailing: true,
				Error:    "dial Github.COM api.internal token=abc123",
			}},
		}, errors.New("open /workspace/project-secret/trace.jsonl on Github.COM api.internal token=abc123")
	})
	svc := NewService(diagnosisTestConfig(), WithTailReader(tail))

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "trace-tail-redact", ForceRefresh: true, WorkspaceRoot: "/workspace/project"})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	text := marshalDiagnosisText(t, diagnosis)
	assertDiagnosisDoesNotLeak(t, text, []string{"/workspace/project-secret", "Github.COM", "api.internal", "token=abc123"})
	if diagnosis.TailError == "" || len(diagnosis.TailWarnings) != 1 {
		t.Fatalf("tail diagnostics = error %q warnings %#v, want visible redacted diagnostics", diagnosis.TailError, diagnosis.TailWarnings)
	}
}

func marshalDiagnosisText(t *testing.T, diagnosis TraceDiagnosis) string {
	t.Helper()
	payload, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatalf("Marshal diagnosis: %v", err)
	}
	return string(payload)
}

func assertDiagnosisDoesNotLeak(t *testing.T, text string, leakedValues []string) {
	t.Helper()
	for _, leaked := range leakedValues {
		if strings.Contains(text, leaked) {
			t.Fatalf("diagnosis leaked %q: %s", leaked, text)
		}
	}
}

func assertTraceDiagnosisContractSummary(t *testing.T, diagnosis TraceDiagnosis) {
	t.Helper()
	if diagnosis.TraceID != "trace-contract" {
		t.Fatalf("TraceID = %q, want trace-contract", diagnosis.TraceID)
	}
	if diagnosis.Limit != TraceDiagnosisMaxLimit {
		t.Fatalf("Limit = %d, want max %d", diagnosis.Limit, TraceDiagnosisMaxLimit)
	}
	if diagnosis.Source != TraceDiagnosisSourceMemory {
		t.Fatalf("Source = %q, want memory", diagnosis.Source)
	}
	if len(diagnosis.Timeline) != 3 {
		t.Fatalf("timeline len = %d, want 3", len(diagnosis.Timeline))
	}
	if len(diagnosis.SlowSummaries) != 1 {
		t.Fatalf("slow summaries len = %d, want 1", len(diagnosis.SlowSummaries))
	}
	if len(diagnosis.ErrorSummaries) != 1 {
		t.Fatalf("error summaries len = %d, want 1", len(diagnosis.ErrorSummaries))
	}
	if len(diagnosis.ErrorSummaries[0].Stack) != 1 {
		t.Fatalf("error stack len = %d, want 1", len(diagnosis.ErrorSummaries[0].Stack))
	}
}

func assertTraceDiagnosisRelatedIDs(t *testing.T, diagnosis TraceDiagnosis) {
	t.Helper()
	if diagnosis.RelatedIDs.ThreadIDs[0] != "thread-1" || diagnosis.RelatedIDs.ToolNames[0] != "tool-1" {
		t.Fatalf("related ids = %+v, want thread/tool ids", diagnosis.RelatedIDs)
	}
}

func assertTraceDiagnosisMemoryTailFlags(t *testing.T, diagnosis TraceDiagnosis) {
	t.Helper()
	if diagnosis.TailAttempted || diagnosis.Degraded || diagnosis.TailFresh || diagnosis.TailTimedOut || diagnosis.TailTruncated {
		t.Fatalf("tail status flags = %+v, want all false for memory-only contract test", diagnosis)
	}
}

func assertTraceDiagnosisDefaultLimitAndNoRawEvents(t *testing.T, diagnosis TraceDiagnosis) {
	t.Helper()
	if diagnosis.Limit != TraceDiagnosisDefaultLimit {
		t.Fatalf("Limit = %d, want default %d", diagnosis.Limit, TraceDiagnosisDefaultLimit)
	}
	if len(diagnosis.Timeline) != 1 {
		t.Fatalf("timeline len = %d, want 1", len(diagnosis.Timeline))
	}
	payload, err := json.Marshal(diagnosis)
	if err != nil {
		t.Fatalf("Marshal diagnosis: %v", err)
	}
	if strings.Contains(string(payload), "metadata") {
		t.Fatalf("diagnosis payload contains raw metadata field: %s", string(payload))
	}
}

func TestDiagnoseTraceRedactsUnixSlashPaths(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithSampler(NewSampler(SamplerConfig{HighFrequencyKeepEvery: 1})))
	if err := svc.Record(context.Background(), TraceEvent{
		TraceID: "trace-slash",
		Method:  fmt.Sprintf("open /workspace/project/internal/handler.go"),
		Status:  StatusError,
		Error:   "failed at /workspace/project/pkg/util.go",
		Code:    CodeAnchor{File: "/workspace/project/internal/handler.go", Function: "Handle", Line: 42},
		Stack:   []StackFrame{{File: "/home/user/outside-project/leak.go", Function: "caller", Line: 5}},
	}); err != nil {
		t.Fatalf("Record slash path event: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{
		TraceID:       "trace-slash",
		IncludeStack:  true,
		WorkspaceRoot: "/workspace/project",
	})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	text := marshalDiagnosisText(t, diagnosis)
	assertDiagnosisDoesNotLeak(t, text, []string{"/home/user/outside-project"})
	if diagnosis.Timeline[0].Code.File != "internal/handler.go" {
		t.Fatalf("slash path code file = %q, want repo-relative internal/handler.go", diagnosis.Timeline[0].Code.File)
	}
	if diagnosis.ErrorSummaries[0].Stack[0].File != redactedPath {
		t.Fatalf("slash path stack file = %q, want %s", diagnosis.ErrorSummaries[0].Stack[0].File, redactedPath)
	}
}

func diagnosisTestConfig() Config {
	return Config{
		IndexMaxEvents:          16,
		IndexMaxTraceEvents:     16,
		IndexMaxThreadEvents:    16,
		IndexMaxSlowEvents:      16,
		IndexMaxErrorEvents:     16,
		MetadataMaxBytes:        4096,
		StringMaxBytes:          512,
		QueryTailTimeoutMS:      100,
		QueryTailMaxConcurrency: 1,
	}
}
