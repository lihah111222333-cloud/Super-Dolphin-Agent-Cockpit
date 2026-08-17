//go:build !windows && !linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// TestSetupInstallerRegistersSQLiteSQLLanguageServer 验证 Darwin/FreeBSD 等
// 非 Windows、非 Linux 平台的 PATH sqruff fixture；Windows/Linux 使用生产缓存。
func TestSetupInstallerRegistersSQLiteSQLLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "sqruff")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("sqruff"))
	t.Setenv("PATH", binDir)

	result, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "sql")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	if result.Binary != "sqruff" || result.Path != fakeServer {
		t.Fatalf("sql installer result = %#v, want sqruff at %q", result, fakeServer)
	}
}

// TestSetupInstallerInstallsPinnedSQLiteSQLLanguageServer 验证非 Windows、
// 非 Linux 平台的 cargo 安装参数和锁定版本。
func TestSetupInstallerInstallsPinnedSQLiteSQLLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	cargoHome := filepath.Join(t.TempDir(), "cargo-home")
	fakeCargo := filepath.Join(binDir, "cargo")
	marker := filepath.Join(t.TempDir(), "cargo-args")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$CARGO_ARGS_MARKER"
/bin/mkdir -p "$CARGO_HOME/bin"
/bin/cat > "$CARGO_HOME/bin/sqruff" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  echo "sqruff 0.38.0"
fi
exit 0
EOF
/bin/chmod +x "$CARGO_HOME/bin/sqruff"
`
	if err := os.WriteFile(fakeCargo, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake cargo: %v", err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("CARGO_ARGS_MARKER", marker)

	if _, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "sql"); err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read cargo args: %v", err)
	}
	want := "install sqruff --version " + sqruffInstallVersion + " --locked"
	if strings.TrimSpace(string(raw)) != want {
		t.Fatalf("cargo args = %q, want %q", strings.TrimSpace(string(raw)), want)
	}
}

// TestSetupInstallerRegistersSQLLanguageServer 验证非 Windows、非 Linux
// 平台的通用 SQL adapter PATH 解析；共享 helper 只提供跨平台 fixture 写入。
func TestSetupInstallerRegistersSQLLanguageServer(t *testing.T) {
	binDir := t.TempDir()
	writeMcpLSPExecutable(t, binDir, "sqruff")
	fakeServer := filepath.Join(binDir, mcpLSPExecutableFileName("sqruff"))
	t.Setenv("PATH", binDir)

	result, err := mustSetupInstaller(t).EnsureInstalledDetailed(lspinstaller.WithInstallCommandCapability(context.Background()), "sql")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(sql) error = %v", err)
	}
	if result.Binary != "sqruff" {
		t.Fatalf("sql installer binary = %q, want sqruff", result.Binary)
	}
	if result.Path != fakeServer {
		t.Fatalf("sql installer path = %q, want %q", result.Path, fakeServer)
	}
}
