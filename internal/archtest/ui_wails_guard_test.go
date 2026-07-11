package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUIWailsNoDirectUIStateImport 锁定 ui/wails 到 uistate 的依赖方向。
// 生产代码只能通过 contract.UIProjectStateFacade 和共享 DTO 访问 UI 状态，不能直接依赖模块私有 Service。
// 扫描范围刻意限制为非测试 Go 文件，测试桩仍可构造更贴近生产的 fixture。
func TestUIWailsNoDirectUIStateImport(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	wailsDir := filepath.Join(root, "internal", "ui", "wails")

	entries, err := os.ReadDir(wailsDir)
	if err != nil {
		t.Fatalf("read %s: %v", wailsDir, err)
	}

	const forbiddenImport = `"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate"`
	var hits []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(wailsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), forbiddenImport) {
			hits = append(hits, name)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("internal/ui/wails reintroduced direct uistate imports (P4 §1 dependency-direction violation); offending files: %v", hits)
	}
}

// TestUIWailsActiveAgentPredicateFromContract 确认“agent 是否活跃”的判断来自 contract 包。
// 这样 ui/wails 和 orchestration 共享同一状态定义，避免本地 helper 重新分叉。
func TestUIWailsActiveAgentPredicateFromContract(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	modulePath := filepath.Join(root, "internal", "ui", "wails", "module.go")
	data, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read %s: %v", modulePath, err)
	}
	src := string(data)
	forbidden := []string{
		"func isActiveAgentState(",
	}
	required := []string{
		"contract.IsActiveAgentState(",
	}
	for _, token := range forbidden {
		if strings.Contains(src, token) {
			t.Errorf("ui/wails/module.go reintroduced private active-agent predicate; forbidden token: %q", token)
		}
	}
	for _, token := range required {
		if !strings.Contains(src, token) {
			t.Errorf("ui/wails/module.go must call contract.IsActiveAgentState; missing token: %q", token)
		}
	}
}
