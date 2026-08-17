//go:build !windows

package multilsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFakeTypeScriptNavigationNodePlatform 写入 POSIX node shell fixture。
func installFakeTypeScriptNavigationNodePlatform(t *testing.T, dir, outputPath string) {
	t.Helper()
	nodePath := filepath.Join(dir, "node")
	script := strings.Join([]string{"#!/bin/sh", "cat >/dev/null", "cat " + shellQuote(outputPath), ""}, "\n")
	if err := os.WriteFile(nodePath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake node: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
