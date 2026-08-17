//go:build !windows && !linux && e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinarySQLiteDiagnosticsAutoInstallsMissingLanguageServer_E2E 验证仅在
// 非 Windows、非 Linux 平台启用的 Cargo recipe；平台选择由 build tag 完成。
func TestMcpLSPBinarySQLiteDiagnosticsAutoInstallsMissingLanguageServer_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := writeBinaryColdStartSQLFixture(t, root)
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: .\n")
	writeBinaryColdStartFile(t, root, ".sqruff", "[sqruff]\ndialect = sqlite\nrules =\n")
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "cargo-args")
	writeFakeSQLAutoInstallCargo(t, binDir)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, binDir, []string{
		"PATH=" + binDir,
		"FAKE_SQL_INSTALL_BIN=" + binDir,
		"FAKE_SQL_CARGO_MARKER=" + marker,
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{"action": "diagnostics", "file_path": target})
	requireMCPToolSuccess(t, client, diagnostics, "sql diagnostics after auto-install")
	requireFakeSQLCargoArgs(t, marker)
	payload := decodeDiagnosticsContentText(t, diagnostics.Result.ContentText())
	if payload.Total != 0 || payload.HasFile(target) {
		t.Fatalf("valid SQLite diagnostics after auto-install = %#v, want no diagnostics; text=%q stderr=%s", payload, diagnostics.Result.ContentText(), client.stderrString())
	}
}

// TestMcpLSPBinarySQLiteDiagnosticsAutoInstallsWithRealCargo_E2E 锁定同一平台
// recipe 的真实 Cargo 安装；Windows/Linux 生产安装链由各自专用源码证明。
func TestMcpLSPBinarySQLiteDiagnosticsAutoInstallsWithRealCargo_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := writeBinaryColdStartSQLFixture(t, root)
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: .\n")
	writeBinaryColdStartFile(t, root, ".sqruff", "[sqruff]\ndialect = sqlite\nrules =\n")
	cargoHome := filepath.Join(t.TempDir(), "cargo-home")
	toolBin := symlinkHostToolsForE2E(t, "cargo", "rustc", "rustup")
	path := filepath.Join(cargoHome, "bin") + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{"PATH=" + path, "CARGO_HOME=" + cargoHome})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{"action": "diagnostics", "file_path": target})
	requireMCPToolSuccess(t, client, diagnostics, "SQLite diagnostics after real Cargo auto-install")
	installed := filepath.Join(cargoHome, "bin", "sqruff")
	if out, err := exec.CommandContext(ctx, installed, "--version").CombinedOutput(); err != nil {
		t.Fatalf("real Cargo-installed sqruff --version: %v\n%s", err, out)
	} else if !strings.Contains(string(out), sqruffInstallVersion) {
		t.Fatalf("real Cargo-installed sqruff version = %q, want %s", out, sqruffInstallVersion)
	}
}

// writeFakeSQLAutoInstallCargo 写入仅供该非 Windows/非 Linux recipe 使用的 POSIX fake Cargo。
func writeFakeSQLAutoInstallCargo(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "cargo")
	script := `#!/bin/sh
set -eu
case " $* " in
  *" sqruff "*) ;;
  *) echo "missing sqruff install arg: $*" >&2; exit 1 ;;
esac
printf '%s\n' "$*" > "$FAKE_SQL_CARGO_MARKER"
/bin/cat > "$FAKE_SQL_INSTALL_BIN/sqruff" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  echo "sqruff 0.38.0"
  exit 0
fi
MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS=1 exec ` + shellQuote(os.Args[0]) + ` -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- "$@"
EOF
/bin/chmod +x "$FAKE_SQL_INSTALL_BIN/sqruff"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake cargo: %v", err)
	}
}

// requireFakeSQLCargoArgs 校验专用 Cargo recipe 的锁定版本参数。
func requireFakeSQLCargoArgs(t *testing.T, marker string) {
	t.Helper()
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read fake cargo marker: %v", err)
	}
	args := string(raw)
	for _, want := range []string{"install", "sqruff", "--version", sqruffInstallVersion, "--locked"} {
		if !strings.Contains(args, want) {
			t.Fatalf("fake cargo args = %q, missing %q", args, want)
		}
	}
}
