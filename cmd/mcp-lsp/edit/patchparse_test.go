package edit

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseImplicitSingleHunk(t *testing.T) {
	hunk, err := Parse("-old\n+new\n")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if hunk.OldText != "old\n" {
		t.Fatalf("OldText = %q, want %q", hunk.OldText, "old\n")
	}
	if hunk.NewText != "new\n" {
		t.Fatalf("NewText = %q, want %q", hunk.NewText, "new\n")
	}
}

func TestParseExplicitBlankHeaderWithContext(t *testing.T) {
	patch := "@@ \n before\n-old\n+new\n after\n"
	hunk, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !reflect.DeepEqual(hunk.BeforeContext, []string{"before"}) {
		t.Fatalf("BeforeContext = %#v", hunk.BeforeContext)
	}
	if !reflect.DeepEqual(hunk.AfterContext, []string{"after"}) {
		t.Fatalf("AfterContext = %#v", hunk.AfterContext)
	}
}

func TestParseAcceptsLenientBareHeader(t *testing.T) {
	hunk, err := Parse("@@\n-old\n+new\n")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if hunk.OldText != "old\n" {
		t.Fatalf("OldText = %q, want %q", hunk.OldText, "old\n")
	}
	if hunk.NewText != "new\n" {
		t.Fatalf("NewText = %q, want %q", hunk.NewText, "new\n")
	}
}

func TestParseAcceptsLenientHeaderWithoutSpace(t *testing.T) {
	tests := []string{
		"@@func main\n-old\n+new\n",
		"@@\"name\": \"value\"\n-old\n+new\n",
	}
	for _, patch := range tests {
		t.Run(patch, func(t *testing.T) {
			hunk, err := Parse(patch)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if hunk.OldText != "old\n" || hunk.NewText != "new\n" {
				t.Fatalf("hunk = %#v", hunk)
			}
		})
	}
}

func TestParseRejectsInsertionOnly(t *testing.T) {
	// Pure insertion without any context should still be rejected
	_, err := Parse("+new\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("Parse error = %v, want ErrInvalidPatch", err)
	}
}

func TestParseAllowsPureInsertionHunk(t *testing.T) {
	// Pure insertion with before context should be allowed
	patch := " func main() {\n+\tfmt.Println(\"hello\")\n"
	hunk, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse pure insertion: %v", err)
	}
	if hunk.OldText != "" {
		t.Fatalf("OldText = %q, want empty for pure insertion", hunk.OldText)
	}
	if !strings.Contains(hunk.NewText, "fmt.Println") {
		t.Fatalf("NewText = %q, want containing fmt.Println", hunk.NewText)
	}
	if len(hunk.BeforeContext) == 0 || hunk.BeforeContext[0] != "func main() {" {
		t.Fatalf("BeforeContext = %v, want [\"func main() {\"]", hunk.BeforeContext)
	}
}

func TestParseAllowsPureInsertionWithAfterContext(t *testing.T) {
	patch := " import (\n+\t\"fmt\"\n )\n"
	hunk, err := Parse(patch)
	if err != nil {
		t.Fatalf("Parse pure insertion with after context: %v", err)
	}
	if hunk.OldText != "" {
		t.Fatalf("OldText = %q, want empty", hunk.OldText)
	}
	if hunk.NewText != "\t\"fmt\"\n" {
		t.Fatalf("NewText = %q", hunk.NewText)
	}
}

func TestParseRejectsPureInsertionWithoutContext(t *testing.T) {
	// Pure insertion without any context line should be rejected
	patch := "+line1\n+line2\n"
	_, err := Parse(patch)
	if err == nil {
		t.Fatal("expected error for pure insertion without context")
	}
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("Parse error = %v, want ErrInvalidPatch", err)
	}
}

func TestParseMultiPreservesOrder(t *testing.T) {
	patch := "@@ first\n-old1\n+new1\n@@ second\n-old2\n+new2\n"
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("len(hunks) = %d, want 2", len(hunks))
	}
	if hunks[0].OldText != "old1\n" || hunks[1].OldText != "old2\n" {
		t.Fatalf("hunk order not preserved: %#v", hunks)
	}
}

func TestParseMultiAcceptsLenientHeaders(t *testing.T) {
	patch := "@@\n-old1\n+new1\n@@second\n-old2\n+new2\n"
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("len(hunks) = %d, want 2", len(hunks))
	}
	if hunks[0].OldText != "old1\n" || hunks[1].OldText != "old2\n" {
		t.Fatalf("hunk order not preserved: %#v", hunks)
	}
}

func TestParseMultiAcceptsUnifiedDiffFileHeaders(t *testing.T) {
	patch := "--- a/file.go\n+++ b/file.go\n@@ -1,2 +1,2 @@\n-old\n+new\n context\n"
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("len(hunks) = %d, want 1", len(hunks))
	}
	if hunks[0].OldText != "old\n" {
		t.Fatalf("OldText = %q, want old text", hunks[0].OldText)
	}
	if hunks[0].NewText != "new\n" {
		t.Fatalf("NewText = %q, want new text", hunks[0].NewText)
	}
	if !reflect.DeepEqual(hunks[0].AfterContext, []string{"context"}) {
		t.Fatalf("AfterContext = %#v, want context", hunks[0].AfterContext)
	}
}

func TestParseMultiAcceptsApplyPatchUpdateFileWrapper(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: file.go\n@@\n-old\n+new\n context\n*** End Patch\n"
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("len(hunks) = %d, want 1", len(hunks))
	}
	if hunks[0].OldText != "old\n" {
		t.Fatalf("OldText = %q, want old text", hunks[0].OldText)
	}
	if hunks[0].NewText != "new\n" {
		t.Fatalf("NewText = %q, want new text", hunks[0].NewText)
	}
	if !reflect.DeepEqual(hunks[0].AfterContext, []string{"context"}) {
		t.Fatalf("AfterContext = %#v, want context", hunks[0].AfterContext)
	}
}

func TestParseMultiAcceptsLeadingImplicitHunk(t *testing.T) {
	patch := " import (\n+\t\"slices\"\n )\n@@ second\n-old2\n+new2\n"
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("len(hunks) = %d, want 2", len(hunks))
	}
	if hunks[0].OldText != "" || hunks[0].NewText != "\t\"slices\"\n" {
		t.Fatalf("first hunk = %#v", hunks[0])
	}
	if !reflect.DeepEqual(hunks[0].BeforeContext, []string{"import ("}) {
		t.Fatalf("first BeforeContext = %#v, want import context", hunks[0].BeforeContext)
	}
	if !reflect.DeepEqual(hunks[0].AfterContext, []string{")"}) {
		t.Fatalf("first AfterContext = %#v, want closing import context", hunks[0].AfterContext)
	}
	if hunks[1].OldText != "old2\n" || hunks[1].NewText != "new2\n" {
		t.Fatalf("second hunk = %#v", hunks[1])
	}
}

func TestParseMultiAcceptsLenientHeaderAfterImplicitHunk(t *testing.T) {
	hunks, err := ParseMulti("-old\n+new\n@@second\n-old2\n+new2\n")
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("len(hunks) = %d, want 2", len(hunks))
	}
	if hunks[0].OldText != "old\n" || hunks[1].OldText != "old2\n" {
		t.Fatalf("hunks = %#v", hunks)
	}
}

func TestParseMultiAcceptsContextOnlySectionAnchor(t *testing.T) {
	patch := strings.Join([]string{
		" first block",
		"-old1",
		"+new1",
		"@@ section anchor",
		" ## Trusted workspace",
		"@@ second change",
		"-old2",
		"+new2",
		"",
	}, "\n")
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	if len(hunks) != 3 {
		t.Fatalf("len(hunks) = %d, want 3", len(hunks))
	}
	if !hunks[1].IsSectionAnchor() {
		t.Fatalf("middle hunk = %#v, want section anchor", hunks[1])
	}
	if !reflect.DeepEqual(hunks[1].AnchorLines, []string{"## Trusted workspace"}) || hunks[1].OldText != "" || hunks[1].NewText != "" {
		t.Fatalf("section anchor = %#v", hunks[1])
	}
}

func TestParseRejectsContextOnlySingleHunk(t *testing.T) {
	_, err := Parse("@@ section anchor\n ## Trusted workspace\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("Parse error = %v, want ErrInvalidPatch", err)
	}
}

func TestParseMultiRejectsTrailingSectionAnchor(t *testing.T) {
	_, err := ParseMulti("@@ change\n-old\n+new\n@@ trailing\n ## trailing context\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("ParseMulti error = %v, want ErrInvalidPatch", err)
	}
}

func TestParseMultiRejectsBlankSectionAnchor(t *testing.T) {
	_, err := ParseMulti("@@ blank anchor\n \n@@ change\n-old\n+new\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("ParseMulti error = %v, want ErrInvalidPatch", err)
	}
}

func TestParseMultiRejectsImplicitContextOnlySectionAnchor(t *testing.T) {
	_, err := ParseMulti(" section anchor\n@@ change\n-old\n+new\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("ParseMulti error = %v, want ErrInvalidPatch", err)
	}
}
