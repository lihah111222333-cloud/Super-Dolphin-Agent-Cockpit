package edit

import (
	"errors"
	"testing"
)

// TestParseWrapsInvalidBodyLineCause verifies malformed body lines keep both parse errors.
func TestParseWrapsInvalidBodyLineCause(t *testing.T) {
	t.Parallel()

	_, err := Parse("@@\n*bad")
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("Parse() error = %v, want ErrInvalidPatch", err)
	}
	if !errors.Is(err, errInvalidPatchBodyLine) {
		t.Fatalf("Parse() error = %v, want errInvalidPatchBodyLine", err)
	}
}
