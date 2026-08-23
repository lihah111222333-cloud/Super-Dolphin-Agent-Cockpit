package sqlcconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyGeneratedOutputsRejectsMissingRegisteredManualExtension(t *testing.T) {
	probe := newConfigFixture(t, "internal/store/sqlc", "store/sqlc")
	units, err := LoadDefault(probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range ManualExtensionPaths(units) {
		t.Run(strings.ReplaceAll(missing, "/", "_"), func(t *testing.T) {
			root := newConfigFixture(t, "internal/store/sqlc", "store/sqlc")
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(missing))); err != nil {
				t.Fatal(err)
			}
			if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "required manual extension") {
				t.Fatalf("VerifyGeneratedOutputs() error = %v, want missing required manual extension", err)
			}
		})
	}
}

func TestVerifyGeneratedOutputsRejectsForgedRegisteredManualExtensionMarker(t *testing.T) {
	root := newConfigFixture(t, "internal/store/sqlc", "store/sqlc")
	units, err := LoadDefault(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range ManualExtensionPaths(units) {
		t.Run(strings.ReplaceAll(relative, "/", "_"), func(t *testing.T) {
			forged := filepath.Join(root, filepath.FromSlash(relative))
			writeFixtureFile(t, forged, GeneratedGoMarker+"\npackage sqlc\n")
			if err := VerifyGeneratedOutputs(root); err == nil || !strings.Contains(err.Error(), "non-canonical marker") {
				t.Fatalf("VerifyGeneratedOutputs() error = %v, want non-canonical manual marker", err)
			}
			writeRequiredManualExtensions(t, root, units)
		})
	}
}

func TestValidateTrackedGitIndexFlagsOnlyAcceptsCanonicalRecord(t *testing.T) {
	relative := "internal/store/sqlc/generated.go"
	for name, flags := range map[string]string{
		"modified":         "M " + relative + "\n",
		"removed":          "R " + relative + "\n",
		"conflicted":       "C " + relative + "\n",
		"skip-worktree":    "S " + relative + "\n",
		"assume-unchanged": "h " + relative + "\n",
		"missing":          "",
		"duplicate":        "H " + relative + "\nH " + relative + "\n",
		"wrong-path":       "H internal/store/sqlc/other.go\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateTrackedGitIndexFlags([]byte(flags), relative); err == nil {
				t.Fatalf("validateTrackedGitIndexFlags(%q) returned nil", flags)
			}
		})
	}
	if err := validateTrackedGitIndexFlags([]byte("H "+relative+"\n"), relative); err != nil {
		t.Fatalf("validateTrackedGitIndexFlags(canonical H) error = %v", err)
	}
}
