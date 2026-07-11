package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadyAndVerifyLinkedWorktree(t *testing.T) {
	linkedRoot, companionBin := createLinkedWorktreeFixture(t)
	t.Setenv("PATH", strings.Join([]string{companionBin, os.Getenv("PATH")}, string(os.PathListSeparator)))

	report, err := runSetup(context.Background(), setupOptions{Command: commandReady, Worktree: linkedRoot})
	if err != nil {
		t.Fatalf("ready linked worktree: %v", err)
	}
	for _, output := range []string{report.Paths.Binary, report.Paths.Config} {
		rel, err := filepath.Rel(linkedRoot, output)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("output escaped worktree: %q rel=%q err=%v", output, rel, err)
		}
	}
	before, err := os.ReadFile(report.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := runSetup(context.Background(), setupOptions{Command: commandVerify, Worktree: linkedRoot})
	if err != nil {
		t.Fatalf("verify linked worktree: %v", err)
	}
	after, err := os.ReadFile(verified.Paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("verify modified config")
	}
}

func TestVerifyRejectsLanguageServerDiagnosticsFailure(t *testing.T) {
	linkedRoot, companionBin := createLinkedWorktreeFixture(t)
	t.Setenv("PATH", strings.Join([]string{companionBin, os.Getenv("PATH")}, string(os.PathListSeparator)))

	if _, err := runSetup(context.Background(), setupOptions{Command: commandReady, Worktree: linkedRoot}); err != nil {
		t.Fatalf("ready linked worktree: %v", err)
	}
	t.Setenv("FAKE_LSP_DIAGNOSTICS_ERROR", "broken language server")
	_, err := runSetup(context.Background(), setupOptions{Command: commandVerify, Worktree: linkedRoot})
	if err == nil || !strings.Contains(err.Error(), "diagnostics") {
		t.Fatalf("verify error = %v, want diagnostics failure", err)
	}
}

func createLinkedWorktreeFixture(t *testing.T) (string, string) {
	t.Helper()
	fixtureRoot := t.TempDir()
	mainRoot := filepath.Join(fixtureRoot, "main")
	linkedRoot := filepath.Join(fixtureRoot, "linked")
	if err := os.MkdirAll(filepath.Join(mainRoot, "cmd", "mcp-lsp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mainRoot, "frontend-app", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(mainRoot, "go.mod"), "module example.com/worktree-fixture\n\ngo 1.22\n")
	writeFixtureFile(t, filepath.Join(mainRoot, "cmd", "mcp-lsp", "main.go"), fakeMCPServerSource)
	writeFixtureFile(t, filepath.Join(mainRoot, "frontend-app", "src", "main.jsx"), "export const ready = true;\n")
	runGitFixture(t, mainRoot, "init", "--initial-branch=main")
	runGitFixture(t, mainRoot, "config", "user.name", "Codex Test")
	runGitFixture(t, mainRoot, "config", "user.email", "codex-test@example.invalid")
	runGitFixture(t, mainRoot, "add", "go.mod", "cmd/mcp-lsp/main.go", "frontend-app/src/main.jsx")
	runGitFixture(t, mainRoot, "commit", "-m", "fixture")
	runGitFixture(t, mainRoot, "worktree", "add", "-b", "codex/integration", linkedRoot)

	companionBin := filepath.Join(fixtureRoot, "companions")
	if err := os.MkdirAll(companionBin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gopls", "typescript-language-server", "tsserver"} {
		writeExecutable(t, filepath.Join(companionBin, executableName(name)))
	}
	return canonicalTestPath(t, linkedRoot), companionBin
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

const fakeMCPServerSource = `package main

import (
	"encoding/json"
	"os"
)

type request struct {
	ID     any    ` + "`json:\"id\"`" + `
	Method string ` + "`json:\"method\"`" + `
	Params struct {
		Name      string         ` + "`json:\"name\"`" + `
		Arguments map[string]any ` + "`json:\"arguments\"`" + `
	} ` + "`json:\"params\"`" + `
}

func main() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	seenGo := false
	seenJavaScript := false
	for {
		var request request
		if err := decoder.Decode(&request); err != nil {
			return
		}
		switch request.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
		case "tools/list":
			tools := []map[string]string{
				{"name": "file"}, {"name": "inspect"}, {"name": "xref"},
				{"name": "grep"}, {"name": "structure"}, {"name": "patch_edit"},
				{"name": "completion"},
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": tools}})
		case "tools/call":
			path, _ := request.Params.Arguments["file_path"].(string)
			switch path {
			case "cmd/mcp-lsp/main.go":
				seenGo = true
			case "frontend-app/src/main.jsx":
				seenJavaScript = true
			default:
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32602, "message": "unexpected diagnostics path"}})
				continue
			}
			if request.Params.Name != "file" || os.Getenv("FAKE_LSP_DIAGNOSTICS_ERROR") != "" {
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
					"isError": true,
					"content": []map[string]string{{"type": "text", "text": "language-server diagnostics failed"}},
				}})
				continue
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
				"isError": false,
				"content": []map[string]string{{"type": "text", "text": "No diagnostics"}},
			}})
		case "shutdown":
			if !seenGo || !seenJavaScript {
				_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32000, "message": "missing language diagnostics probe"}})
				continue
			}
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": nil})
		case "exit":
			return
		}
	}
}
`
