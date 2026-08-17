//go:build !windows

package tools

import (
	"encoding/json"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
)

// TestNonWindowsToolTimeoutTierSelection 锁定非 Windows 构建不继承 Windows 冷安装策略。
func TestNonWindowsToolTimeoutTierSelection(t *testing.T) {
	if got := patchEditTimeoutTier(json.RawMessage(`{"action":"rename"}`)); got != middleware.TierNormal {
		t.Fatalf("non-Windows patch_edit rename timeout=%s, want %s", got, middleware.TierNormal)
	}
	if got := fileToolTimeoutTier(json.RawMessage(`{"action":"open_file"}`)); got != middleware.TierNormal {
		t.Fatalf("non-Windows file open_file timeout=%s, want %s", got, middleware.TierNormal)
	}
}
