package archtest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecoveryCandidateRemainsCapabilityOnly prevents the candidate from
// regaining product marker SQL, runtime DDL, Fx wiring, or provider imports.
func TestRecoveryCandidateRemainsCapabilityOnly(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "mcp-orch", "store", "waitoperation", "recoverycandidate")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		source := string(data)
		for _, forbidden := range []string{"go.uber.org/fx", "/provider/", "UPDATE orchestration_wait_operation_commit_markers", "INSERT INTO orchestration_wait_operation_commit_markers", "CREATE TABLE IF NOT EXISTS"} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s contains forbidden capability bypass %q", path, forbidden)
			}
		}
	}
}
