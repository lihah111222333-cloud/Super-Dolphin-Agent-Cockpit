//go:build e2e

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSqruffLSPStarts_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	binDir := installSqruffForE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(binDir, mcpLSPExecutableFileName("sqruff"))
	version := exec.CommandContext(ctx, binary, "--version")
	if out, err := version.CombinedOutput(); err != nil {
		t.Fatalf("sqruff --version after recipe install: %v\n%s", err, out)
	} else if !strings.Contains(strings.ToLower(string(out)), "sqruff") {
		t.Fatalf("sqruff --version = %q, want sqruff version output", out)
	}

	requireSqruffInitialize(t, ctx, binary)
}

func TestMcpLSPBinarySQLiteDiagnosticsAutoInstallsMissingLanguageServer_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell scripts as fake cargo and installed LSP binary")
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
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "sql diagnostics after auto-install")
	requireFakeSQLCargoArgs(t, marker)

	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if payload.Total != 0 || payload.HasFile(target) {
		t.Fatalf("valid SQLite diagnostics after auto-install = %#v, want no diagnostics; raw=%s stderr=%s",
			payload, diagnostics.Result.StructuredContent, client.stderrString())
	}
}

func TestMcpLSPBinarySQLiteDiagnosticsWithRealLanguageServer_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	sqlBinDir := installSqruffForE2E(t)
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := writeBinaryColdStartSQLFixture(t, root)
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: .\n")
	writeBinaryColdStartFile(t, root, ".sqruff", "[sqruff]\ndialect = sqlite\nrules =\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, sqlBinDir, []string{
		"PATH=" + sqlBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "real sql diagnostics")
}

func TestMcpLSPBinarySQLiteInvalidSQLProducesRealDiagnostic_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	sqlBinDir := installSqruffForE2E(t)
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := writeBinaryColdStartFile(t, root, "queries/invalid.sql", "SELECT (\n")
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: sqlite\n    queries: queries\n")
	writeBinaryColdStartFile(t, root, ".sqruff", "[sqruff]\ndialect = sqlite\nrules =\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, sqlBinDir, []string{
		"PATH=" + sqlBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "invalid SQLite diagnostics")
	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if payload.Total == 0 || !payload.HasFile(target) {
		t.Fatalf("invalid SQLite SQL produced no parser diagnostic: payload=%#v raw=%s stderr=%s", payload, diagnostics.Result.StructuredContent, client.stderrString())
	}
}

func TestMcpLSPBinarySQLiteDiagnosticsAutoInstallsWithRealCargo_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX PATH isolation")
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
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"CARGO_HOME=" + cargoHome,
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "SQLite diagnostics after real Cargo auto-install")
	installed := filepath.Join(cargoHome, "bin", "sqruff")
	if out, err := exec.CommandContext(ctx, installed, "--version").CombinedOutput(); err != nil {
		t.Fatalf("real Cargo-installed sqruff --version: %v\n%s", err, out)
	} else if !strings.Contains(string(out), sqruffInstallVersion) {
		t.Fatalf("real Cargo-installed sqruff version = %q, want %s", out, sqruffInstallVersion)
	}
}

func TestMcpLSPBinarySQLDiagnosticsAcceptsSQLiteQuestionMarkPlaceholder_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	sqlBinDir := installSqruffForE2E(t)
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	writeBinaryColdStartFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n  - engine: \"sqlite\"\n    queries: \"cmd/mcp-orch/sql/queries\"\n")
	writeBinaryColdStartFile(t, root, ".sqruff", "[sqruff]\ndialect = sqlite\nrules =\n")
	target := writeBinaryColdStartFile(t, root, "cmd/mcp-orch/sql/queries/command_card.sql", "-- name: GetCommandCard :one\nSELECT id\nFROM command_cards\nWHERE card_key = ?;\n")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, sqlBinDir, []string{
		"PATH=" + sqlBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "SQLite question-mark placeholder diagnostics")

	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if payload.Total != 0 || payload.HasFile(target) {
		t.Fatalf("valid SQLite question-mark placeholder produced diagnostics: payload=%#v raw=%s text=%q stderr=%s",
			payload, diagnostics.Result.StructuredContent, diagnostics.Result.ContentText(), client.stderrString())
	}
}

func requireSqruffInitialize(t *testing.T, ctx context.Context, binary string) {
	t.Helper()
	proc := startSqruffProcessForE2E(t, ctx, binary)
	defer proc.close(t)
	writeSQLInitializeRequest(t, proc)
	response := readSQLInitializeResponse(t, proc)
	requireSQLInitializeCapabilities(t, response, proc.stderr.String())
}

type sqlLanguageServerProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *lockedStringBuilder
	waiters *sync.WaitGroup
}

func startSqruffProcessForE2E(t *testing.T, ctx context.Context, binary string) *sqlLanguageServerProcess {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, "lsp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("sqruff stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("sqruff stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("sqruff stderr pipe: %v", err)
	}
	var stderr lockedStringBuilder
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sqruff: %v", err)
	}
	var waiters sync.WaitGroup
	waiters.Go(func() {
		_, _ = io.Copy(&stderr, stderrPipe)
	})
	return &sqlLanguageServerProcess{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdoutPipe),
		stderr:  &stderr,
		waiters: &waiters,
	}
}

func writeSQLInitializeRequest(t *testing.T, proc *sqlLanguageServerProcess) {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"processId": nil,
			"rootUri":   "file://" + t.TempDir(),
			"capabilities": map[string]any{
				"textDocument": map[string]any{},
			},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal initialize: %v", err)
	}
	if _, err := proc.stdin.Write(append([]byte("Content-Length: "+strconv.Itoa(len(raw))+"\r\n\r\n"), raw...)); err != nil {
		t.Fatalf("write initialize: %v; stderr=%s", err, proc.stderr.String())
	}
}

func readSQLInitializeResponse(t *testing.T, proc *sqlLanguageServerProcess) json.RawMessage {
	t.Helper()
	response, err := readFakeLSPFramedMessage(proc.stdout)
	if err != nil {
		t.Fatalf("read initialize response: %v; stderr=%s", err, proc.stderr.String())
	}
	return response
}

func requireSQLInitializeCapabilities(t *testing.T, response json.RawMessage, stderr string) {
	t.Helper()
	var payload struct {
		ID     int `json:"id"`
		Result struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		t.Fatalf("unmarshal initialize response: %v; raw=%s stderr=%s", err, response, stderr)
	}
	if payload.Error != nil {
		t.Fatalf("initialize returned error %q; raw=%s stderr=%s", payload.Error.Message, response, stderr)
	}
	if payload.ID != 1 || len(payload.Result.Capabilities) == 0 {
		t.Fatalf("initialize response = %s, want capabilities for request id 1; stderr=%s", response, stderr)
	}
}

func (p *sqlLanguageServerProcess) close(t *testing.T) {
	t.Helper()
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	p.waiters.Wait()
}

func installSqruffForE2E(t *testing.T) string {
	t.Helper()
	venv := filepath.Join(t.TempDir(), "venv")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	create := exec.CommandContext(ctx, "python3", "-m", "venv", venv)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create sqruff test venv: %v\n%s", err, out)
	}
	binDir := filepath.Join(venv, "bin")
	if runtime.GOOS == "windows" {
		binDir = filepath.Join(venv, "Scripts")
	}
	install := exec.CommandContext(ctx, filepath.Join(binDir, mcpLSPExecutableFileName("pip")), "install", "sqruff=="+sqruffInstallVersion)
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install sqruff test backend: %v\n%s", err, out)
	}
	return binDir
}

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
