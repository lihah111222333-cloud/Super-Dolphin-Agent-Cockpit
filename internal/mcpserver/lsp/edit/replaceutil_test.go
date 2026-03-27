package edit

import (
	"errors"
	"strings"
	"testing"
)

func TestOffsetToLine(t *testing.T) {
	t.Run("normal line mapping", func(t *testing.T) {
		content := "alpha\nbeta\ngamma\n"
		line, err := OffsetToLine(content, 6)
		if err != nil {
			t.Fatalf("OffsetToLine returned error: %v", err)
		}
		if line != 2 {
			t.Fatalf("line = %d, want 2", line)
		}
	})

	t.Run("empty content", func(t *testing.T) {
		line, err := OffsetToLine("", 0)
		if err != nil {
			t.Fatalf("OffsetToLine returned error: %v", err)
		}
		if line != 1 {
			t.Fatalf("line = %d, want 1", line)
		}
	})

	t.Run("no trailing newline", func(t *testing.T) {
		content := "alpha\nbeta"
		line, err := OffsetToLine(content, len(content))
		if err != nil {
			t.Fatalf("OffsetToLine returned error: %v", err)
		}
		if line != 2 {
			t.Fatalf("line = %d, want 2", line)
		}
	})
}

func TestReplacementPreview(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	start := strings.Index(content, "beta")
	end := start + len("beta")

	preview, err := ReplacementPreview(content, start, end, "BETA")
	if err != nil {
		t.Fatalf("ReplacementPreview returned error: %v", err)
	}
	if !strings.Contains(preview, "   2 | BETA") {
		t.Fatalf("preview = %q, want line for replacement", preview)
	}
	if strings.Contains(preview, "beta") {
		t.Fatalf("preview = %q, unexpectedly contains old text", preview)
	}
}

func TestBuildEditContext(t *testing.T) {
	lines := []string{"l1", "l2", "l3", "l4", "l5", "target", "l7", "l8", "l9", "l10", "l11", "l12", "l13", ""}
	content := strings.Join(lines, "\n")
	start := strings.Index(content, "target")
	end := start + len("target")

	context, previewStart, previewEnd, err := BuildEditContext(content, start, end, "updated")
	if err != nil {
		t.Fatalf("BuildEditContext returned error: %v", err)
	}
	if previewStart != 1 || previewEnd != 11 {
		t.Fatalf("preview range = %d-%d, want 1-11", previewStart, previewEnd)
	}
	if !strings.Contains(context, "-   6 | target") {
		t.Fatalf("context = %q, missing removed line", context)
	}
	if !strings.Contains(context, "+   6 | updated") {
		t.Fatalf("context = %q, missing added line", context)
	}
	if strings.Contains(context, "  12 | l12") || strings.Contains(context, "  13 | l13") {
		t.Fatalf("context = %q, expected context window clipping", context)
	}
}

func TestGuardContentAndReplacement(t *testing.T) {
	if err := GuardContentAndReplacement("ok", "fine"); err != nil {
		t.Fatalf("GuardContentAndReplacement returned error: %v", err)
	}

	tooLargeReplacement := strings.Repeat("x", ReplaceRangeMaxReplacementBytes+1)
	if err := GuardContentAndReplacement("ok", tooLargeReplacement); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("replacement error = %v, want ErrInvalidPatch", err)
	}

	tooLargeContent := strings.Repeat("y", ReplaceRangeMaxContentBytes+1)
	if err := GuardContentAndReplacement(tooLargeContent, "ok"); !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("content error = %v, want ErrInvalidPatch", err)
	}
}

func TestShouldForceBypass(t *testing.T) {
	if ShouldForceBypass(ReplaceRangeForceBypassMaxBytes) {
		t.Fatal("ShouldForceBypass returned true at boundary")
	}
	if !ShouldForceBypass(ReplaceRangeForceBypassMaxBytes + 1) {
		t.Fatal("ShouldForceBypass returned false above boundary")
	}
}
