//go:build linux && e2e

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

const linuxManagedSwiftRubyE2ETimeout = 30 * time.Minute

// TestMcpLSPBinaryLinuxManagedSwiftRubyRealServers_E2E 使用空 PATH 启动真实
// sourcekit-lsp 与 solargraph，锁定受管下载、冷启动、语义请求和同进程复用。
func TestMcpLSPBinaryLinuxManagedSwiftRubyRealServers_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real managed Swift/Ruby E2E in short mode")
	}
	if linuxManagedSwiftRubyE2ETimeout <= 10*time.Minute {
		t.Fatalf("managed Swift/Ruby timeout = %s, want greater than 10 minutes", linuxManagedSwiftRubyE2ETimeout)
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), linuxManagedSwiftRubyE2ETimeout)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	cases := []binaryColdStartLanguageCase{
		{languageID: "swift", write: writeBinaryColdStartSwiftFixture},
		{languageID: "ruby", write: writeBinaryColdStartRubyFixture},
	}
	for _, tc := range cases {
		t.Run(tc.languageID, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.languageID))
			for iteration := 1; iteration <= 2; iteration++ {
				started := time.Now()
				result := client.callTool(t, "structure", map[string]any{
					"action":    "document_symbol",
					"file_path": target,
				})
				t.Logf("managed language=%s hover iteration=%d elapsed=%s", tc.languageID, iteration, time.Since(started))
				requireMCPToolSuccess(t, client, result, "real managed "+tc.languageID+" hover")
			}
			diagnostics := client.callTool(t, "diagnostics", map[string]any{"file_path": target})
			requireMCPToolSuccess(t, client, diagnostics, "real managed "+tc.languageID+" diagnostics")
		})
	}
}
