//go:build darwin && e2e

package main

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	darwinIdleRealNodePath       = "/opt/homebrew/bin/node"
	darwinIdleRealTypeScriptPath = "/opt/homebrew/bin/typescript-language-server"
)

// resolveIdleRealToolPaths 解析 macOS Homebrew 的真实工具；缺失时保持原 E2E 的 N/V 跳过语义。
func resolveIdleRealToolPaths(t *testing.T) idleRealToolPaths {
	t.Helper()
	nodePath := requireDarwinIdleRealTool(t, darwinIdleRealNodePath)
	typeScriptPath := requireDarwinIdleRealTool(t, darwinIdleRealTypeScriptPath)
	return idleRealToolPaths{
		nodePath:         nodePath,
		typeScriptPath:   typeScriptPath,
		serverSearchPath: filepath.Dir(darwinIdleRealTypeScriptPath),
	}
}

func requireDarwinIdleRealTool(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		t.Skipf("N/V: exact Darwin real tool %s is unavailable: %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved == "" {
		t.Skipf("N/V: exact Darwin real tool %s has no resolvable native target: %v", path, err)
	}
	resolvedInfo, err := os.Stat(resolved)
	if err != nil || resolvedInfo.IsDir() || resolvedInfo.Mode()&0111 == 0 {
		t.Skipf("N/V: exact Darwin real tool %s resolved to a non-executable target %s: %v", path, resolved, err)
	}
	return resolved
}
