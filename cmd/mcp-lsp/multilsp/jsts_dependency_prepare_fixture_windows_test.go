//go:build windows

package multilsp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFakePnpmExecutablePlatform 写入 Windows pnpm.bat fixture。
func writeFakePnpmExecutablePlatform(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "pnpm.bat")
	body := "@echo off\r\n" +
		"echo %CD% %*>> \"%PNPM_LOG%\"\r\n" +
		"if not \"%1 %2 %3\"==\"install --frozen-lockfile --ignore-scripts\" exit /b 64\r\n" +
		"if not \"%PNPM_EXIT%\"==\"\" (\r\n" +
		"  echo pnpm failed 1>&2\r\n" +
		"  exit /b %PNPM_EXIT%\r\n" +
		")\r\n" +
		"mkdir node_modules >NUL 2>NUL\r\n" +
		"exit /b 0\r\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake pnpm: %v", err)
	}
}
