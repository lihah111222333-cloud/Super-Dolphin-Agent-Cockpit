package archtest

import (
	"strings"
	"testing"
)

// TestRootBridgeAllowlistIntegrity 守住 root bridge allowlist 的结构完整性。
//
// 字段缺失、引用文件被删或 key 重复都会立即失败，确保 allowlist schema
// 自身一直受测试保护，而不是依赖后续 matcher 才暴露结构问题。
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

func TestRootBridgeAllowlistReturnsIndependentSnapshots(t *testing.T) {
	first := rootBridgeAllowlist()
	second := rootBridgeAllowlist()
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("root bridge allowlist snapshot is empty")
	}
	if &first[0] == &second[0] {
		t.Fatal("root bridge allowlist snapshots share backing storage")
	}
	first[0].Reason = "local mutation"
	if second[0].Reason == "local mutation" {
		t.Fatal("root bridge allowlist mutation leaked into another snapshot")
	}
}
