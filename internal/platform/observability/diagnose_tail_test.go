package observability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDiagnoseTraceReportsTailFailureAsDegraded(t *testing.T) {
	svc := NewService(diagnosisTestConfig(), WithTailReader(QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		return QueryResult{Source: QuerySourceJSONLTail}, errors.New("open /home/alice/private/trace.jsonl: denied")
	})))
	if err := svc.Record(context.Background(), TraceEvent{TraceID: "tail-fail", Method: "memory", Status: StatusOK}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "tail-fail", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	assertTailFailureDiagnosis(t, diagnosis)
}

func TestDiagnoseTraceForceRefreshBypassesInflightTail(t *testing.T) {
	cfg := diagnosisTestConfig()
	cfg.QueryTailMaxConcurrency = 2
	cfg.QueryTailTimeoutMS = 1000
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	tail := QueryTailReaderFunc(func(ctx context.Context, query Query) (QueryResult, error) {
		if query.TraceID != "force-refresh" {
			return QueryResult{Source: QuerySourceJSONLTail}, errors.New("unexpected trace")
		}
		call := calls.Add(1)
		if call == 1 {
			close(started)
			if err := waitForTailRelease(ctx, release); err != nil {
				return QueryResult{Source: QuerySourceJSONLTail}, err
			}
			return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "force-refresh", Method: "stale-inflight"}}}, nil
		}
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{{TraceID: "force-refresh", Method: "fresh-force"}}}, nil
	})
	svc := NewService(cfg, WithTailReader(tail))
	ordinary := make(chan TraceDiagnosis, 1)
	ordinaryDone := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-ordinaryDone:
		case <-time.After(time.Second):
			t.Fatal("ordinary diagnosis goroutine did not stop")
		}
	})
	go func() {
		defer close(ordinaryDone)
		diagnosis, _ := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "force-refresh"})
		ordinary <- diagnosis
	}()
	waitForSignal(t, started, "ordinary diagnosis tail read")

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	fresh, err := svc.DiagnoseTrace(ctx, TraceDiagnosisRequest{TraceID: "force-refresh", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace force refresh: %v", err)
	}
	assertFreshTailDiagnosis(t, fresh, "fresh-force")
	assertAtomicValue(t, &calls, 2, "tail calls before release")

	close(release)
	old := receiveDiagnosis(t, ordinary)
	if got := diagnosisMethods(old); got != "stale-inflight" {
		t.Fatalf("ordinary methods = %q, want stale-inflight", got)
	}
}

func TestDiagnoseTraceForceRefreshSeesAppendedJSONL(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(diagnosisTestConfig(), WithTailReader(JSONLTailReader{Dir: dir, MaxBytes: 1024 * 1024}))

	first, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "jsonl-fresh", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace before append: %v", err)
	}
	if first.Source != TraceDiagnosisSourceNone || !first.TailAttempted || !first.TailFresh {
		t.Fatalf("first diagnosis = %+v, want fresh empty tail", first)
	}

	writeJSONL(t, filepath.Join(dir, "trace-2026-06-03.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "jsonl-fresh", Method: "from-disk", Status: StatusOK})
	second, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "jsonl-fresh", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace after append: %v", err)
	}
	if second.Source != TraceDiagnosisSourceTail {
		t.Fatalf("second source = %q, want tail", second.Source)
	}
	if got := diagnosisMethods(second); got != "from-disk" {
		t.Fatalf("second methods = %q, want from-disk", got)
	}
}

func TestDiagnoseTraceReportsTailWarningsAndCost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace-2026-06-03.jsonl")
	writeJSONL(t, path, TraceEvent{SchemaVersion: SchemaVersion, TraceID: "warn-tail", Method: "valid", Status: StatusOK})
	appendRaw(t, path, []byte(`{"schema_version":`))
	svc := NewService(diagnosisTestConfig(), WithTailReader(JSONLTailReader{Dir: dir, MaxBytes: 1024 * 1024}))

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "warn-tail", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	assertTailWarningDiagnosis(t, diagnosis, dir)
	if got := diagnosisMethods(diagnosis); got != "valid" {
		t.Fatalf("timeline methods = %q, want valid", got)
	}
}

func TestDiagnoseTraceSeparatesLimitAndTailWindowTruncation(t *testing.T) {
	limitedTail := QueryTailReaderFunc(func(context.Context, Query) (QueryResult, error) {
		return QueryResult{Source: QuerySourceJSONLTail, Events: []TraceEvent{
			{TraceID: "limit-tail", Method: "old", Status: StatusOK},
			{TraceID: "limit-tail", Method: "new", Status: StatusOK},
		}, Truncated: true}, nil
	})
	limitSvc := NewService(diagnosisTestConfig(), WithTailReader(limitedTail))
	limited, err := limitSvc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "limit-tail", Limit: 1, ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace limited: %v", err)
	}
	if !limited.Truncated || limited.TailTruncated {
		t.Fatalf("limited diagnosis = %+v, want top-level truncated only", limited)
	}

	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "trace-2026-06-02.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "window-tail", Method: "old", Status: StatusOK})
	writeJSONL(t, filepath.Join(dir, "trace-2026-06-03.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "window-tail", Method: "new", Status: StatusOK})
	newInfo := statFile(t, filepath.Join(dir, "trace-2026-06-03.jsonl"))
	windowSvc := NewService(diagnosisTestConfig(), WithTailReader(JSONLTailReader{Dir: dir, MaxBytes: newInfo.Size()}))
	windowed, err := windowSvc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "window-tail", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace windowed: %v", err)
	}
	if !windowed.TailTruncated || windowed.Truncated {
		t.Fatalf("windowed diagnosis = %+v, want tail truncated only", windowed)
	}
}

func TestDiagnoseTraceReportsTailTimeout(t *testing.T) {
	cfg := diagnosisTestConfig()
	cfg.QueryTailTimeoutMS = 1
	tail := QueryTailReaderFunc(func(ctx context.Context, _ Query) (QueryResult, error) {
		<-ctx.Done()
		return QueryResult{Source: QuerySourceJSONLTail, TailTimedOut: true}, ctx.Err()
	})
	svc := NewService(cfg, WithTailReader(tail))

	diagnosis, err := svc.DiagnoseTrace(context.Background(), TraceDiagnosisRequest{TraceID: "timeout-tail", ForceRefresh: true})
	if err != nil {
		t.Fatalf("DiagnoseTrace: %v", err)
	}
	if !diagnosis.TailAttempted || !diagnosis.Degraded || !diagnosis.TailTimedOut {
		t.Fatalf("timeout diagnosis = %+v, want attempted degraded timed out", diagnosis)
	}
	if diagnosis.TailDurationMS <= 0 || diagnosis.TailError == "" {
		t.Fatalf("timeout diagnosis duration/error = %d/%q, want visible", diagnosis.TailDurationMS, diagnosis.TailError)
	}
}

func assertTailFailureDiagnosis(t *testing.T, diagnosis TraceDiagnosis) {
	t.Helper()
	if !diagnosis.TailAttempted || !diagnosis.Degraded || diagnosis.TailFresh {
		t.Fatalf("tail flags = %+v, want attempted degraded non-fresh", diagnosis)
	}
	if diagnosis.Source != TraceDiagnosisSourceMemory {
		t.Fatalf("Source = %q, want memory fallback marked degraded", diagnosis.Source)
	}
	if diagnosis.TailError == "" || strings.Contains(diagnosis.TailError, "/home/alice") {
		t.Fatalf("TailError = %q, want scrubbed non-empty error", diagnosis.TailError)
	}
	if got := diagnosisMethods(diagnosis); got != "memory" {
		t.Fatalf("timeline methods = %q, want memory", got)
	}
}

func assertFreshTailDiagnosis(t *testing.T, diagnosis TraceDiagnosis, wantMethod string) {
	t.Helper()
	if got := diagnosisMethods(diagnosis); got != wantMethod {
		t.Fatalf("tail methods = %q, want %q", got, wantMethod)
	}
	if !diagnosis.TailAttempted || !diagnosis.TailFresh || diagnosis.Source != TraceDiagnosisSourceTail {
		t.Fatalf("tail flags = %+v, want fresh tail source", diagnosis)
	}
}

func assertAtomicValue(t *testing.T, value *atomic.Int32, want int32, label string) {
	t.Helper()
	if got := value.Load(); got != want {
		t.Fatalf("%s = %d, want %d", label, got, want)
	}
}

func assertTailWarningDiagnosis(t *testing.T, diagnosis TraceDiagnosis, dir string) {
	t.Helper()
	if !diagnosis.TailAttempted || !diagnosis.TailFresh || !diagnosis.Degraded {
		t.Fatalf("tail flags = %+v, want fresh degraded tail with warning", diagnosis)
	}
	if diagnosis.DecodeErrorCount != 1 || len(diagnosis.TailWarnings) != 1 {
		t.Fatalf("decode warnings = count %d warnings %#v, want one", diagnosis.DecodeErrorCount, diagnosis.TailWarnings)
	}
	if diagnosis.TailFilesScanned != 1 || diagnosis.TailBytesRead <= 0 || diagnosis.TailDurationMS < 0 {
		t.Fatalf("tail cost fields = files %d bytes %d duration %d", diagnosis.TailFilesScanned, diagnosis.TailBytesRead, diagnosis.TailDurationMS)
	}
	if strings.Contains(strings.Join(diagnosis.TailWarnings, "\n"), dir) {
		t.Fatalf("TailWarnings leaked temp dir: %#v", diagnosis.TailWarnings)
	}
}

func receiveDiagnosis(t *testing.T, diagnoses <-chan TraceDiagnosis) TraceDiagnosis {
	t.Helper()
	select {
	case diagnosis := <-diagnoses:
		return diagnosis
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for diagnosis")
		return TraceDiagnosis{}
	}
}

func diagnosisMethods(diagnosis TraceDiagnosis) string {
	values := make([]TraceEvent, 0, len(diagnosis.Timeline))
	for _, item := range diagnosis.Timeline {
		values = append(values, TraceEvent{Method: item.Method})
	}
	return methods(values)
}

func statFile(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	return info
}
