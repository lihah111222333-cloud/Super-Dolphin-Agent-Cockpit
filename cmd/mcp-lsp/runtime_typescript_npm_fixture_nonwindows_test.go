//go:build !windows

package main

import (
	"os"
	"testing"
)

// writeTypeScriptNPMBinaryFixture 在非 Windows 上保留 npm 全局安装的符号链接形态。
func writeTypeScriptNPMBinaryFixture(t *testing.T, languageServerCLI, binaryPath string) {
	t.Helper()
	if err := os.Symlink(languageServerCLI, binaryPath); err != nil {
		t.Fatalf("symlink typescript-language-server: %v", err)
	}
}
