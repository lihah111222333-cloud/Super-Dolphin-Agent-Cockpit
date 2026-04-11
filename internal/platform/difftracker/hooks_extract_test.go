package difftracker

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractPatch_ValidReplaceRange(t *testing.T) {
	patch, err := ExtractPatch(`{"success":true,"action":"replace_range","replaced":"old\n","replacement":"new\n"}`, "main.go")
	if err != nil {
		t.Fatalf("ExtractPatch() error = %v", err)
	}
	for _, want := range []string{"--- a/main.go", "+++ b/main.go", "-old", "+new"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("patch missing %q: %q", want, patch)
		}
	}
}

func TestExtractPatch_InvalidFormat(t *testing.T) {
	if _, err := ExtractPatch(`{"success":true,"action":"rename"}`, "main.go"); err == nil {
		t.Fatal("ExtractPatch() error = nil, want non-nil")
	}
}

func TestExtractPatch_EmptyContent(t *testing.T) {
	if _, err := ExtractPatch("", "main.go"); err == nil {
		t.Fatal("ExtractPatch() error = nil, want non-nil")
	}
}

func TestExtractPatchFromReplaceRange_RejectsHeaderOnlyInsert(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"text":"--- /dev/null\n+++ b/main.go\n+new\n"}]}`)
	if _, _, err := ExtractPatchFromReplaceRange(raw); err == nil {
		t.Fatal("ExtractPatchFromReplaceRange() error = nil, want non-nil")
	}
}
