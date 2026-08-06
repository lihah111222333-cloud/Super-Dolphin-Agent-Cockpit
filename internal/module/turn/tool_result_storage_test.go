package turn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolResultRuntimeRequiresOwner(t *testing.T) {
	var toolResults *ToolResultRuntime
	defer func() {
		if recover() == nil {
			t.Fatal("Capture() did not panic for a nil tool result runtime")
		}
	}()
	toolResults.Capture(ToolResultMeta{}, "result")
}

func TestCaptureToolResultPersistsLargePayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	toolResults := NewToolResultRuntime()
	toolResults.ResetScope("thread-1", "turn-1")

	raw := strings.Repeat("x", toolResultPersistThresholdChars+32)
	record := toolResults.Capture(ToolResultMeta{
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		CallID:    "call-1",
		ToolName:  "shell",
		Timestamp: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
	}, raw)
	if record.PersistedPath == "" {
		t.Fatal("PersistedPath = empty, want stored large result")
	}
	if !record.Truncated {
		t.Fatal("Truncated = false, want true for large result preview")
	}
	if record.OriginalSize != len([]rune(raw)) {
		t.Fatalf("OriginalSize = %d, want %d", record.OriginalSize, len([]rune(raw)))
	}
	if got := len([]rune(record.Preview)); got != toolResultPersistThresholdChars {
		t.Fatalf("preview chars = %d, want %d", got, toolResultPersistThresholdChars)
	}
	stored, err := os.ReadFile(record.PersistedPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", record.PersistedPath, err)
	}
	if string(stored) != raw {
		t.Fatalf("stored payload mismatch: got %d bytes, want %d", len(stored), len(raw))
	}
}

func TestToolResultPersistFailurePropagatesToProviderRecord(t *testing.T) {
	homeFile := filepath.Join(t.TempDir(), "home-file")
	if err := os.WriteFile(homeFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", homeFile, err)
	}
	t.Setenv("HOME", homeFile)
	t.Setenv("XDG_CACHE_HOME", homeFile)
	toolResults := NewToolResultRuntime()
	toolResults.ResetScope("thread-persist-fail", "turn-persist-fail")

	raw := strings.Repeat("x", toolResultPersistThresholdChars+32)
	record := toolResults.Capture(ToolResultMeta{
		ThreadID:  "thread-persist-fail",
		TurnID:    "turn-persist-fail",
		CallID:    "call-persist-fail",
		ToolName:  "shell",
		Timestamp: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
	}, raw)
	if !record.Truncated {
		t.Fatal("Truncated = false, want true for large result preview")
	}
	if !record.PersistFailed {
		t.Fatalf("PersistFailed = false, want true; record=%+v", record)
	}
	if strings.TrimSpace(record.PersistError) == "" {
		t.Fatalf("PersistError = empty, want visible storage failure; record=%+v", record)
	}
	if record.PersistedPath != "" {
		t.Fatalf("PersistedPath = %q, want empty on failed persist", record.PersistedPath)
	}
}

func TestCaptureToolResultBudgetResetsPerTurnScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	toolResults := NewToolResultRuntime()
	toolResults.ResetScope("thread-2", "turn-2")

	first := toolResults.Capture(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("a", 40_000))
	second := toolResults.Capture(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("b", 40_000))
	third := toolResults.Capture(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("c", 50_000))
	if first.Truncated || second.Truncated {
		t.Fatalf("unexpected truncation before budget exhaustion: first=%+v second=%+v", first, second)
	}
	if !third.Truncated {
		t.Fatalf("third result = %+v, want budget truncation", third)
	}
	if got := len([]rune(third.Preview)); got != 40_000 {
		t.Fatalf("third preview chars = %d, want 40000 remaining-budget preview", got)
	}
	toolResults.ResetScope("thread-2", "turn-2")
	reset := toolResults.Capture(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("d", 50_000))
	if reset.Truncated {
		t.Fatalf("reset result = %+v, want full preview after scope reset", reset)
	}
}

func TestRepairTruncatedJSON_ObjectTruncatedMidValue(t *testing.T) {
	original := `{"files":{"a.go":{"rows":[[1,2,"hello, world"],[3,4]]}},"total":3}`
	truncated := original[:40]
	result := repairTruncatedJSON(original, truncated)
	if !json.Valid([]byte(result)) {
		t.Fatalf("result is not valid JSON: %q", result)
	}
	if result == truncated {
		t.Fatalf("expected repair to modify truncated string, got same: %q", result)
	}
}

func TestRepairTruncatedJSON_ArrayTruncated(t *testing.T) {
	original := `[{"name":"main","kind":12},{"name":"init","kind":12},{"name":"run","kind":12}]`
	truncated := original[:45]
	result := repairTruncatedJSON(original, truncated)
	if !json.Valid([]byte(result)) {
		t.Fatalf("result is not valid JSON: %q", result)
	}
	if result == truncated {
		t.Fatalf("expected repair to modify truncated string, got same: %q", result)
	}
}

func TestRepairTruncatedJSON_NonJSON(t *testing.T) {
	original := "just plain text output from a command"
	truncated := original[:20]
	result := repairTruncatedJSON(original, truncated)
	if result != truncated {
		t.Fatalf("expected plain text to be returned as-is, got %q", result)
	}
}

func TestRepairTruncatedJSON_TruncatedMidString(t *testing.T) {
	original := `{"count":42,"msg":"hello, world","extra":true}`
	truncated := original[:30] // cuts inside "hello, world" after complete "count":42
	result := repairTruncatedJSON(original, truncated)
	if !json.Valid([]byte(result)) {
		t.Fatalf("result is not valid JSON: %q", result)
	}
	if result == truncated {
		t.Fatalf("expected repair to modify truncated string, got same: %q", result)
	}
}

func TestRepairTruncatedJSON_NoCleanPosition(t *testing.T) {
	original := `{"key":"value"}`
	truncated := `{"`
	result := repairTruncatedJSON(original, truncated)
	if result != truncated {
		t.Fatalf("expected fallback to truncated, got %q", result)
	}
}

func TestCaptureToolResultRepairsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	toolResults := NewToolResultRuntime()
	toolResults.ResetScope("thread-json", "turn-json")

	original := `[` + strings.Repeat(`{"id":1,"name":"test"},`, 5000) + `{"id":5001,"name":"last"}]`
	if !json.Valid([]byte(original)) {
		t.Fatal("test setup: original is not valid JSON")
	}
	record := toolResults.Capture(ToolResultMeta{
		ThreadID: "thread-json",
		TurnID:   "turn-json",
		CallID:   "call-json",
		ToolName: "test",
	}, original)
	if !record.Truncated {
		t.Skip("original not large enough to trigger truncation")
	}
	if !json.Valid([]byte(record.Preview)) {
		t.Fatalf("preview is not valid JSON after repair: %q...", record.Preview[:100])
	}
}
