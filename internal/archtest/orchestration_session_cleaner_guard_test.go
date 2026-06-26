package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这个测试守护 session 清理接口归属。
// RemoveSessionGeneration 已经是 owner contract 的一部分，orchestration 生产代码不能再用本地接口加类型断言把它变成可选能力。
//
// 这个 guard 用文本扫描锁定两个禁区：
//  1. 生产文件不能重新声明 generationAwareSessionCleaner 接口。
//  2. 生产文件不能重新引入 generationAwareSessionCleaner 类型断言。
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
