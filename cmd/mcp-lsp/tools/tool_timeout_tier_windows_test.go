//go:build windows

package tools

import (
	"encoding/json"
	"testing"
)

// TestWindowsToolTimeoutTierSelection 锁定 Windows 构建实际选用 Windows 冷安装策略。
func TestWindowsToolTimeoutTierSelection(t *testing.T) {
	if got := patchEditTimeoutTier(json.RawMessage(`{"action":"rename"}`)); got != toolTimeoutDisabled {
		t.Fatalf("Windows patch_edit rename timeout=%s, want disabled", got)
	}
	if got := fileToolTimeoutTier(json.RawMessage(`{"action":"open_file"}`)); got != toolTimeoutDisabled {
		t.Fatalf("Windows file open_file timeout=%s, want disabled", got)
	}
}
