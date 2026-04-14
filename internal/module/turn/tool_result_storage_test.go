package turn

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCaptureToolResultPersistsLargePayload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ResetToolResultScope("thread-1", "turn-1")

	raw := strings.Repeat("x", toolResultPersistThresholdChars+32)
	record := CaptureToolResult(ToolResultMeta{
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

func TestCaptureToolResultBudgetResetsPerTurnScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ResetToolResultScope("thread-2", "turn-2")

	first := CaptureToolResult(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("a", 40_000))
	second := CaptureToolResult(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("b", 40_000))
	third := CaptureToolResult(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("c", 50_000))
	if first.Truncated || second.Truncated {
		t.Fatalf("unexpected truncation before budget exhaustion: first=%+v second=%+v", first, second)
	}
	if !third.Truncated {
		t.Fatalf("third result = %+v, want budget truncation", third)
	}
	if got := len([]rune(third.Preview)); got != 40_000 {
		t.Fatalf("third preview chars = %d, want 40000 remaining-budget preview", got)
	}
	ResetToolResultScope("thread-2", "turn-2")
	reset := CaptureToolResult(ToolResultMeta{ThreadID: "thread-2", TurnID: "turn-2"}, strings.Repeat("d", 50_000))
	if reset.Truncated {
		t.Fatalf("reset result = %+v, want full preview after scope reset", reset)
	}
}
