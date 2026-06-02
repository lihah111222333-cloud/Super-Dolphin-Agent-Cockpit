package observability

import (
	"strings"
	"testing"
)

func TestCodeAnchorConstructorsPreserveStableShape(t *testing.T) {
	anchor := NewCodeAnchor("internal/platform/observability/code_anchor.go", "observability.NewCodeAnchor", 12)
	if anchor.File != "internal/platform/observability/code_anchor.go" {
		t.Fatalf("File = %q", anchor.File)
	}
	if anchor.Function != "observability.NewCodeAnchor" {
		t.Fatalf("Function = %q", anchor.Function)
	}
	if anchor.Line != 12 {
		t.Fatalf("Line = %d", anchor.Line)
	}
}

func TestCodeAnchorFromCallerProvidesFileFunctionAndBestEffortLine(t *testing.T) {
	anchor := anchorTestHelper()
	if !strings.HasSuffix(anchor.File, "code_anchor_test.go") {
		t.Fatalf("File = %q", anchor.File)
	}
	if !strings.Contains(anchor.Function, "anchorTestHelper") {
		t.Fatalf("Function = %q", anchor.Function)
	}
	if anchor.Line <= 0 {
		t.Fatalf("Line = %d, want best-effort positive line", anchor.Line)
	}
}

func anchorTestHelper() CodeAnchor {
	return CodeAnchorFromCaller(0)
}
