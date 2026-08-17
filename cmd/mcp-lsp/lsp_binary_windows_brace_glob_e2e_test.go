//go:build windows && e2e

package main

import (
	"path/filepath"
	"testing"
)

// TestLSPBinaryWindowsEscapedBraceGlobMatchesLiteralFilename verifies that
// Windows path normalization preserves glob escapes for literal braces.
func TestLSPBinaryWindowsEscapedBraceGlobMatchesLiteralFilename(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	needle := "windowsEscapedBraceNeedle"
	writeLSPBinaryFixture(t, filepath.Join(root, "src", "component{demo}.jsx"), "export const literal = '"+needle+"';\n")
	writeLSPBinaryFixture(t, filepath.Join(root, "src", "componentdemo.jsx"), "export const plain = '"+needle+"';\n")
	client := startLSPBinaryClient(t, root)

	result := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       needle,
		"path":        "src",
		"glob":        `**/*\{demo\}.jsx`,
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error for escaped literal-brace glob: %s; stderr=%s", result.ContentText(), client.stderr.String())
	}
	payload := decodeLSPBinaryGrepContent(t, result.ContentText())
	if payload.Total != 1 || payload.Showing != 1 {
		t.Fatalf("escaped literal-brace glob payload = total:%d showing:%d, want 1/1; content=%s stderr=%s",
			payload.Total, payload.Showing, result.ContentText(), client.stderr.String())
	}
	rows := lspBinaryGrepRowsForFile(t, root, payload, "src/component{demo}.jsx")
	if len(rows.Rows) != 1 {
		t.Fatalf("escaped literal-brace glob rows = %d, want 1", len(rows.Rows))
	}
}
