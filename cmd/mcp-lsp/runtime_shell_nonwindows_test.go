//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// TestSetupInstallerRegistersShellLanguageServer 验证非 Windows PATH fixture
// 的 shell adapter；Windows 生产路径使用锁定 native cache。
func TestSetupInstallerRegistersShellLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "bash-language-server")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("bash-language-server"))
	writeMcpLSPExecutable(t, binDir, "shellcheck")
	t.Setenv("PATH", binDir)

	result, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "shellscript")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(shellscript) error = %v", err)
	}
	if wantBinary := runtimeNPMExecutableName("bash-language-server"); result.Binary != wantBinary {
		t.Fatalf("shell installer binary = %q, want %q", result.Binary, wantBinary)
	}
	if result.Path != fakeServer {
		t.Fatalf("shell installer path = %q, want %q", result.Path, fakeServer)
	}
}

// TestSetupInstallerInstallsShellcheckWhenShellServerAlreadyExists 验证非
// Windows POSIX npm fixture 在缺少 shellcheck 时执行安装并留下 typed 结果。
func TestSetupInstallerInstallsShellcheckWhenShellServerAlreadyExists(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "bash-language-server")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("bash-language-server"))
	fakeNPM := filepath.Join(binDir, mcpLSPExecutableFileName("npm"))
	marker := filepath.Join(binDir, "npm-called")
	script := `#!/bin/sh
set -eu
case " $* " in
  *" shellcheck "* | *" shellcheck@"*) ;;
  *) echo "missing shellcheck install arg: $*" >&2; exit 1 ;;
esac
printf '%s\n' "$*" > "$FAKE_NPM_MARKER"
printf '#!/bin/sh\nexit 0\n' > "$FAKE_INSTALL_BIN/shellcheck"
/bin/chmod +x "$FAKE_INSTALL_BIN/shellcheck"
`
	if err := os.WriteFile(fakeNPM, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("FAKE_INSTALL_BIN", binDir)
	t.Setenv("FAKE_NPM_MARKER", marker)

	result, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "shellscript")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(shellscript) error = %v", err)
	}
	if result.Path != fakeServer {
		t.Fatalf("shell installer path = %q, want %q", result.Path, fakeServer)
	}
	if _, err := os.Stat(filepath.Join(binDir, mcpLSPExecutableFileName("shellcheck"))); err != nil {
		t.Fatalf("shellcheck dependency was not installed: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("shell installer did not invoke npm when shellcheck was missing: %v", err)
	}
}
