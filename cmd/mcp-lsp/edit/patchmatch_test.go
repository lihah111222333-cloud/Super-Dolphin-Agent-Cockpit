package edit

import (
	"errors"
	"strings"
	"testing"
)

func TestMatchContextBeforeAfterDisambiguates(t *testing.T) {
	content := "one\nneedle\nx\ntwo\nneedle\ny\n"
	hunk, err := Parse("@@ \n two\n-needle\n+updated\n y\n")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	matches, err := MatchContext(content, []Hunk{hunk})
	if err != nil {
		t.Fatalf("MatchContext returned error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	if matches[0].ResolvedLSPLine != 5 {
		t.Fatalf("ResolvedLSPLine = %d, want 5", matches[0].ResolvedLSPLine)
	}
}

func TestMatchContextSubstringFallback(t *testing.T) {
	hunk := Hunk{OldText: "value", NewText: "VALUE"}
	content := "prefix value suffix\n"
	matches, err := MatchContext(content, []Hunk{hunk})
	if err != nil {
		t.Fatalf("MatchContext returned error: %v", err)
	}
	if matches[0].MatchedBy != "substring_exact" {
		t.Fatalf("MatchedBy = %q, want substring_exact", matches[0].MatchedBy)
	}
}

func TestMatchContextAmbiguous(t *testing.T) {
	hunk := Hunk{OldText: "value", NewText: "VALUE"}
	content := "value here\nanother value here\n"
	_, err := MatchContext(content, []Hunk{hunk})
	if !errors.Is(err, ErrAmbiguousMatch) {
		t.Fatalf("MatchContext error = %v, want ErrAmbiguousMatch", err)
	}
}

func TestMatchContextPureInsertionBeforeOnly(t *testing.T) {
	content := "package main\n\nfunc main() {\n}\n"
	hunk := Hunk{
		OldText:       "",
		NewText:       "\tfmt.Println(\"hello\")\n",
		BeforeContext: []string{"func main() {"},
	}
	matches, err := MatchContext(content, []Hunk{hunk})
	if err != nil {
		t.Fatalf("MatchContext pure insertion: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	if matches[0].MatchedBy != "pure_insertion" {
		t.Fatalf("MatchedBy = %q, want pure_insertion", matches[0].MatchedBy)
	}
	// Verify insertion point is after "func main() {\n"
	if matches[0].ResolvedStartOffset != matches[0].ResolvedEndOffset {
		t.Fatalf("pure insertion should have start == end offset, got %d != %d",
			matches[0].ResolvedStartOffset, matches[0].ResolvedEndOffset)
	}
}

func TestMatchContextPureInsertionAfterOnly(t *testing.T) {
	content := "package main\n\nimport (\n)\n\nfunc main() {\n}\n"
	hunk := Hunk{
		OldText:      "",
		NewText:      "\t\"fmt\"\n",
		AfterContext: []string{")"},
	}
	matches, err := MatchContext(content, []Hunk{hunk})
	if err != nil {
		t.Fatalf("MatchContext pure insertion after-only: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	// Verify the insertion is before ")"
	result := content[:matches[0].ResolvedStartOffset] + hunk.NewText + content[matches[0].ResolvedEndOffset:]
	if !strings.Contains(result, "import (\n\t\"fmt\"\n)") {
		t.Fatalf("insertion result = %q, want fmt inserted before )", result)
	}
}

func TestMatchContextPureInsertionBeforeAndAfter(t *testing.T) {
	content := "import (\n)\n\nfunc main() {\n}\n"
	hunk := Hunk{
		OldText:       "",
		NewText:       "\t\"fmt\"\n",
		BeforeContext: []string{"import ("},
		AfterContext:  []string{")"},
	}
	matches, err := MatchContext(content, []Hunk{hunk})
	if err != nil {
		t.Fatalf("MatchContext pure insertion before+after: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	result := content[:matches[0].ResolvedStartOffset] + hunk.NewText + content[matches[0].ResolvedEndOffset:]
	if !strings.Contains(result, "import (\n\t\"fmt\"\n)") {
		t.Fatalf("insertion result = %q", result)
	}
}

func TestMatchContextPureInsertionAmbiguous(t *testing.T) {
	// Two identical "func f() {" lines — pure insertion should be ambiguous
	content := "func f() {\n}\nfunc f() {\n}\n"
	hunk := Hunk{
		OldText:       "",
		NewText:       "\t// inserted\n",
		BeforeContext: []string{"func f() {"},
	}
	_, err := MatchContext(content, []Hunk{hunk})
	if !errors.Is(err, ErrAmbiguousMatch) {
		t.Fatalf("MatchContext error = %v, want ErrAmbiguousMatch", err)
	}
}

func TestMatchContextPureInsertionEOF(t *testing.T) {
	content := "package main\n\nfunc main() {\n}\n"
	hunk := Hunk{
		OldText:       "",
		NewText:       "\nfunc helper() {}\n",
		BeforeContext: []string{"}"},
	}
	matches, err := MatchContext(content, []Hunk{hunk})
	if err != nil {
		t.Fatalf("MatchContext pure insertion EOF: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	// EOF insertion: offset should be at end of content
	if matches[0].ResolvedStartOffset != len(content) {
		t.Fatalf("EOF insertion offset = %d, want %d", matches[0].ResolvedStartOffset, len(content))
	}
}
