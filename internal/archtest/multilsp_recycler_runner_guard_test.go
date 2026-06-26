package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLSPPoolRecyclerOwnedByRunner 守住 LSP 回收器的运行时归属边界：
// NewManagerPool 不能在构造函数里启动回收循环，poolRecycler 也不能暴露临时
// Start/Stop 生命周期或自行启动 goroutine。运行时所有者必须是通过 `group:"runners"`
// 注册的 platformrunner.Runner，因此唯一入口是受 ctx 控制的 Run(ctx)。
//
// 该守卫扫描 cmd/mcp-lsp/multilsp 下的非测试 Go 文件；一旦自启动循环或
// Start/Stop 入口重新出现，测试会直接失败，避免生命周期绕过 runner 管理。
func TestMultiLSPPoolRecyclerOwnedByRunner(t *testing.T) {
	const dir = "../../cmd/mcp-lsp/multilsp"

	forbidden := []string{
		"pool.recycler.Start(",
		"p.recycler.Start(",
		"p.recycler.Stop(",
		"go r.loop()",
		"func (r *poolRecycler) Start(",
		"func (r *poolRecycler) Stop(",
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
				t.Errorf("%s: forbidden recycler lifecycle literal %q present (P22 P2 LSP-S1: use Run(ctx) as Runner owner, not self-launched goroutine / Start()/Stop() pair)", path, tok)
			}
		}
	}
}
