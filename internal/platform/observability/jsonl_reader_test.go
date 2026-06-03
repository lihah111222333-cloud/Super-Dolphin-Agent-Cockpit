package observability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailReaderReturnsEventsAndDecodeMetadataForMalformedTrailingLine(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "trace-2026-06-01.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "a", Status: StatusOK})
	appendRaw(t, filepath.Join(dir, "trace-2026-06-01.jsonl"), []byte(`{"schema_version":`))

	reader := JSONLTailReader{Dir: dir, MaxBytes: 1024 * 1024}
	result, err := reader.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].TraceID != "a" {
		t.Fatalf("Events = %#v, want valid event a", result.Events)
	}
	if len(result.DecodeErrors) != 1 {
		t.Fatalf("DecodeErrors = %#v, want one", result.DecodeErrors)
	}
	if !result.DecodeErrors[0].Trailing || result.DecodeErrors[0].Metadata["decode_error"] != true {
		t.Fatalf("decode metadata missing: %#v", result.DecodeErrors[0])
	}
}

func TestTailReaderIgnoresNonTraceFilesAndBoundsBytes(t *testing.T) {
	dir := t.TempDir()
	writeJSONL(t, filepath.Join(dir, "trace-2026-06-01.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "old", Status: StatusOK})
	writeJSONL(t, filepath.Join(dir, "trace-2026-06-02.jsonl"), TraceEvent{SchemaVersion: SchemaVersion, TraceID: "new", Status: StatusOK})
	if err := os.WriteFile(filepath.Join(dir, "trace-2026-06-03.jsonl.bak"), []byte("not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile non-trace: %v", err)
	}
	oldTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	_ = os.Chtimes(filepath.Join(dir, "trace-2026-06-01.jsonl"), oldTime, oldTime)
	_ = os.Chtimes(filepath.Join(dir, "trace-2026-06-02.jsonl"), newTime, newTime)

	info, err := os.Stat(filepath.Join(dir, "trace-2026-06-02.jsonl"))
	if err != nil {
		t.Fatalf("Stat newest: %v", err)
	}
	reader := JSONLTailReader{Dir: dir, MaxBytes: info.Size()}
	result, err := reader.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].TraceID != "new" {
		t.Fatalf("Events = %#v, want only newest event", result.Events)
	}
	if len(result.DecodeErrors) != 0 {
		t.Fatalf("DecodeErrors = %#v, want none", result.DecodeErrors)
	}
}

func TestTailReaderRejectsUnboundedTraceFileEnumeration(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i <= maxTailCandidateFiles; i++ {
		path := filepath.Join(dir, "trace-2026-06-"+twoDigit(i%30+1)+"-"+twoDigit(i)+".jsonl")
		writeJSONL(t, path, TraceEvent{SchemaVersion: SchemaVersion, TraceID: "trace", Status: StatusOK})
	}
	reader := JSONLTailReader{Dir: dir, MaxBytes: 1024 * 1024}
	if _, err := reader.Read(context.Background()); err == nil {
		t.Fatalf("Read accepted too many trace files")
	}
}

func TestTailReadStopsBetweenFileReadChunksWhenContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := cancelAfterFirstReadReader{cancel: cancel, data: strings.Repeat("x", 128*1024)}

	_, err := readTailFileBytes(ctx, &reader, 128*1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("readTailFileBytes error = %v, want context canceled", err)
	}
}

func TestTailParseStopsWhenContextCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var result TailReadResult

	err := parseTailLines(ctx, "trace.jsonl", []byte(`{"trace_id":"trace"}`+"\n"), &result)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parseTailLines error = %v, want context canceled", err)
	}
}

type cancelAfterFirstReadReader struct {
	cancel context.CancelFunc
	data   string
	read   bool
}

func (r *cancelAfterFirstReadReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	r.cancel()
	return copy(p, r.data), nil
}

func twoDigit(value int) string {
	return fmt.Sprintf("%02d", value)
}

func writeJSONL(t *testing.T, path string, event TraceEvent) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func appendRaw(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile(%s): %v", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("Write raw: %v", err)
	}
}
