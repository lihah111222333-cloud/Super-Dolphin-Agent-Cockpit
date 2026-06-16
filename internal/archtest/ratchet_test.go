package archtest

import (
	"path/filepath"
	"testing"
)

// TestFreezeBaselineReturnsCollectError verifies library callers receive scan errors.
func TestFreezeBaselineReturnsCollectError(t *testing.T) {
	t.Parallel()

	_, err := FreezeBaseline(CheckOptions{RepoRoot: filepath.Join(t.TempDir(), "missing")})
	if err == nil {
		t.Fatal("FreezeBaseline() error = nil, want missing root error")
	}
}
