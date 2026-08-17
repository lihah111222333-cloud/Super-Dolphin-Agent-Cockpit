//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installStructureFakeTypeScriptNavigationNodePlatform 写入 Windows node.cmd fixture。
func installStructureFakeTypeScriptNavigationNodePlatform(t *testing.T, dir, outputPath string) {
	t.Helper()
	nodePath := filepath.Join(dir, "node.cmd")
	script := strings.Join([]string{"@echo off", "more >nul", "type \"" + outputPath + "\"", ""}, "\r\n")
	if err := os.WriteFile(nodePath, []byte(script), 0o600); err != nil {
		t.Fatalf("write fake Windows node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
