//go:build windows && e2e

package main

import (
	"bufio"
	"bytes"
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

func TestRealGraphQLSingleRequest90sProbeE2E(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_GO_BIN", `C:\Users\mima0000\.tools\go1.26.5-verified\go\bin\go.exe`)
	root := realNodeRepoRoot(t)
	nodeDist := filepath.Join(root, ".super-dolphin", "cache", "lsp-assets", "node-runtime", "22.22.0", "arm64", "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a", "ready", "node-v22.22.0-win-arm64")
	installDir := filepath.Join(root, ".super-dolphin", "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a")
	graphql := filepath.Join(installDir, "node_modules", ".bin", "graphql-lsp.cmd")
	target := filepath.Join(root, "bin", "LSP", "test", "graphql", "schema.graphql")
	if _, err := os.Stat(graphql); err != nil {
		t.Fatalf("real graphql cache binary=%s: %v", graphql, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("schema=%s: %v", target, err)
	}
	t.Logf("real GraphQL startup decision binary=%s argv=[server -m stream --configDir %s] productRoot=%s nodeDist=%s installDir=%s", graphql, filepath.Dir(target), root, nodeDist, installDir)
	runGraphQLDirectInitialize90s(t, graphql, filepath.Dir(target), nodeDist)
	binary := buildRealMcpLSPBinary(t, root)
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, realNodeServerCasesForLanguage("graphql")[0])
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	productRoot := filepath.Join(root, ".super-dolphin")
	client := startRealMcpLSPBinary(t, ctx, binary, fixture.workDir, root, nodeDist, installDir, productRoot)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	args := realMCPWindowsToolArguments("graphql", fixture.workDir, "structure", "document_symbol", map[string]any{"action": "document_symbol", "file_path": fixture.targetFile})
	t.Logf("MCP request id=graphql-open-file tool=file action=open_file file=%s mcp_pid=%d", fixture.targetFile, client.cmd.Process.Pid)
	done := make(chan mcpLSPBinaryResponse, 1)
	go func() { done <- client.callTool(t, "structure", args) }()
	select {
	case response := <-done:
		t.Logf("MCP response id=graphql-open-file is_error=%t content=%s stderr_tail=%s", response.Result.IsError, response.Result.ContentText(), client.stderrString())
	case <-ctx.Done():
		t.Fatalf("MCP request timeout id=graphql-open-file mcp_pid=%d binary=%s argv=[server -m stream --configDir %s] stderr_tail=%s", client.cmd.Process.Pid, graphql, fixture.workDir, client.stderrString())
	}
	for _, request := range []struct{ id, tool, action string }{{"graphql-diagnostics", "file", "diagnostics"}, {"graphql-document-symbol", "structure", "document_symbol"}} {
		request := request
		args := realMCPWindowsToolArguments("graphql", fixture.workDir, request.tool, request.action, map[string]any{"action": request.action, "file_path": fixture.targetFile})
		result := make(chan mcpLSPBinaryResponse, 1)
		started := time.Now()
		go func() { result <- client.callTool(t, request.tool, args) }()
		select {
		case response := <-result:
			content := response.Result.ContentText()
			t.Logf("MCP response id=%s elapsed=%s is_error=%t content=%s stderr_tail=%s", request.id, time.Since(started).Round(time.Millisecond), response.Result.IsError, content, client.stderrString())
			if request.action == "document_symbol" && (!strings.Contains(content, "OK total=") || strings.Contains(content, "total=0")) {
				t.Fatalf("real GraphQL document_symbol empty: %s", content)
			}
			if request.action == "document_symbol" {
				schema, err := os.ReadFile(filepath.Join(root, "bin", "LSP", "test", "graphql", "schema.graphql"))
				if err != nil || !strings.Contains(string(schema), "type Film") || !strings.Contains(string(schema), "type Person") || !strings.Contains(string(schema), "interface Node") {
					t.Fatalf("schema declaration check failed: err=%v", err)
				}
				t.Logf("rg-equivalent schema declaration check: Film/Person/Node present; MCP document_symbol content non-empty")
			}
		case <-time.After(90 * time.Second):
			t.Fatalf("MCP request timeout id=%s mcp_pid=%d binary=%s argv=[server -m stream --configDir %s] stderr_tail=%s", request.id, client.cmd.Process.Pid, graphql, fixture.workDir, client.stderrString())
		}
	}
}

func runGraphQLDirectInitialize90s(t *testing.T, graphql, configDir, nodeDist string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", graphql, "server", "-m", "stream", "--configDir", configDir)
	cmd.Dir = configDir
	cmd.Env = append(os.Environ(), "PATH="+nodeDist+";"+os.Getenv("PATH"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("direct GraphQL start binary=%s argv=%q: %v", graphql, cmd.Args, err)
	}
	t.Logf("direct GraphQL child start pid=%d binary=%s argv=%q", cmd.Process.Pid, graphql, cmd.Args)
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Logf("direct GraphQL child exit stderr_tail=%s", stderr.String())
	}()
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"processId": nil, "rootUri": "file:///" + strings.ReplaceAll(configDir, `\`, "/"), "capabilities": map[string]any{}}})
	_, _ = fmt.Fprintf(stdin, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	line := make(chan string, 1)
	go func() { got, _ := bufio.NewReader(stdout).ReadString('\n'); line <- got }()
	select {
	case got := <-line:
		t.Logf("direct GraphQL initialize header=%q", got)
	case <-ctx.Done():
		t.Fatalf("direct GraphQL initialize timeout pid=%d argv=%q stderr_tail=%s", cmd.Process.Pid, cmd.Args, stderr.String())
	}
}
