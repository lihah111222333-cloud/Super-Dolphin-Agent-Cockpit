//go:build e2e

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
)

// TestStandaloneOfficialClaudeProbe 验证实际官方 CLI 的无模型 control 握手；不启动 daemon，也不把协议成功当作模型执行成功。
func TestStandaloneOfficialClaudeProbe(t *testing.T) {
	cli, home, model := requireManagedProviderInputs(t, "CLAUDE")
	fixture := writeManagedServeE2EFixture(t, "claude", cli, home, model)
	cfg, err := standalone.LoadConfig(fixture.config)
	if err != nil {
		t.Fatal("load private Claude probe configuration failed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	results, err := standalone.ProbeConfiguredProviders(ctx, cfg)
	if err != nil {
		t.Fatalf("official Claude control handshake failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly one provider observation, got %d", len(results))
	}
	if results[0].Name != "claude" || results[0].Executable != cli || results[0].Protocol != "claude-stream-json" {
		t.Fatal("official Claude probe identity or protocol mismatch")
	}
}
