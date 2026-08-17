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

// TestMcpLSPBinaryGrepAndReadFileShareFreshSnapshotForAllLanguages_E2E 锁定全部注册语言的搜索与精确读取共享最新磁盘快照。
func TestMcpLSPBinaryGrepAndReadFileShareFreshSnapshotForAllLanguages_E2E(t *testing.T) {
	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	for _, tc := range binaryColdStartLanguageCases(t) {
		t.Run(tc.languageID, func(t *testing.T) {
			runFileSnapshotCoherenceCase(t, binary, fakeServersBinDir, tc)
		})
	}
}

func runFileSnapshotCoherenceCase(t *testing.T, binary, fakeServersBinDir string, tc binaryColdStartLanguageCase) {
	t.Helper()
	root := t.TempDir()
	target := tc.write(t, root)
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s fixture: %v", tc.languageID, err)
	}
	stale := "StaleFileSnapshot-" + tc.languageID
	fresh := "FreshFileSnapshot-" + tc.languageID
	writeFileSnapshotFixture(t, target, freshWorkspaceSymbolContent(tc.languageID, string(original), stale))

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "lsp-cache"),
	})
	t.Cleanup(func() { client.close(t) })
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	warmWorkspaceSymbolDocument(t, client, target, tc.languageID)

	staleContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read opened %s fixture: %v", tc.languageID, err)
	}
	writeFileSnapshotFixture(t, target, strings.ReplaceAll(string(staleContent), stale, fresh))
	assertGrepAndReadFileFreshSnapshot(t, client, tc.languageID, target, stale, fresh)
}

func assertGrepAndReadFileFreshSnapshot(t *testing.T, client *mcpLSPBinaryClient, languageID, target, stale, fresh string) {
	t.Helper()
	search := client.callTool(t, "grep", map[string]any{
		"action": "text_search", "query": fresh, "paths": []string{target}, "max_results": 10,
	})
	requireMCPToolSuccess(t, client, search, languageID+" fresh grep")
	requireToolResultContains(t, search, fresh, languageID+" fresh grep marker")

	read := client.callTool(t, "file", map[string]any{
		"action": "read_file", "pos": target + ":1", "scope": "lines", "limit": 200,
	})
	requireMCPToolSuccess(t, client, read, languageID+" fresh read_file")
	text := read.Result.ContentText()
	if !strings.Contains(text, fresh) {
		t.Fatalf("%s read_file did not observe grep's fresh snapshot: %s", languageID, text)
	}
	if strings.Contains(text, stale) {
		t.Fatalf("%s read_file retained stale marker after grep observed rewrite: %s", languageID, text)
	}
}

func writeFileSnapshotFixture(t *testing.T, target, content string) {
	t.Helper()
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("write file snapshot fixture %s: %v", target, err)
	}
}
