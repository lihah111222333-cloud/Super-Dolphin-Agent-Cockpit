package multilsp

import (
	"context"
	"strings"
	"testing"
)

func TestManagerEditPythonFormatterRequiresProductContext(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	_, err := (*managerEdit)(&manager{}).formatPythonDocument(context.Background(), documentRef{absPath: "sample.py"})
	if err == nil || (!strings.Contains(err.Error(), "SUPER_DOLPHIN_HOME") && !strings.Contains(err.Error(), "tool scope CWD")) {
		t.Fatalf("formatPythonDocument error = %v, want explicit product-context failure", err)
	}
}

func TestPythonEndPositionUsesUTF16AndFinalLine(t *testing.T) {
	got := pythonEndPosition("x = '😀'\nreturn\n")
	if got.Line != 2 || got.Character != 0 {
		t.Fatalf("pythonEndPosition trailing newline = %#v", got)
	}
	got = pythonEndPosition("x = '😀'")
	if got.Line != 0 || got.Character != 8 {
		t.Fatalf("pythonEndPosition UTF-16 = %#v", got)
	}
}
