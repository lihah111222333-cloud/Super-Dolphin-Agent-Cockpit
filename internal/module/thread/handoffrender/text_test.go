package handoffrender

import "testing"

func TestThreadTextRowAccessorsTrimValues(t *testing.T) {
	t.Parallel()

	row := &ThreadTextRow{
		Status:   " created ",
		ThreadID: " thread-1 ",
		CWD:      " /repo/a ",
	}

	if got := ThreadStatus(row); got != "created" {
		t.Fatalf("ThreadStatus() = %q, want created", got)
	}
	if got := ThreadID(row); got != "thread-1" {
		t.Fatalf("ThreadID() = %q, want thread-1", got)
	}
	if got := ThreadCWD(row); got != "/repo/a" {
		t.Fatalf("ThreadCWD() = %q, want /repo/a", got)
	}
}

func TestThreadTextRowAccessorsAcceptNil(t *testing.T) {
	t.Parallel()

	if ThreadStatus(nil) != "" || ThreadID(nil) != "" || ThreadCWD(nil) != "" {
		t.Fatal("nil row accessors must return empty strings")
	}
}

func TestNormalizeAndTruncateText(t *testing.T) {
	t.Parallel()

	if got := NormalizeText(" hello\r\n   世界\tagain "); got != "hello 世界 again" {
		t.Fatalf("NormalizeText() = %q, want collapsed text", got)
	}
	if got := TruncateText("你好世界", 2); got != "你好…" {
		t.Fatalf("TruncateText() = %q, want rune-safe truncation", got)
	}
}
