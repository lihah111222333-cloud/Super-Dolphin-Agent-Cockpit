//go:build e2e

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPBinaryGrepBraceGlobMatchesFrontendJSAndJSX(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	root := canonicalToolTestRoot(t, t.TempDir())
	needle := "braceGlobNeedle"
	writeLSPBinaryFixture(t, filepath.Join(root, "frontend-app", "src", "state", "client.js"), "export const clientMarker = '"+needle+"';\n")
	writeLSPBinaryFixture(t, filepath.Join(root, "frontend-app", "src", "App.jsx"), "export function App() { return <main>"+needle+"</main>; }\n")
	for _, want := range []string{"frontend-app/src/state/client.js", "frontend-app/src/App.jsx"} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(want)))
		if err != nil || !strings.Contains(string(contents), needle) {
			t.Fatalf("native fixture search %s = %q, err=%v; want %q", want, contents, err, needle)
		}
	}
}
