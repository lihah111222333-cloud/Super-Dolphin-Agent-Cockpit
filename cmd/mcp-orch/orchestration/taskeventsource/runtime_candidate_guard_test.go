package taskeventsource

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRuntimeEventCandidateIsNotReferencedByProductionFiles is a local
// architecture guard. The candidate may be exercised by its own tests, but no
// non-test production file may construct or enable it before an atomic owner is
// available.
func TestRuntimeEventCandidateIsNotReferencedByProductionFiles(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../../../../"))
	candidateDir := filepath.Join(root, "cmd/mcp-orch/orchestration/taskeventsource")
	var violations []string
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry os.DirEntry, walkErr error) error {
			violation, err := candidateProductionFileViolation(path, entry, walkErr, candidateDir)
			if err != nil {
				return err
			}
			if violation {
				violations = append(violations, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", tree, err)
		}
	}
	if len(violations) != 0 {
		t.Fatalf("runtime event candidate is referenced by production files: %v", violations)
	}
}

func candidateProductionFileViolation(path string, entry os.DirEntry, walkErr error, candidateDir string) (bool, error) {
	if walkErr != nil {
		return false, walkErr
	}
	if !isCandidateProductionGoFile(path, entry, candidateDir) {
		return false, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(contents)
	return candidateTextContainsAdapter(text), nil
}

func isCandidateProductionGoFile(path string, entry os.DirEntry, candidateDir string) bool {
	if entry.IsDir() {
		return false
	}
	if filepath.Ext(path) != ".go" {
		return false
	}
	if strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !strings.HasPrefix(path, candidateDir+string(filepath.Separator))
}

func candidateTextContainsAdapter(text string) bool {
	if strings.Contains(text, "NewRuntimeEventCandidate(") {
		return true
	}
	return strings.Contains(text, "RuntimeEventCandidate{")
}
