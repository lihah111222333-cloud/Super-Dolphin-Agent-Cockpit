package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThreadTurnPendingLaunchSpawnerContractGuard is the P22 P4 S2
// side-channel-interface guard: the PendingLaunchSpawner contract must
// live in internal/contract (owner-neutral), not in the turn consumer
// package. Pre-P4 the interface was exported as
// `turn.PendingLaunchSpawner` and the thread owning module had to
// import turn just to declare `var _ turn.PendingLaunchSpawner =
// (Service)(nil)` — a classic side-channel hidden contract (§2.5, §281).
//
// The guard enforces two invariants by file-text scan:
//  1. internal/module/turn does not re-declare `type
//     PendingLaunchSpawner interface`. Name-squatting the interface in
//     the consumer package is exactly what S2 removed.
//  2. internal/module/thread/module.go references the contract version
//     (`contract.PendingLaunchSpawner`) and not `turn.PendingLaunchSpawner`,
//     so the thread→turn import cycle stays broken.
//
// Test name matches P4 §TDD line 257: TestThreadTurnPendingLaunchSpawnerContractGuard.
func TestThreadTurnPendingLaunchSpawnerContractGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	// 1. turn must not re-export PendingLaunchSpawner as an interface
	//    type. Comments that mention the old name are fine — the token
	//    `type PendingLaunchSpawner interface` is the real violation.
	turnDir := filepath.Join(root, "internal", "module", "turn")
	entries, err := os.ReadDir(turnDir)
	if err != nil {
		t.Fatalf("read %s: %v", turnDir, err)
	}
	const forbiddenDecl = "type PendingLaunchSpawner interface"
	var declHits []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(turnDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), forbiddenDecl) {
			declHits = append(declHits, name)
		}
	}
	if len(declHits) > 0 {
		t.Fatalf("internal/module/turn reintroduced the PendingLaunchSpawner interface (P4 §2.5 side-channel violation); offending files: %v", declHits)
	}

	// 2. thread/module.go must reference contract.PendingLaunchSpawner
	//    (the post-S2 home) and must NOT reference
	//    turn.PendingLaunchSpawner in code positions. We allow comment
	//    mentions of the old name (historical context in the P22 P4 S2
	//    migration notes), so the check is token-shaped rather than
	//    substring-wide.
	threadModule := filepath.Join(root, "internal", "module", "thread", "module.go")
	data, err := os.ReadFile(threadModule)
	if err != nil {
		t.Fatalf("read %s: %v", threadModule, err)
	}
	src := string(data)
	if !strings.Contains(src, "contract.PendingLaunchSpawner") {
		t.Errorf("thread/module.go must reference contract.PendingLaunchSpawner after P4 S2")
	}
	// Forbid active references to the old symbol in non-comment contexts.
	// var _ turn.PendingLaunchSpawner = ... and fx.As(new(turn.PendingLaunchSpawner))
	// are the two pre-S2 shapes. Both begin at column 0-ish and use `turn.` as a real
	// package qualifier — so we match those precise tokens.
	forbiddenRefs := []string{
		"var _ turn.PendingLaunchSpawner",
		"fx.As(new(turn.PendingLaunchSpawner))",
	}
	for _, token := range forbiddenRefs {
		if strings.Contains(src, token) {
			t.Errorf("thread/module.go reintroduced active reference to turn.PendingLaunchSpawner: %q", token)
		}
	}
}
