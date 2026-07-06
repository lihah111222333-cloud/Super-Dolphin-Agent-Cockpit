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

func TestSQLLanguageServerNPMInstallRecipeStarts_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	prefix := installSQLLanguageServerRecipeForE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(prefix, "node_modules", ".bin", mcpLSPExecutableFileName("sql-language-server"))
	version := exec.CommandContext(ctx, binary, "--version")
	if out, err := version.CombinedOutput(); err != nil {
		t.Fatalf("sql-language-server --version after recipe install: %v\n%s", err, out)
	} else if !strings.Contains(string(out), "1.7.1") {
		t.Fatalf("sql-language-server --version = %q, want 1.7.1", out)
	}

	requireSQLLanguageServerInitialize(t, ctx, binary)
}

func TestMcpLSPBinarySQLDiagnosticsAutoInstallsMissingLanguageServer_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell scripts as fake npm and installed LSP binary")
	}

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := writeBinaryColdStartSQLFixture(t, root)
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "npm-args")
	writeFakeSQLAutoInstallNPM(t, binDir)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, binDir, []string{
		"PATH=" + binDir,
		"FAKE_SQL_INSTALL_BIN=" + binDir,
		"FAKE_SQL_NPM_MARKER=" + marker,
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	requireMCPToolSuccess(t, client, diagnostics, "sql diagnostics after auto-install")
	requireFakeSQLNPMArgs(t, marker)

	payload := decodeDiagnosticsStructuredContent(t, diagnostics.Result.StructuredContent)
	if !payload.HasFile(target) {
		t.Fatalf("sql diagnostics missing target %s: payload=%#v raw=%s text=%q stderr=%s",
			target, payload, diagnostics.Result.StructuredContent,
			diagnostics.Result.ContentText(), client.stderrString())
	}
	message := payload.FirstMessageForFile(t, target)
	if !strings.Contains(message, "fake cold-start diagnostic for sql") {
		t.Fatalf("sql diagnostics message = %q, want fake SQL diagnostic; raw=%s stderr=%s",
			message, diagnostics.Result.StructuredContent, client.stderrString())
	}
}

func TestMcpLSPBinarySQLDiagnosticsWithRealLanguageServer_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	prefix := installSQLLanguageServerRecipeForE2E(t)
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	target := writeBinaryColdStartSQLFixture(t, root)
	sqlBinDir := filepath.Join(prefix, "node_modules", ".bin")

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

func requireSQLLanguageServerInitialize(t *testing.T, ctx context.Context, binary string) {
	t.Helper()
	proc := startSQLLanguageServerProcessForE2E(t, ctx, binary)
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

func startSQLLanguageServerProcessForE2E(t *testing.T, ctx context.Context, binary string) *sqlLanguageServerProcess {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, "up", "--method", "stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("sql-language-server stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("sql-language-server stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("sql-language-server stderr pipe: %v", err)
	}
	var stderr lockedStringBuilder
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sql-language-server: %v", err)
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

func installSQLLanguageServerRecipeForE2E(t *testing.T) string {
	t.Helper()
	prefix := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	install := exec.CommandContext(ctx, "npm", "install", "--prefix", prefix, "sql-language-server", "vscode-languageserver-protocol@3.17.5", "vscode-jsonrpc@8.2.0")
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install SQL language server recipe: %v\n%s", err, out)
	}
	return prefix
}

func writeFakeSQLAutoInstallNPM(t *testing.T, binDir string) {
	t.Helper()
	path := filepath.Join(binDir, "npm")
	script := `#!/bin/sh
set -eu
case " $* " in
  *" sql-language-server "*) ;;
  *) echo "missing sql-language-server install arg: $*" >&2; exit 1 ;;
esac
case " $* " in
  *" vscode-languageserver-protocol@3.17.5 "*) ;;
  *) echo "missing pinned vscode-languageserver-protocol install arg: $*" >&2; exit 1 ;;
esac
case " $* " in
  *" vscode-jsonrpc@8.2.0 "*) ;;
  *) echo "missing pinned vscode-jsonrpc install arg: $*" >&2; exit 1 ;;
esac
printf '%s\n' "$*" > "$FAKE_SQL_NPM_MARKER"
/bin/cat > "$FAKE_SQL_INSTALL_BIN/sql-language-server" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  echo "1.7.1"
  exit 0
fi
MCP_LSP_FAKE_MULTILANG_DIAGNOSTICS=1 exec ` + shellQuote(os.Args[0]) + ` -test.run=TestFakeMultilangDiagnosticsLangserverHelper -- "$@"
EOF
/bin/chmod +x "$FAKE_SQL_INSTALL_BIN/sql-language-server"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
}

func requireFakeSQLNPMArgs(t *testing.T, marker string) {
	t.Helper()
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read fake npm marker: %v", err)
	}
	args := string(raw)
	for _, want := range []string{"install", "-g", "sql-language-server", "vscode-languageserver-protocol@3.17.5", "vscode-jsonrpc@8.2.0"} {
		if !strings.Contains(args, want) {
			t.Fatalf("fake npm args = %q, missing %q", args, want)
		}
	}
}
