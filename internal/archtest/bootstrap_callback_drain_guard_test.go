package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBootstrapCallbackOwnedByWaitGroup 约束 bootstrap 回调必须由 WaitGroup 托管。
// OnShutdown 和 OnConfigChanged 不能 fire-and-forget；所有回调都要经 spawnCallback 注册，
// 让 Close() 能通过 drainCallbacks 有界等待。
//
// 非测试实现中禁止出现的直接启动形态：
//   - `go c.cfg.OnShutdown(`
//   - `go c.cfg.OnConfigChanged(`
//
// 同时确认 Close() 仍调用 drainCallbacks，避免重构时静默丢失 drain 边界。
func TestBootstrapCallbackOwnedByWaitGroup(t *testing.T) {
	const dir = "../../internal/mcpserver/common/bootstrap"

	forbidden := []string{
		"go c.cfg.OnShutdown(",
		"go c.cfg.OnConfigChanged(",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	drainFound := false
	for _, e := range entries {
		if scanBootstrapCallbackFile(t, dir, e, forbidden) {
			drainFound = true
		}
	}
	if !drainFound {
		t.Errorf("internal/mcpserver/common/bootstrap: expected drainCallbacks( to remain wired into Close()")
	}
}

func scanBootstrapCallbackFile(t *testing.T, dir string, e os.DirEntry, forbidden []string) bool {
	t.Helper()

	if e.IsDir() {
		return false
	}
	name := e.Name()
	if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(data)
	assertBootstrapCallbackForbiddenTokens(t, path, text, forbidden)
	return strings.Contains(text, "drainCallbacks(")
}

func assertBootstrapCallbackForbiddenTokens(t *testing.T, path, text string, forbidden []string) {
	t.Helper()

	for _, tok := range forbidden {
		if strings.Contains(text, tok) {
			t.Errorf("%s: forbidden fire-and-forget callback literal %q present (P22 P2 bootstrap-S1: use spawnCallback so callbackWG can drain)", path, tok)
		}
	}
}
