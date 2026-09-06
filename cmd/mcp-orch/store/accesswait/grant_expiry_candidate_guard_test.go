package accesswait

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGrantExpiryRunnerCandidateIsNotWired keeps the expiry runner isolated
// until production has an authenticated, durable security-time owner. Tests
// in this package may construct it explicitly; production Fx/unified_v2 code
// must not acquire the candidate by accident.
func TestGrantExpiryRunnerCandidateIsNotWired(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../../../../"))
	candidateDir := filepath.Join(root, "cmd/mcp-orch/store/accesswait")
	var violations []string
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry os.DirEntry, walkErr error) error {
			return inspectGrantExpiryPath(path, entry, walkErr, candidateDir, &violations)
		})
		if err != nil {
			t.Fatalf("scan %s: %v", tree, err)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("grant expiry runner candidate is wired into production: %v", violations)
	}
}

func inspectGrantExpiryPath(path string, entry os.DirEntry, walkErr error, candidateDir string, violations *[]string) error {
	if walkErr != nil {
		return walkErr
	}
	if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || path == filepath.Join(candidateDir, "grant_expiry_runner_candidate.go") {
		return nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(contents), "NewGrantExpiryRunner(") || strings.Contains(string(contents), "GrantExpiryRunner{") {
		*violations = append(*violations, path)
	}
	return nil
}
