//go:build windows && e2e

package main

import (
	"os"
	"path/filepath"
	"strings"
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
	contents, err := os.ReadFile(filepath.Join(root, "src", "component{demo}.jsx"))
	if err != nil || !strings.Contains(string(contents), needle) {
		t.Fatalf("native literal-brace fixture search err=%v contents=%q", err, contents)
	}
}
