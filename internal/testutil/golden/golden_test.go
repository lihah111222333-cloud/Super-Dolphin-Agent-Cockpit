package golden

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewTestOwnerRejectsNilUpdateFlag(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewTestOwner(nil) must fail fast")
		}
	}()

	NewTestOwner(nil)
}

func TestAssertJSONExplicitOwnerWritesAndReadsGolden(t *testing.T) {
	update := true
	tc := Case{
		BaseDir: t.TempDir(),
		Domain:  DomainIntegration,
		Name:    "explicit_owner",
	}
	owner := NewTestOwner(&update)

	AssertJSON(t, owner, tc, map[string]any{"value": "updated"})
	path, err := tc.path()
	if err != nil {
		t.Fatalf("golden path: %v", err)
	}
	if _, err := os.Stat(filepath.Clean(path)); err != nil {
		t.Fatalf("updated golden file: %v", err)
	}

	update = false
	AssertJSON(t, owner, tc, map[string]any{"value": "updated"})
}
