package archtest_test

import (
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func TestCodeSizeGuard(t *testing.T) {
	opts := archtest.CheckOptions{
		RepoRoot:  repoRoot(t),
		ScanRoots: []string{"internal", "cmd", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	}
	// 守卫运行时自动收缩 / 删除已回落到默认预算的 freeze 条目（同步回写 freeze_registry.go）。
	// 语义与 “only-shrink” 基准一致：用余量立即落盘，避免过期值垃圾积累。
	fixes, err := archtest.AutoRepairFreezeRegistry(opts)
	if err != nil {
		t.Fatalf("freeze registry autofix failed: %v", err)
	}
	for _, f := range fixes {
		t.Logf("freeze registry auto-repaired: %s", f.String())
	}
	violations := archtest.CheckAll(opts)
	if len(violations) == 0 {
		return
	}
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	t.Fatalf("code size guard violations (%d):\n%s", len(violations), strings.Join(lines, "\n"))
}
