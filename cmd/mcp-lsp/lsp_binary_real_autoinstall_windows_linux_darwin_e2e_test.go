//go:build e2e && (darwin || linux || windows)

package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMcpLSPBinaryRealClangdCAndMQL_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real clangd e2e test in short mode")
	}
	clangdPath, err := exec.LookPath("clangd")
	if err != nil {
		t.Fatalf("real clangd e2e requires clangd in PATH: %v", err)
	}

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(clangdPath), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	cases := []struct {
		name  string
		write func(*testing.T, string) string
	}{
		{name: "c", write: writeBinaryColdStartCFixture},
		{name: "cpp", write: writeBinaryColdStartCPPFixture},
		{name: "mql4", write: func(t *testing.T, root string) string {
			return writeRealMQLClangdFixture(t, root, ".mq4", "__MQL4__")
		}},
		{name: "mql5", write: func(t *testing.T, root string) string {
			return writeRealMQLClangdFixture(t, root, ".mq5", "__MQL5__")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.name))
			diagnostics := client.callTool(t, "file", map[string]any{"action": "diagnostics", "file_path": target})
			requireMCPToolSuccess(t, client, diagnostics, "real clangd "+tc.name+" diagnostics")
			symbols := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
			requireMCPToolSuccess(t, client, symbols, "real clangd "+tc.name+" document symbols")
		})
	}
}

func writeRealMQLClangdFixture(t *testing.T, root, extension, versionDefine string) string {
	t.Helper()
	forward := writeBinaryColdStartFile(t, root, filepath.Join(".mql-auto-forwards", "robot.auto.mqh"), "int SharedValue();\n")
	writeBinaryColdStartFile(t, root, filepath.Join("Include", "consumer.mqh"), "int UseShared() { return SharedValue(); }\n")
	target := writeBinaryColdStartFile(t, root, "robot"+extension, "#include \"Include/consumer.mqh\"\nint SharedValue() { return 42; }\nvoid OnTick() { (void)UseShared(); }\n")
	arguments := []string{
		"clang++", "-xc++", "-std=c++17", "-fsyntax-only",
		"-D__MQL__", "-D" + versionDefine,
		"-I" + root, "-include", forward, target,
	}
	payload, err := json.Marshal([]map[string]any{{"directory": root, "file": target, "arguments": arguments}})
	if err != nil {
		t.Fatalf("marshal MQL clangd compile database: %v", err)
	}
	writeBinaryColdStartFile(t, root, "compile_commands.json", string(payload))
	return target
}

func TestMcpLSPBinaryDiagnosticsWithRealSystemLanguageServers_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	cases := []realLSPDiagnosticsCase{
		{"c", []string{"clangd"}, writeBinaryColdStartCFixture},
		{"cpp", []string{"clangd"}, writeBinaryColdStartCPPFixture},
		{"objective-c", []string{"clangd"}, writeBinaryColdStartObjectiveCFixture},
		{"objective-cpp", []string{"clangd"}, writeBinaryColdStartObjectiveCPPFixture},
		{"swift", []string{"sourcekit-lsp"}, writeBinaryColdStartSwiftFixture},
		{"rust", []string{"rust-analyzer"}, writeBinaryColdStartRustFixture},
		{"java", []string{"jdtls"}, writeBinaryColdStartJavaFixture},
	}
	requireHostBinariesForE2E(t, cases)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	for _, tc := range cases {
		t.Run(tc.languageID, func(t *testing.T) {
			target := tc.write(t, filepath.Join(root, tc.languageID))
			diagnostics := client.callTool(t, "file", map[string]any{
				"action":    "diagnostics",
				"file_path": target,
			})
			requireMCPToolSuccess(t, client, diagnostics, "real "+tc.languageID+" diagnostics")
		})
	}
}
