//go:build e2e

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMcpLSPBinaryExternalEnvRootAllowsWorktreeSQLDiagnostics_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	envRoot, worktree := writeExternalEnvWorktreeRoot(t, "20260706-openitems-p2-sql-diagnostics")
	target := writeExternalEnvFixture(t,
		filepath.Join(worktree, "backend", "migrations", "schema", "096_order_client_id_idempotency.sql"),
		"SELECT (\n",
	)
	writeLSPBinaryFixture(t, filepath.Join(worktree, "sqlc.yaml"), "version: '2'\nsql:\n  - engine: sqlite\n    queries: backend/migrations/schema\n")
	writeLSPBinaryFixture(t, filepath.Join(worktree, ".sqruff"), "[sqruff]\ndialect = sqlite\nrules =\n")
	sqlBinDir := installSqruffForE2E(t)

	client := startLSPBinaryClientWithExternalEnvRoot(t, buildLSPBinary(t), envRoot, worktree, sqlBinDir, nil)
	result := client.callToolWithoutTrustedScope(t, "diagnostics", map[string]any{
		"work_dir":  worktree,
		"file_path": target,
	})
	if result.IsError {
		t.Fatalf("SQL diagnostics returned error; text=%q stderr=%s",
			result.ContentText(), client.stderr.String())
	}
	payload := decodeExternalEnvDiagnosticsPayload(t, result)
	if !payload.HasFile(target) {
		t.Fatalf("SQL diagnostics did not report worktree target %s; text=%q stderr=%s",
			target, result.ContentText(), client.stderr.String())
	}
	if payload.Total == 0 {
		t.Fatalf("SQL diagnostics returned clean payload, want real sqruff parser diagnostic evidence; text=%q stderr=%s",
			result.ContentText(), client.stderr.String())
	}
}

func TestMcpLSPBinaryExternalEnvRootDiagnosticsStayOnWorktreeTarget_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	envRoot, worktree := writeExternalEnvWorktreeRoot(t, "20260706-openitems-p5-admin-ui-auth")
	mainTarget := writeExternalEnvFixture(t,
		filepath.Join(envRoot, "admin-ui-v2", "src", "features", "algo", "components", "SystemStrategyManager.test.tsx"),
		"export const mainOnly: number = 1\n",
	)
	worktreeTarget := writeExternalEnvFixture(t,
		filepath.Join(worktree, "admin-ui-v2", "src", "features", "algo", "components", "SystemStrategyManager.test.tsx"),
		"export const worktreeOnly: number = 1\n",
	)
	writeLSPBinaryFixture(t, filepath.Join(worktree, "admin-ui-v2", "tsconfig.json"), `{"compilerOptions":{"jsx":"react-jsx"}}`)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)

	client := startLSPBinaryClientWithExternalEnvRoot(t, buildLSPBinary(t), envRoot, worktree, fakeServersBinDir, nil)
	result := client.callToolWithoutTrustedScope(t, "diagnostics", map[string]any{
		"work_dir":  worktree,
		"file_path": worktreeTarget,
	})
	if result.IsError {
		t.Fatalf("worktree diagnostics returned error; text=%q stderr=%s",
			result.ContentText(), client.stderr.String())
	}
	payload := decodeExternalEnvDiagnosticsPayload(t, result)
	if !payload.HasFile(worktreeTarget) {
		t.Fatalf("diagnostics did not stay on worktree target %s; text=%q stderr=%s",
			worktreeTarget, result.ContentText(), client.stderr.String())
	}
	if payload.HasFile(mainTarget) {
		t.Fatalf("diagnostics leaked to env-root main target %s instead of worktree target %s; text=%q",
			mainTarget, worktreeTarget, result.ContentText())
	}
}

func TestMcpLSPBinaryExternalEnvRootTypeScriptSlowDiagnosticsEventuallyReady_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	envRoot, worktree := writeExternalEnvWorktreeRoot(t, "20260706-openitems-p5-admin-ui-auth")
	target := writeExternalEnvFixture(t,
		filepath.Join(worktree, "admin-ui-v2", "src", "hooks", "use-app-startup.ts"),
		"export const startupState: number = 1\n",
	)
	writeLSPBinaryFixture(t, filepath.Join(worktree, "admin-ui-v2", "tsconfig.json"), `{"compilerOptions":{}}`)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)

	client := startLSPBinaryClientWithExternalEnvRoot(t, buildLSPBinary(t), envRoot, worktree, fakeServersBinDir, []string{
		fakeMultilangDiagnosticDelayEnv + "=9s",
	})
	result := client.callToolWithoutTrustedScope(t, "diagnostics", map[string]any{
		"work_dir":  worktree,
		"file_path": target,
	})
	if result.IsError {
		t.Fatalf("TypeScript slow diagnostics returned error, want eventual diagnostics readiness; text=%q stderr=%s",
			result.ContentText(), client.stderr.String())
	}
	payload := decodeExternalEnvDiagnosticsPayload(t, result)
	if !payload.HasFile(target) {
		t.Fatalf("TypeScript slow diagnostics missing target %s; text=%q stderr=%s",
			target, result.ContentText(), client.stderr.String())
	}
	if payload.Total == 0 {
		t.Fatalf("TypeScript slow diagnostics returned clean payload, want fake diagnostic evidence; text=%q stderr=%s",
			result.ContentText(), client.stderr.String())
	}
}

func writeExternalEnvWorktreeRoot(t *testing.T, worktreeName string) (string, string) {
	t.Helper()
	envRoot := canonicalToolTestRoot(t, filepath.Join(t.TempDir(), "wjboot-v2"))
	worktree := filepath.Join(envRoot, ".worktrees", worktreeName)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree fixture: %v", err)
	}
	return envRoot, worktree
}

func writeExternalEnvFixture(t *testing.T, path, body string) string {
	t.Helper()
	writeLSPBinaryFixture(t, path, body)
	return path
}

func startLSPBinaryClientWithExternalEnvRoot(t *testing.T, binary, envRoot, processCWD, pathPrefix string, extraEnv []string) *lspBinaryClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = processCWD
	cmd.Env = lspBinaryEnv(t, envRoot)
	if strings.TrimSpace(pathPrefix) != "" {
		cmd.Env = append(cmd.Env, "PATH="+pathPrefix+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("open mcp-lsp stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("open mcp-lsp stdout: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mcp-lsp binary: %v", err)
	}
	goroutines := newTestGoroutineGroup(t)
	client := &lspBinaryClient{
		cmd:     cmd,
		stdin:   stdin,
		decoder: json.NewDecoder(stdout),
		cancel:  cancel,
		done:    make(chan error, 1),
		stderr:  stderr,
		root:    processCWD,
	}
	goroutines.Go(func() {
		client.done <- cmd.Wait()
	})
	t.Cleanup(func() {
		client.close()
	})
	client.initialize(t)
	return client
}

func decodeExternalEnvDiagnosticsPayload(t *testing.T, result lspBinaryToolResult) diagnosticsPayload {
	t.Helper()
	return decodeDiagnosticsContentText(t, result.ContentText())
}
