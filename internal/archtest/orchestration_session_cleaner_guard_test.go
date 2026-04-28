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

	var declHits, assertHits []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		if strings.Contains(src, forbiddenDecl) {
			declHits = append(declHits, name)
		}
		if strings.Contains(src, forbiddenAssert) {
			assertHits = append(assertHits, name)
		}
	}

	if len(declHits) > 0 {
		t.Errorf("cmd/mcp-orch/orchestration reintroduced `type generationAwareSessionCleaner interface` (P4 §279 side-channel violation); offending files: %v", declHits)
	}
	if len(assertHits) > 0 {
		t.Errorf("cmd/mcp-orch/orchestration performs a `.(generationAwareSessionCleaner)` type assertion (P4 §279 side-channel violation); offending files: %v", assertHits)
	}
}
