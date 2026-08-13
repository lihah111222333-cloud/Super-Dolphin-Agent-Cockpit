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
	context := matches[0].EditContext
	if !strings.Contains(context, "+   6 | func helper() {}") {
		t.Fatalf("EditContext missing helper at EOF insertion line:\n%s", context)
	}
	if strings.Contains(context, "    6 | }") {
		t.Fatalf("EditContext rendered old EOF line after insertion:\n%s", context)
	}
}

func TestMatchContextPureInsertionEditContextKeepsFollowingComment(t *testing.T) {
	content := strings.Join([]string{
		"func TestBefore(t *testing.T) {",
		"\tselect {",
		"\tcase <-time.After(wait):",
		"\t\tt.Fatal(\"timeout\")",
		"\t}",
		"}",
		"",
		"// TestAfter verifies missing handler handling.",
		"func TestAfter(t *testing.T) {",
		"\tcfg := Config{}",
		"}",
		"",
	}, "\n")
	patch := strings.Join([]string{
		" }",
		" ",
		"+// inserted repro marker.",
		" // TestAfter verifies missing handler handling.",
		" func TestAfter(t *testing.T) {",
		"",
	}, "\n")
	hunks, err := ParseMulti(patch)
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	matches, err := MatchContext(content, hunks)
	if err != nil {
		t.Fatalf("MatchContext returned error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	if matches[0].MatchedBy != "pure_insertion" {
		t.Fatalf("MatchedBy = %q, want pure_insertion", matches[0].MatchedBy)
	}
	context := matches[0].EditContext
	if !strings.Contains(context, "+   8 | // inserted repro marker.") {
		t.Fatalf("EditContext missing inserted marker:\n%s", context)
	}
	if !strings.Contains(context, "    9 | // TestAfter verifies missing handler handling.") {
		t.Fatalf("EditContext missing shifted original comment after insertion:\n%s", context)
	}
	if !strings.Contains(context, "   10 | func TestAfter(t *testing.T) {") {
		t.Fatalf("EditContext missing shifted function declaration after insertion:\n%s", context)
	}
}

func TestMatchContextSectionAnchorScopesFollowingHunk(t *testing.T) {
	content := "value\n## Trusted workspace\n  value\n"
	hunks, err := ParseMulti("@@ section\n ## Trusted workspace\n@@ change\n-value\n+updated\n")
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	matches, err := MatchContext(content, hunks)
	if err != nil {
		t.Fatalf("MatchContext returned error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	if !matches[0].SectionAnchor || matches[0].EditContext != "" {
		t.Fatalf("anchor match = %#v, want read-only marker without edit context", matches[0])
	}
	if matches[1].ResolvedLSPLine != 3 {
		t.Fatalf("changed hunk line = %d, want 3", matches[1].ResolvedLSPLine)
	}
}

func TestMatchContextSectionAnchorMustBeUnique(t *testing.T) {
	content := "## section\nvalue\n## section\nvalue\n"
	hunks, err := ParseMulti("@@ section\n ## section\n@@ change\n-value\n+updated\n")
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	_, err = MatchContext(content, hunks)
	if !errors.Is(err, ErrAmbiguousMatch) {
		t.Fatalf("MatchContext error = %v, want ErrAmbiguousMatch", err)
	}
}

func TestMatchContextSectionAnchorMustExist(t *testing.T) {
	hunks, err := ParseMulti("@@ section\n ## missing\n@@ change\n-value\n+updated\n")
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	_, err = MatchContext("## present\nvalue\n", hunks)
	if !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("MatchContext error = %v, want ErrSequenceNotFound", err)
	}
}

func TestMatchContextSectionAnchorRejectsSubstringOnlyMatch(t *testing.T) {
	hunks, err := ParseMulti("@@ section\n ## section\n@@ change\n-value\n+updated\n")
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	_, err = MatchContext("prefix ## section\nvalue\n", hunks)
	if !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("MatchContext error = %v, want ErrSequenceNotFound", err)
	}
}

func TestMatchContextSectionAnchorRejectsTrimmedMatch(t *testing.T) {
	hunks, err := ParseMulti("@@ section\n ## section\n@@ change\n-value\n+updated\n")
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	_, err = MatchContext("  ## section  \nvalue\n", hunks)
	if !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("MatchContext error = %v, want ErrSequenceNotFound", err)
	}
}

func TestMatchContextRejectsMalformedSectionAnchorContract(t *testing.T) {
	hunks := []Hunk{
		{AnchorLines: []string{"## section"}, NewText: "unexpected\n"},
		{OldText: "value\n", NewText: "updated\n"},
	}
	_, err := MatchContext("## section\nvalue\n", hunks)
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("MatchContext error = %v, want ErrInvalidPatch", err)
	}
}

func TestMatchContextRejectsAnchorOnlySequence(t *testing.T) {
	_, err := MatchContext("## section\n", []Hunk{{AnchorLines: []string{"## section"}}})
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("MatchContext error = %v, want ErrInvalidPatch", err)
	}
}

func TestMatchContextSectionAnchorMustBeUniqueAcrossPriorSections(t *testing.T) {
	content := "## shared\nbefore\n## first\nold1\n## shared\nold2\n"
	hunks, err := ParseMulti(strings.Join([]string{
		"@@ first section",
		" ## first",
		"@@ first change",
		"-old1",
		"+new1",
		"@@ shared section",
		" ## shared",
		"@@ second change",
		"-old2",
		"+new2",
		"",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	_, err = MatchContext(content, hunks)
	if !errors.Is(err, ErrAmbiguousMatch) {
		t.Fatalf("MatchContext error = %v, want ErrAmbiguousMatch", err)
	}
}

func TestMatchContextSectionAnchorRejectsBackwardOrder(t *testing.T) {
	content := "## earlier\nold1\n## later\nold2\n"
	hunks, err := ParseMulti(strings.Join([]string{
		"@@ later section",
		" ## later",
		"@@ later change",
		"-old2",
		"+new2",
		"@@ earlier section",
		" ## earlier",
		"@@ earlier change",
		"-old1",
		"+new1",
		"",
	}, "\n"))
	if err != nil {
		t.Fatalf("ParseMulti returned error: %v", err)
	}
	_, err = MatchContext(content, hunks)
	if !errors.Is(err, ErrSequenceNotFound) {
		t.Fatalf("MatchContext error = %v, want ErrSequenceNotFound", err)
	}
}
