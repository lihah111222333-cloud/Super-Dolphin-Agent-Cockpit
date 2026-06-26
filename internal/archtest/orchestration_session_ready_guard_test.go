package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationSessionReadyWaiterContractGuard 锁定 orchestration 等待 session ready 的公开边界。
// 服务层必须通过 contract.OrchestrationTurnStarter.WaitForSessionReady 调用 owner contract，
// 不能在运行时用私有 sessionReadyWaiter 侧向接口补能力，否则具体实现会绕过编译期约束。
//
// 文件文本扫描同时校验两件事：不得重新声明本地 sessionReadyWaiter 接口，
// helpers.go 也不得再对 turnStarter 做 `.(sessionReadyWaiter)` 类型断言。
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
