package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationNoModuleExport 防止 orchestration 子包重新导出 package-level Module。
// 根入口 cmd/mcp-orch/fx.go 的 buildOrchestrationOptions 才能决定哪些 provider 组合到一起；
// 子包只暴露独立 building blocks，避免重新形成不透明的协议外壳。
//
// 该 guard 扫描 cmd/mcp-orch/orchestration 下所有非测试 Go 文件，发现顶层 `var Module`
// 或 `func Module(` 就失败。注释中的历史说明不会触发，只有实际代码 token 会被拦截。
func TestOrchestrationNoModuleExport(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)

	dir := filepath.Join(root, "cmd", "mcp-orch", "orchestration")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	// 同时禁止 `var Module` 和 `func Module(`，防止把被移除的模块出口换一种写法带回来。
	forbidden := []string{
		"\nvar Module ",
		"\nvar Module=",
		"\nfunc Module(",
	}

	var hits []string
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
		// 文件开头补一个换行，让第一行声明也能命中 "\nvar Module " 模式。
		src := "\n" + string(data)
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, name+" ["+strings.TrimSpace(token)+"]")
			}
		}
	}

	if len(hits) > 0 {
		t.Fatalf("cmd/mcp-orch/orchestration reintroduced a package-level Module export (P4 §278 violation); offending files/tokens: %v", hits)
	}
}
