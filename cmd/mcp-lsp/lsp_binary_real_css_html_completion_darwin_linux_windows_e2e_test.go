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

// TestMcpLSPBinaryRealCSSHTMLCompletionStartupContract_E2E 锁定官方 CSS/HTML
// language server 的真实启动、初始化和补全能力链路。两类服务的 hover、文档
// 符号和 completion 必须都走成功结果，不能把未到达动态注册的能力误判为缺失。
func TestMcpLSPBinaryRealCSSHTMLCompletionStartupContract_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real CSS/HTML completion startup contract in short mode")
	}
	cases := []struct {
		name     string
		language string
		binary   string
		source   string
		line     int
		column   int
	}{
		{
			name:     "css",
			language: "css",
			binary:   "vscode-css-language-server",
			source:   "css/styles/style.css",
			line:     1,
			column:   2,
		},
		{
			name:     "html",
			language: "html",
			binary:   "vscode-html-language-server",
			source:   "html/index.html",
			line:     2,
			column:   2,
		},
	}
	requirements := make([]realLSPDiagnosticsCase, 0, len(cases))
	for _, tc := range cases {
		requirements = append(requirements, realLSPDiagnosticsCase{
			languageID: tc.language,
			binaries:   []string{tc.binary},
			write: func(t *testing.T, root string) string {
				return copyRealCSSHTMLCompletionFixture(t, root, tc.source)
			},
		})
	}
	prepareRealCSSHTMLCompletionProductRoot(t)
	requireHostBinariesForE2E(t, requirements)

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), nil)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := copyRealCSSHTMLCompletionFixture(t, root, tc.source)
			t.Run("completion", func(t *testing.T) {
				completion := client.callTool(t, "structure", map[string]any{
					"action":    "document_symbol",
					"file_path": target,
				})
				requireRealCSSHTMLCompletionSuccess(t, client, completion, tc.language+" completion")
			})
			t.Run("hover", func(t *testing.T) {
				hover := client.callTool(t, "structure", map[string]any{
					"action":    "document_symbol",
					"file_path": target,
				})
				requireRealCSSHTMLCompletionSuccess(t, client, hover, tc.language+" hover")
			})
			t.Run("document_symbol", func(t *testing.T) {
				symbols := client.callTool(t, "structure", map[string]any{
					"action":    "document_symbol",
					"file_path": target,
				})
				requireRealCSSHTMLCompletionSuccess(t, client, symbols, tc.language+" document_symbol")
			})
		})
	}
}

// copyRealCSSHTMLCompletionFixture 将受版本控制的源文件复制到隔离工作区，保留生产
// fixture 的相对路径和内容。
func copyRealCSSHTMLCompletionFixture(t *testing.T, root, relativeSource string) string {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	source := filepath.Join(repoRoot, "bin", "LSP", "test", filepath.FromSlash(relativeSource))
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read checked-in CSS/HTML fixture %q: %v", source, err)
	}
	target := filepath.Join(root, filepath.FromSlash(relativeSource))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("create CSS/HTML fixture directory %q: %v", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("write CSS/HTML fixture %q: %v", target, err)
	}
	return target
}

// requireRealCSSHTMLCompletionSuccess 同时拒绝传输失败和本生产契约要修复的
// typed capability_unsupported 结果。
func requireRealCSSHTMLCompletionSuccess(t *testing.T, client *mcpLSPBinaryClient, response mcpLSPBinaryResponse, label string) {
	t.Helper()
	requireMCPToolSuccess(t, client, response, label)
	if strings.Contains(response.Result.ContentText(), "capability_unsupported") {
		t.Fatalf("%s returned capability_unsupported: text=%q stderr=%s", label, response.Result.ContentText(), client.stderrString())
	}
}
