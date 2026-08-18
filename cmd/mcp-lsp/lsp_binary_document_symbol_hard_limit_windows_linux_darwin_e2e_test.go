//go:build e2e && (darwin || linux || windows)

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

// TestMcpLSPBinaryDocumentSymbolHardLimitExplainsRecoveryWindowsLinuxDarwin_E2E 锁定三平台真实 stdio MCP 的容量失败原因与恢复方案。
func TestMcpLSPBinaryDocumentSymbolHardLimitExplainsRecoveryWindowsLinuxDarwin_E2E(t *testing.T) {
	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsShutdownWarningLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		fakeGoplsManyDocumentSymbolsEnv + "=1",
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	t.Cleanup(func() { client.close(t) })
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	requireDocumentSymbolHardLimitRecoveryE2E(t, client, target)
}

func requireDocumentSymbolHardLimitRecoveryE2E(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	result := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "go",
		"max_results": 50,
	})
	requireMCPToolSuccess(t, client, result, "document symbol hard limit recovery")
	text := result.Result.ContentText()
	doc := requireLineProtocolDocumentE2E(t, text)
	if doc.Header.Total != 60 || doc.Header.Showing != 50 || !doc.Header.Truncated {
		t.Fatalf("document symbol hard limit = total:%d showing:%d truncated:%v, want 60/50/true; text=%q",
			doc.Header.Total, doc.Header.Showing, doc.Header.Truncated, text)
	}

	attributes, hint := documentSymbolHardLimitRecoveryE2E(doc.Records)
	if got := attributes["failure_reason"]; got != "document_symbol_hard_limit_reached" {
		t.Fatalf("document symbol hard limit failure_reason = %q, want document_symbol_hard_limit_reached; text=%q", got, text)
	}
	if got := attributes["effective_limit"]; got != "50" {
		t.Fatalf("document symbol hard limit effective_limit = %q, want 50; text=%q", got, text)
	}
	if got := attributes["next_step"]; got != "narrow_file_or_symbol_scope" {
		t.Fatalf("document symbol hard limit next_step = %q, want narrow_file_or_symbol_scope; text=%q", got, text)
	}
	if hint != "next: document_symbol reached the protocol hard limit (50); narrow the file/symbol scope" {
		t.Fatalf("document symbol hard limit hint = %q, want recovery plan; text=%q", hint, text)
	}
}

func documentSymbolHardLimitRecoveryE2E(records []lineprotocol.Record) (map[string]string, string) {
	var attributes map[string]string
	var hint string
	for _, record := range records {
		switch record.Kind {
		case "ATTR":
			attributes = record.Fields
		case "HINT":
			hint = record.Value
		}
	}
	return attributes, hint
}
