package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrchestrationNoRPCShellExport 防止 orchestration 子包重新导出旧 RPC 壳构造器。
// cmd/mcp-orch 根装配层只能通过 ProvideRPCFacade 消费 handler bundle，
// 其它子包不应把 orchestration 当作可复用 RPC 协议层。
//
// 任何非测试 Go 文件重新声明 NewOrchestrationHandlers 都会失败，
// 让回归在同一变更中暴露，而不是悄悄滑回协议壳形态。
func TestOrchestrationNoRPCShellExport(t *testing.T) {
	const dir = "../../cmd/mcp-orch/orchestration"

	forbidden := []string{
		"\nfunc NewOrchestrationHandlers(",
		"\ntype NewOrchestrationHandlers ",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, tok := range forbidden {
			if strings.Contains(text, tok) {
				t.Errorf("%s: forbidden export token %q present (P22 P4 §117/§277: handler.Map protocol shell must not return under the old `NewOrchestrationHandlers` name; use ProvideRPCFacade instead)", path, strings.TrimSpace(tok))
			}
		}
	}
}
