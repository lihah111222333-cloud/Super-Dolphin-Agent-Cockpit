//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMcpLSPBinaryRustSemanticWorkspaceReady_E2E is the real-session red/green
// contract for the failure where rust-analyzer starts but Cargo semantic data
// has not loaded. The JSON log is intentionally complete so a failed run is
// directly replayable against the checked-in fixture.
func TestMcpLSPBinaryRustSemanticWorkspaceReady_E2E(t *testing.T) {
	repo := repoRootForMcpLSPBinaryTest(t)
	fixture := filepath.Join(repo, "bin", "LSP", "test", "rust")
	hostPreflight := map[string]any{}
	for _, name := range []string{"rust-analyzer", "cargo", "rustc", "rustup"} {
		path, err := exec.LookPath(name)
		hostPreflight[name] = map[string]any{"available": err == nil, "path": path, "error": errorText(err)}
	}
	root := t.TempDir()
	if err := copyRustFixture(t, fixture, root); err != nil {
		t.Fatal(err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"MCP_LSP_TRACE_TIMING=1",
		"MCP_LSP_TRACE_LSP_SHAPES=1",
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	target := filepath.Join(root, "src", "main.rs")
	posMain := target + ":10:4"
	posArgs := target + ":11:9"
	posArgsCompletion := target + ":12:12"
	observed := map[string]any{
		"process_started": true,
		"workspace":       fixture,
		"server":          map[string]any{"binary": "rust-analyzer", "toolchain": []string{"cargo", "rustc"}},
		"host_preflight":  hostPreflight,
		"calls": []map[string]any{
			{"tool": "file", "action": "diagnostics", "file_path": target},
			{"tool": "inspect", "action": "hover", "pos": posMain},
			{"tool": "structure", "action": "document_symbol", "file_path": target},
			{"tool": "inspect", "action": "definition", "pos": posArgs},
			{"tool": "xref", "action": "references", "pos": posArgs},
			{"tool": "completion", "pos": posArgsCompletion},
		},
		"expectation": "process started and diagnostics/document symbols work, while Rust semantic workspace is loaded before navigation",
	}
	results := make([]map[string]any, 0, 6)
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"file", map[string]any{"action": "diagnostics", "file_path": target}},
		{"inspect", map[string]any{"action": "hover", "pos": posMain}},
		{"structure", map[string]any{"action": "document_symbol", "file_path": target}},
		{"inspect", map[string]any{"action": "definition", "pos": posArgs}},
		{"xref", map[string]any{"action": "references", "pos": posArgs}},
		{"completion", map[string]any{"pos": posArgsCompletion}},
	} {
		result := client.callTool(t, call.tool, call.args)
		results = append(results, map[string]any{
			"tool":     call.tool,
			"args":     call.args,
			"is_error": result.Result.IsError,
			"content":  result.Result.ContentText(),
		})
		observed["observed_results"] = results
		if result.Result.IsError || strings.TrimSpace(result.Result.ContentText()) == "" {
			observed["failed_call"] = map[string]any{"tool": call.tool, "args": call.args, "result": result.Result.ContentText(), "stderr": client.stderrString()}
			payload, _ := json.MarshalIndent(observed, "", "  ")
			t.Fatalf("Rust semantic workspace E2E failed; reproduction JSON:\n%s", payload)
		}
		if call.tool == "file" && !strings.Contains(result.Result.ContentText(), "OK total=0") {
			t.Fatalf("Rust diagnostics are not zero: %s", result.Result.ContentText())
		}
		if call.tool == "structure" && !strings.Contains(result.Result.ContentText(), "name=Config") {
			t.Fatalf("Rust document symbols do not contain Config: %s", result.Result.ContentText())
		}
	}
	t.Logf("Rust semantic workspace E2E PASS; reproduction JSON:\n%s", mustJSON(observed))
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func copyRustFixture(t *testing.T, source, target string) error {
	t.Helper()
	for _, rel := range []string{"Cargo.toml", "rust-toolchain.toml", filepath.Join("src", "main.rs")} {
		path := filepath.Join(source, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read Rust fixture %s: %w", path, err)
		}
		dest := filepath.Join(target, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("write Rust fixture %s: %w", dest, err)
		}
	}
	return nil
}

func mustJSON(value any) string {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(payload)
}
