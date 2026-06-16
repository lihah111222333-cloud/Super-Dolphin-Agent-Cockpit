package handoffrender

import (
	"testing"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// TestThreadFieldHelpersTrimValuesAndHandleNil verifies handoff row helpers are nil-safe.
func TestThreadFieldHelpersTrimValuesAndHandleNil(t *testing.T) {
	t.Parallel()

	if ThreadStatus(nil) != "" || ThreadID(nil) != "" || ThreadCWD(nil) != "" {
		t.Fatal("thread field helpers should return empty strings for nil rows")
	}

	row := &threadstore.Thread{ThreadID: " t-1 ", Cwd: " /repo ", Status: " running "}
	if got := ThreadID(row); got != "t-1" {
		t.Fatalf("ThreadID() = %q, want t-1", got)
	}
	if got := ThreadCWD(row); got != "/repo" {
		t.Fatalf("ThreadCWD() = %q, want /repo", got)
	}
	if got := ThreadStatus(row); got != "running" {
		t.Fatalf("ThreadStatus() = %q, want running", got)
	}
}

// TestNormalizeTextCollapsesWhitespace verifies handoff snippets render as single-line text.
func TestNormalizeTextCollapsesWhitespace(t *testing.T) {
	t.Parallel()

	got := NormalizeText("  first line\r\n\tsecond   line  ")
	if got != "first line second line" {
		t.Fatalf("NormalizeText() = %q, want collapsed text", got)
	}
}

// TestTruncateTextUsesRuneLimit verifies handoff snippets truncate by rune count.
func TestTruncateTextUsesRuneLimit(t *testing.T) {
	t.Parallel()

	if got := TruncateText("abcdef", 4); got != "abcd\u2026" {
		t.Fatalf("TruncateText() = %q, want abcd ellipsis", got)
	}
	if got := TruncateText("你好世界", 2); got != "你好\u2026" {
		t.Fatalf("TruncateText() = %q, want two-rune prefix", got)
	}
	if got := TruncateText("go", 4); got != "go" {
		t.Fatalf("TruncateText() = %q, want original text", got)
	}
	if got := TruncateText("go", 0); got != "" {
		t.Fatalf("TruncateText() = %q, want empty text for non-positive limit", got)
	}
}
