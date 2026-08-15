package tools

import (
	"testing"
	"time"
)

// TestEditLSPSyncTimeoutCoversColdLanguageServerStartup 锁定冷启动较慢的真实
// language server 不会重新落回历史上的两秒同步窗口。
func TestEditLSPSyncTimeoutCoversColdLanguageServerStartup(t *testing.T) {
	if editLSPSyncTimeout != 60*time.Second {
		t.Fatalf("edit LSP sync timeout = %s, want 60s", editLSPSyncTimeout)
	}
}
