package archtest

import (
	"strings"
	"testing"
)

// TestRootBridgeAllowlistIntegrity guards the P22 semantic root-bridge
// allowlist declared in root_bridge_allowlist.go.
//
// Unlike the matcher-level P0 skeletons in fx_invoke_guard_test.go and
// friends, this integrity check is *not* parked behind t.Skip — if the
// allowlist loses a required field, references a deleted file, or grows a
// duplicate key, the suite fails immediately. That is what P0 §TDD 与清理要求
// means by "先落 semantic allowlist schema": the schema itself is always
// verified, while the per-slice matcher subtests are filled in by downstream
// PRs.
func TestRootBridgeAllowlistIntegrity(t *testing.T) {
	t.Parallel()

	repoRoot := repoRootForGuardTests(t)
	problems := rootBridgeAllowlistIntegrityViolations(repoRoot)
	if len(problems) == 0 {
		return
	}

	t.Fatalf("root-bridge allowlist integrity violations (%d):\n  %s",
		len(problems), strings.Join(problems, "\n  "))
}
