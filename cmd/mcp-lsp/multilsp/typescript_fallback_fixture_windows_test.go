//go:build windows

package multilsp

import (
	"os"
	"path/filepath"
	"testing"
)

// installFakeTypeScriptNavigationNodePlatform 写入 Windows node.cmd fixture。
func installFakeTypeScriptNavigationNodePlatform(t *testing.T, dir, outputPath string) {
	t.Helper()
	nodePath := filepath.Join(dir, "node.cmd")
	script := "@echo off\r\ntype \"" + outputPath + "\"\r\n"
	if err := os.WriteFile(nodePath, []byte(script), 0o600); err != nil {
		t.Fatalf("write fake Windows node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
