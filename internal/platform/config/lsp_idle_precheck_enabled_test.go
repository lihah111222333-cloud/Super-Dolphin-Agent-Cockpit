//go:build mcp_lsp_short_idle_precheck

package config

import (
	"testing"
	"time"
)

// TestLSPShortIdlePrecheckBuildTagIsExplicit 验证只有显式测试构建才能运行十五分钟以下的快速生命周期预检。
func TestLSPShortIdlePrecheckBuildTagIsExplicit(t *testing.T) {
	got, err := parseLSPIdleTimeout(lspIdleTimeoutEnv, "6m", true)
	if err != nil {
		t.Fatalf("parse short precheck timeout: %v", err)
	}
	if got != 6*time.Minute {
		t.Fatalf("short precheck timeout = %s, want 6m", got)
	}
}
