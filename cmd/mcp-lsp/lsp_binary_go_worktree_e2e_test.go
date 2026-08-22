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

func TestLSPBinaryGoDiagnosticsIgnoresUnrelatedAmbientGoWorkForWorktree(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	requireRealGopls(t)

	parent := canonicalToolTestRoot(t, t.TempDir())
	mainModule := filepath.Join(parent, "main")
	worktree := filepath.Join(parent, ".worktrees", "feature")
	writeLSPBinaryFixture(t, filepath.Join(mainModule, "go.mod"), "module example.com/main\n\ngo 1.25.0\n")
	writeLSPBinaryFixture(t, filepath.Join(mainModule, "main.go"), "package main\n\nfunc main() {}\n")
	goWorkPath := filepath.Join(parent, "go.work")
	writeLSPBinaryFixture(t, goWorkPath, "go 1.25.0\n\nuse ./main\n")

	target := filepath.Join(worktree, "main.go")
	writeLSPBinaryFixture(t, filepath.Join(worktree, "go.mod"), "module example.com/worktree\n\ngo 1.25.0\n")
	writeLSPBinaryFixture(t, target, "package main\n\nfunc main() {}\n")
	binary := goWorktreeLSPBinaryUnderTest(t)
	t.Setenv("GOWORK", goWorkPath)

	client := startPrebuiltLSPBinaryClient(t, binary, worktree)
	diagnostics := client.callTool(t, "diagnostics", map[string]any{
		"file_path": target,
	})
	if diagnostics.IsError {
		t.Fatalf("diagnostics returned MCP error result; text=%q stderr=%s",
			diagnostics.ContentText(), client.stderr.String())
	}

	payload := decodeDiagnosticsContentText(t, diagnostics.ContentText())
	if containsGoWorkspaceConfigurationDiagnostic(payload, target) {
		t.Fatalf("diagnostics leaked unrelated ambient go.work %s into worktree target; text=%q stderr=%s",
			goWorkPath, diagnostics.ContentText(), client.stderr.String())
	}
}

func goWorktreeLSPBinaryUnderTest(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("MCP_LSP_GO_WORKTREE_E2E_BINARY"); binary != "" {
		return binary
	}
	return buildLSPBinary(t)
}

func startPrebuiltLSPBinaryClient(t *testing.T, binary, root string) *lspBinaryClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = root
	cmd.Env = lspBinaryEnv(t, root)
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
		root:    root,
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

func containsGoWorkspaceConfigurationDiagnostic(payload diagnosticsPayload, target string) bool {
	for _, table := range payload.Data {
		if table.File != target {
			continue
		}
		for _, row := range table.Rows {
			if len(row) < 4 {
				continue
			}
			message, _ := row[3].(string)
			normalized := strings.ToLower(message)
			if strings.Contains(normalized, "go.work") || strings.Contains(normalized, "workspace") {
				return true
			}
		}
	}
	return false
}
