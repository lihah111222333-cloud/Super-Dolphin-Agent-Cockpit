package edit

import (
	"errors"
	"reflect"
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
	_, err := Parse("+new\n")
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

func TestParseMultiRejectsLeadingImplicitHunk(t *testing.T) {
	_, err := ParseMulti("-old\n+new\n@@ second\n-old2\n+new2\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("ParseMulti error = %v, want ErrInvalidPatch", err)
	}
}

func TestParseMultiRejectsLenientHeaderAfterImplicitHunk(t *testing.T) {
	_, err := ParseMulti("-old\n+new\n@@second\n-old2\n+new2\n")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("ParseMulti error = %v, want ErrInvalidPatch", err)
	}
}
