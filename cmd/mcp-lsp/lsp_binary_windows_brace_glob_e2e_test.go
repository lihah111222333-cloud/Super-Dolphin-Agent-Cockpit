//go:build windows && e2e

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
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
		"paths":       []string{"src"},
		"glob":        `**/*\{demo\}.jsx`,
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error for escaped literal-brace glob: %s; stderr=%s", result.ContentText(), client.stderr.String())
	}
	doc, err := lineprotocol.Parse(result.ContentText())
	if err != nil {
		t.Fatalf("parse escaped literal-brace grep content: %v; content=%s", err, result.ContentText())
	}
	if doc.Header.Total != 1 || doc.Header.Showing != 1 {
		t.Fatalf("escaped literal-brace glob payload = total:%d showing:%d, want 1/1; content=%s stderr=%s",
			doc.Header.Total, doc.Header.Showing, result.ContentText(), client.stderr.String())
	}
	want := filepath.ToSlash(filepath.Join(root, "src", "component{demo}.jsx"))
	rows := 0
	for _, record := range doc.Records {
		if record.Kind == "ROW" && strings.EqualFold(filepath.ToSlash(record.Fields["file"]), want) {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("escaped literal-brace glob rows = %d, want 1; content=%s", rows, result.ContentText())
	}
}
