//go:build windows

package main

import (
	"os"
	"testing"
)

// writeTypeScriptNPMBinaryFixture 在 Windows 上创建常规 command wrapper。
// packaged-wrapper 测试不验证符号链接语义，因此不应要求 SeCreateSymbolicLinkPrivilege。
func writeTypeScriptNPMBinaryFixture(t *testing.T, _ string, binaryPath string) {
	t.Helper()
	if err := os.WriteFile(binaryPath, []byte("@echo off\r\nexit /b 0\r\n"), 0o700); err != nil {
		t.Fatalf("write Windows typescript-language-server wrapper: %v", err)
	}
}
