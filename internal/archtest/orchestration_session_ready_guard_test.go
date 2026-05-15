package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationSessionReadyWaiterContractGuard is the P22 P4 S4a
// side-channel-interface guard for cmd/mcp-orch/orchestration. Pre-P4
// the orchestration service used a local `sessionReadyWaiter` interface
// and type-asserted turnStarter to it at runtime, letting the "is the
// session ready to accept a turn?" semantics remain a private extension
// of whatever concrete implementation happened to be wired. P4 §279
// forbids that: WaitForSessionReady is now part of the owner contract
// contract.OrchestrationTurnStarter, and the service calls it directly.
//
// The guard enforces two invariants by file-text scan so the side-channel
// cannot silently reappear:
//  1. cmd/mcp-orch/orchestration does not re-declare `type
//     sessionReadyWaiter interface`. Historical comments that mention
//     the old name are permitted (the migration note in helpers.go).
//  2. cmd/mcp-orch/orchestration/helpers.go does not perform the
//     `.(sessionReadyWaiter)` type assertion.
func TestOrchestrationSessionReadyWaiterContractGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	dir := filepath.Join(root, "cmd", "mcp-orch", "orchestration")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	const forbiddenDecl = "type sessionReadyWaiter interface"
	const forbiddenAssert = ".(sessionReadyWaiter)"

	declHits, assertHits := orchestrationSessionReadyGuardHits(t, entries, dir, forbiddenDecl, forbiddenAssert)
	if len(declHits) > 0 {
		t.Errorf("cmd/mcp-orch/orchestration reintroduced `type sessionReadyWaiter interface` (P4 §279 side-channel violation); offending files: %v", declHits)
	}
	if len(assertHits) > 0 {
		t.Errorf("cmd/mcp-orch/orchestration performs a `.(sessionReadyWaiter)` type assertion (P4 §279 side-channel violation); offending files: %v", assertHits)
	}
}

func orchestrationSessionReadyGuardHits(t *testing.T, entries []os.DirEntry, dir, forbiddenDecl, forbiddenAssert string) ([]string, []string) {
	t.Helper()
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
	return declHits, assertHits
}
