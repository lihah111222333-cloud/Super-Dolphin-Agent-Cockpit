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
