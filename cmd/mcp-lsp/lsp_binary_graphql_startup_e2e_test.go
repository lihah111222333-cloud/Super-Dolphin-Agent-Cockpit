//go:build e2e && (darwin || linux || windows)

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinaryGraphQLStartupPassesProjectConfigDir_E2E 锁定生产启动契约：
// GraphQL server 必须在第一次 document_symbol 请求前收到项目文件系统根，
// 否则 graphql-language-service-cli 会静默返回空符号/诊断。
func TestMcpLSPBinaryGraphQLStartupPassesProjectConfigDir_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping GraphQL production startup contract E2E in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	root := t.TempDir()
	target := copyGraphQLBinLSPFixtureForStartupE2E(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	serverName, ok := fakeProtocolServerForLanguage("graphql")
	if !ok {
		t.Fatal("missing GraphQL fake protocol server mapping")
	}
	bundle := writeFakeProtocolBundle(t, binary, fakeServersBinDir, serverName, "graphql")
	extraEnv := append([]string{
		fakeMultilangRequireGraphQLConfigDirEnv + "=1",
		fakeMultilangGraphQLConfigDirEnv + "=" + root,
	}, bundle.extraEnv...)
	extraEnv = append(extraEnv,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR="+bundle.bundleDir,
		"SUPER_DOLPHIN_LSP_MANIFEST="+bundle.manifestPath,
	)
	client := startFakeProtocolBundleClientForTest(t, ctx, bundle, root, fakeServersBinDir, extraEnv, serverName)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	result := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "graphql",
	})
	requireMCPToolSuccess(t, client, result, "GraphQL document_symbol startup contract")
	if !strings.Contains(result.Result.ContentText(), "FakeSymbol") {
		t.Fatalf("GraphQL document_symbol returned no symbols after production startup: text=%q stderr=%s", result.Result.ContentText(), client.stderrString())
	}
}

func copyGraphQLBinLSPFixtureForStartupE2E(t *testing.T, root string) string {
	t.Helper()
	sourceRoot := filepath.Join(repoRootForMcpLSPBinaryTest(t), "bin", "LSP", "test", "graphql")
	for _, name := range []string{"package.json", "schema.graphql", ".graphqlrc.yml"} {
		payload, err := os.ReadFile(filepath.Join(sourceRoot, name))
		if err != nil {
			t.Fatalf("read GraphQL bin/LSP/test/%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, name), payload, 0o600); err != nil {
			t.Fatalf("copy GraphQL bin/LSP/test/%s: %v", name, err)
		}
	}
	return filepath.Join(root, "schema.graphql")
}
