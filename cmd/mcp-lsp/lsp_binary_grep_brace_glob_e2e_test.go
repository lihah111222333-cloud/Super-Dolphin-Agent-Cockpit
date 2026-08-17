//go:build e2e

package main

import (
	"path/filepath"
	"testing"
)

func TestLSPBinaryGrepBraceGlobMatchesFrontendJSAndJSX(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	needle := "braceGlobNeedle"
	writeLSPBinaryFixture(t, filepath.Join(root, "frontend-app", "src", "state", "client.js"), "export const clientMarker = '"+needle+"';\n")
	writeLSPBinaryFixture(t, filepath.Join(root, "frontend-app", "src", "App.jsx"), "export function App() { return <main>"+needle+"</main>; }\n")
	client := startLSPBinaryClient(t, root)

	result := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       needle,
		"paths":       []string{"frontend-app/src"},
		"glob":        "**/*.{js,jsx}",
		"max_results": 10,
	})
	if result.IsError {
		t.Fatalf("grep returned tool error for frontend JS/JSX brace glob: %s; stderr=%s", result.ContentText(), client.stderr.String())
	}
	payload := decodeLSPBinaryGrepContent(t, result.ContentText())
	if payload.Total != 2 || payload.Showing != 2 {
		t.Fatalf("grep brace glob payload = total:%d showing:%d, want 2/2; content=%s stderr=%s",
			payload.Total, payload.Showing, result.ContentText(), client.stderr.String())
	}
	for _, want := range []string{"frontend-app/src/state/client.js", "frontend-app/src/App.jsx"} {
		rows := lspBinaryGrepRowsForFile(t, root, payload, want)
		if len(rows.Rows) != 1 {
			t.Fatalf("grep rows for %s = %d, want 1", want, len(rows.Rows))
		}
	}
}
