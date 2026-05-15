package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationGenerationAwareSessionCleanerContractGuard is the P22
// P4 S4b side-channel-interface guard. Pre-P4 the orchestration service
// used a local `generationAwareSessionCleaner` interface and
// type-asserted sessionCleaner to it at runtime so RemoveSessionGeneration
// could remain an optional extension. P4 §279 forbids that:
// RemoveSessionGeneration is now part of the owner contract
// contract.OrchestrationSessionCleaner, and the service calls it
// directly (see cmd/mcp-orch/orchestration/process_lifecycle.go
// removeSession).
//
// The guard enforces two invariants by file-text scan:
//  1. cmd/mcp-orch/orchestration does not re-declare `type
//     generationAwareSessionCleaner interface` in any production file.
//  2. cmd/mcp-orch/orchestration does not perform the
//     `.(generationAwareSessionCleaner)` type assertion.
//
// Historical references in comments are allowed (the migration note in
// process_lifecycle.go documents the removal for future readers).
func TestOrchestrationGenerationAwareSessionCleanerContractGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	dir := filepath.Join(root, "cmd", "mcp-orch", "orchestration")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	const forbiddenDecl = "type generationAwareSessionCleaner interface"
	const forbiddenAssert = ".(generationAwareSessionCleaner)"

	declHits, assertHits := collectSessionCleanerContractHits(t, dir, entries, forbiddenDecl, forbiddenAssert)

	if len(declHits) > 0 {
		t.Errorf("cmd/mcp-orch/orchestration reintroduced `type generationAwareSessionCleaner interface` (P4 §279 side-channel violation); offending files: %v", declHits)
	}
	if len(assertHits) > 0 {
		t.Errorf("cmd/mcp-orch/orchestration performs a `.(generationAwareSessionCleaner)` type assertion (P4 §279 side-channel violation); offending files: %v", assertHits)
	}
}

func collectSessionCleanerContractHits(t *testing.T, dir string, entries []os.DirEntry, forbiddenDecl, forbiddenAssert string) ([]string, []string) {
	t.Helper()

	var declHits, assertHits []string
	for _, entry := range entries {
		if !isProductionGoEntry(entry) {
			continue
		}
		name := entry.Name()
		src := readGuardSource(t, filepath.Join(dir, name))
		if strings.Contains(src, forbiddenDecl) {
			declHits = append(declHits, name)
		}
		if strings.Contains(src, forbiddenAssert) {
			assertHits = append(assertHits, name)
		}
	}
	return declHits, assertHits
}

func isProductionGoEntry(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func readGuardSource(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
