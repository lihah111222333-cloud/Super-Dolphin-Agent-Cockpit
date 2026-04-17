package edit

import (
	"errors"
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
