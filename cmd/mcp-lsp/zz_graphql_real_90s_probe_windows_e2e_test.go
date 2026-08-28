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

type graphQL90sProbeEnvironment struct {
	root       string
	nodeDist   string
	installDir string
	graphql    string
	target     string
}

type graphQLProbeRequest struct {
	id     string
	tool   string
	action string
}

type graphQLProbeRuntime struct {
	client     *mcpLSPBinaryClient
	graphql    string
	workDir    string
	goroutines *testGoroutineGroup
}

func TestRealGraphQLSingleRequest90sProbeE2E(t *testing.T) {
	environment := prepareGraphQL90sProbeEnvironment(t)
	t.Logf("real GraphQL startup decision binary=%s argv=[server -m stream --configDir %s] productRoot=%s nodeDist=%s installDir=%s", environment.graphql, filepath.Dir(environment.target), environment.root, environment.nodeDist, environment.installDir)
	runGraphQLDirectInitialize90s(t, environment.graphql, filepath.Dir(environment.target), environment.nodeDist)
	binary := buildRealMcpLSPBinary(t, environment.root)
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, realNodeServerCasesForLanguage("graphql")[0])
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	productRoot := filepath.Join(environment.root, ".super-dolphin")
	client := startRealMcpLSPBinary(t, ctx, binary, fixture.workDir, environment.root, environment.nodeDist, environment.installDir, productRoot)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	runtime := graphQLProbeRuntime{
		client: client, graphql: environment.graphql, workDir: fixture.workDir,
		goroutines: newTestGoroutineGroup(t),
	}
	args := realMCPWindowsToolArguments("graphql", fixture.workDir, "structure", "document_symbol", map[string]any{"action": "document_symbol", "file_path": fixture.targetFile})
	t.Logf("MCP request id=graphql-open-file tool=file action=open_file file=%s mcp_pid=%d", fixture.targetFile, client.cmd.Process.Pid)
	response := runtime.callBeforeContextDone(t, ctx, "graphql-open-file", "structure", args)
	t.Logf("MCP response id=graphql-open-file is_error=%t content=%s stderr_tail=%s", response.Result.IsError, response.Result.ContentText(), client.stderrString())
	for _, request := range []graphQLProbeRequest{{"graphql-diagnostics", "file", "diagnostics"}, {"graphql-document-symbol", "structure", "document_symbol"}} {
		args := realMCPWindowsToolArguments("graphql", fixture.workDir, request.tool, request.action, map[string]any{"action": request.action, "file_path": fixture.targetFile})
		started := time.Now()
		response := runtime.callWithin(t, request, args, 90*time.Second)
		content := response.Result.ContentText()
		t.Logf("MCP response id=%s elapsed=%s is_error=%t content=%s stderr_tail=%s", request.id, time.Since(started).Round(time.Millisecond), response.Result.IsError, content, client.stderrString())
		requireGraphQLProbeResponse(t, request, content, environment.target)
	}
}

// prepareGraphQL90sProbeEnvironment 解析并校验真实 GraphQL probe 的固定资产路径。
func prepareGraphQL90sProbeEnvironment(t *testing.T) graphQL90sProbeEnvironment {
	t.Helper()
	t.Setenv("SUPER_DOLPHIN_GO_BIN", `C:\Users\mima0000\.tools\go1.26.5-verified\go\bin\go.exe`)
	root := realNodeRepoRoot(t)
	nodeDist := filepath.Join(root, ".super-dolphin", "cache", "lsp-assets", "node-runtime", "22.22.0", "arm64", "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a", "ready", "node-v22.22.0-win-arm64")
	installDir := filepath.Join(root, ".super-dolphin", "cache", "lsp-assets", "npm-cohort", "22.22.0", "arm64", "5b44fd410df7b4cd0a1891a05a7b606f8fb7d8786a94997b996a372e82478d7a")
	graphql := filepath.Join(installDir, "node_modules", ".bin", "graphql-lsp.cmd")
	target := filepath.Join(root, "bin", "LSP", "test", "graphql", "schema.graphql")
	requireGraphQLProbePath(t, "real graphql cache binary", graphql)
	requireGraphQLProbePath(t, "schema", target)
	return graphQL90sProbeEnvironment{root: root, nodeDist: nodeDist, installDir: installDir, graphql: graphql, target: target}
}

// requireGraphQLProbePath 校验 probe 依赖路径存在并保留原始错误上下文。
func requireGraphQLProbePath(t *testing.T, label, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s=%s: %v", label, path, err)
	}
}

// startToolCall 通过测试 goroutine group 启动一个受控 MCP 调用。
func (runtime graphQLProbeRuntime) startToolCall(t *testing.T, tool string, args map[string]any) <-chan mcpLSPBinaryResponse {
	result := make(chan mcpLSPBinaryResponse, 1)
	runtime.goroutines.Go(func() {
		result <- runtime.client.callTool(t, tool, args)
	})
	return result
}

// callBeforeContextDone 等待 MCP 调用完成，并保持首请求共享 90 秒 context 的超时语义。
func (runtime graphQLProbeRuntime) callBeforeContextDone(t *testing.T, ctx context.Context, id, tool string, args map[string]any) mcpLSPBinaryResponse {
	t.Helper()
	select {
	case response := <-runtime.startToolCall(t, tool, args):
		return response
	case <-ctx.Done():
		t.Fatalf("MCP request timeout id=%s mcp_pid=%d binary=%s argv=[server -m stream --configDir %s] stderr_tail=%s", id, runtime.client.cmd.Process.Pid, runtime.graphql, runtime.workDir, runtime.client.stderrString())
		return mcpLSPBinaryResponse{}
	}
}

// callWithin 等待后续 MCP 调用完成，并保持每次 90 秒 timer 的超时语义。
func (runtime graphQLProbeRuntime) callWithin(t *testing.T, request graphQLProbeRequest, args map[string]any, timeout time.Duration) mcpLSPBinaryResponse {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case response := <-runtime.startToolCall(t, request.tool, args):
		return response
	case <-timer.C:
		t.Fatalf("MCP request timeout id=%s mcp_pid=%d binary=%s argv=[server -m stream --configDir %s] stderr_tail=%s", request.id, runtime.client.cmd.Process.Pid, runtime.graphql, runtime.workDir, runtime.client.stderrString())
		return mcpLSPBinaryResponse{}
	}
}

// requireGraphQLProbeResponse 校验 document_symbol 内容与真实 schema 声明。
func requireGraphQLProbeResponse(t *testing.T, request graphQLProbeRequest, content, schemaPath string) {
	t.Helper()
	if request.action != "document_symbol" {
		return
	}
	if !strings.Contains(content, "OK total=") || strings.Contains(content, "total=0") {
		t.Fatalf("real GraphQL document_symbol empty: %s", content)
	}
	schema, err := os.ReadFile(schemaPath)
	if err != nil || !strings.Contains(string(schema), "type Film") || !strings.Contains(string(schema), "type Person") || !strings.Contains(string(schema), "interface Node") {
		t.Fatalf("schema declaration check failed: err=%v", err)
	}
	t.Logf("rg-equivalent schema declaration check: Film/Person/Node present; MCP document_symbol content non-empty")
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
	newTestGoroutineGroup(t).Go(func() {
		got, _ := bufio.NewReader(stdout).ReadString('\n')
		line <- got
	})
	select {
	case got := <-line:
		t.Logf("direct GraphQL initialize header=%q", got)
	case <-ctx.Done():
		t.Fatalf("direct GraphQL initialize timeout pid=%d argv=%q stderr_tail=%s", cmd.Process.Pid, cmd.Args, stderr.String())
	}
}
