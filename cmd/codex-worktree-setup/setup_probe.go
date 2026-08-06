package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type lspProbeRequirements struct {
	tools      []string
	probeFiles []string
}

func newLSPProbeRequirements() lspProbeRequirements {
	return lspProbeRequirements{
		tools: []string{"file", "inspect", "xref", "grep", "structure", "patch_edit", "completion"},
		probeFiles: []string{
			"cmd/mcp-lsp/main.go",
			"frontend-app/src/main.jsx",
		},
	}
}

func requiredLSPTools(yield func(int, string) bool) {
	for index, name := range newLSPProbeRequirements().tools {
		if !yield(index, name) {
			return
		}
	}
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpProbe struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	encoder *json.Encoder
	decoder *json.Decoder
	stderr  bytes.Buffer
}

// probeMCP 启动真实 worktree binary，并通过 Go 与 JS 文件 diagnostics 验证语言服务器可用。
func probeMCP(ctx context.Context, binary, worktree string, env map[string]string) ([]string, error) {
	requirements := newLSPProbeRequirements()
	probe, err := startMCPProbe(ctx, binary, worktree, env)
	if err != nil {
		return nil, err
	}
	if _, err := probe.call(1, "initialize", map[string]any{"protocolVersion": "2024-11-05"}); err != nil {
		return nil, probe.fail("initialize", err)
	}
	if err := probe.notify("notifications/initialized"); err != nil {
		return nil, probe.fail("initialized notification", err)
	}
	listed, err := probe.call(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, probe.fail("tools/list", err)
	}
	names, err := decodeToolNames(listed.Result)
	if err != nil {
		return nil, probe.fail("decode tools/list", err)
	}
	if err := validateToolNames(requirements, names); err != nil {
		return nil, probe.fail("validate tools/list", err)
	}
	nextID := 3
	for _, file := range requirements.probeFiles {
		if err := probe.fileDiagnostics(nextID, file); err != nil {
			return nil, probe.fail("diagnostics "+file, err)
		}
		nextID++
	}
	if err := probe.shutdown(nextID); err != nil {
		return nil, err
	}
	return names, nil
}

// startMCPProbe 使用目标 worktree 的目录和受管环境启动 sidecar 探针。
func startMCPProbe(ctx context.Context, binary, worktree string, env map[string]string) (*mcpProbe, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = worktree
	cmd.Env = sidecarEnvironment(env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open mcp-lsp stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open mcp-lsp stdout: %w", err)
	}
	probe := &mcpProbe{
		cmd:     cmd,
		stdin:   stdin,
		encoder: json.NewEncoder(stdin),
		decoder: json.NewDecoder(bufio.NewReader(stdout)),
	}
	cmd.Stderr = &probe.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mcp-lsp: %w", err)
	}
	return probe, nil
}

func (probe *mcpProbe) call(id int, method string, params any) (rpcResponse, error) {
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := probe.encoder.Encode(request); err != nil {
		return rpcResponse{}, err
	}
	var response rpcResponse
	if err := probe.decoder.Decode(&response); err != nil {
		return rpcResponse{}, err
	}
	if response.Error != nil {
		return rpcResponse{}, fmt.Errorf("json-rpc %d: %s", response.Error.Code, response.Error.Message)
	}
	return response, nil
}

func (probe *mcpProbe) notify(method string) error {
	return probe.encoder.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": map[string]any{}})
}

// fileDiagnostics 调用公开 file 工具并拒绝 MCP 工具级错误，确保对应语言服务器真正启动。
func (probe *mcpProbe) fileDiagnostics(id int, file string) error {
	response, err := probe.call(id, "tools/call", map[string]any{
		"name": "file",
		"arguments": map[string]any{
			"action":    "diagnostics",
			"file_path": file,
		},
	})
	if err != nil {
		return err
	}
	var result struct {
		IsError *bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return fmt.Errorf("decode tools/call result: %w", err)
	}
	if result.IsError == nil {
		return errors.New("tools/call result is missing isError")
	}
	if !*result.IsError {
		return nil
	}
	messages := make([]string, 0, len(result.Content))
	for _, item := range result.Content {
		if text := strings.TrimSpace(item.Text); text != "" {
			messages = append(messages, text)
		}
	}
	return fmt.Errorf("file diagnostics failed: %s", strings.Join(messages, "; "))
}

// fail 终止失败探针并把 sidecar stderr 保留在返回错误中。
func (probe *mcpProbe) fail(stage string, cause error) error {
	_ = probe.stdin.Close()
	_ = probe.cmd.Process.Kill()
	_ = probe.cmd.Wait()
	return fmt.Errorf("mcp-lsp %s: %w; stderr=%s", stage, cause, strings.TrimSpace(probe.stderr.String()))
}

// shutdown 按 MCP 顺序关闭探针，任一步失败都立即终止进程并报告阶段。
func (probe *mcpProbe) shutdown(id int) error {
	if _, err := probe.call(id, "shutdown", map[string]any{}); err != nil {
		return probe.fail("shutdown", err)
	}
	if err := probe.notify("exit"); err != nil {
		return probe.fail("exit notification", err)
	}
	if err := probe.stdin.Close(); err != nil {
		return probe.fail("close stdin", err)
	}
	if err := probe.cmd.Wait(); err != nil {
		return fmt.Errorf("wait for mcp-lsp: %w; stderr=%s", err, strings.TrimSpace(probe.stderr.String()))
	}
	return nil
}

func decodeToolNames(raw json.RawMessage) ([]string, error) {
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		names = append(names, tool.Name)
	}
	return names, nil
}

// sidecarEnvironment 清除继承的 peer/runtime 根变量，再以受管配置确定性覆盖。
func sidecarEnvironment(overrides map[string]string) []string {
	replaced := map[string]bool{
		"GO_AGENT_CTL_RPC_ADDR": true, "GO_AGENT_PEER_MODE": true,
		"GO_AGENT_LSP_ROOT": true, "GO_AGENT_LSP_ROOTS": true,
		"PROJECT_ROOT": true, "SUPER_DOLPHIN_RUNTIME_MODE": true,
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": true, "SUPER_DOLPHIN_DEPENDENCY_PROFILE": true,
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR": true, "SUPER_DOLPHIN_LSP_MANIFEST": true,
	}
	for key := range overrides {
		replaced[key] = true
	}
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !replaced[key] {
			result = append(result, item)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+overrides[key])
	}
	return result
}

// validateToolNames 要求 tools/list 恰好包含七个短工具名。
func validateToolNames(requirements lspProbeRequirements, names []string) error {
	want := append([]string(nil), requirements.tools...)
	got := append([]string(nil), names...)
	sort.Strings(want)
	sort.Strings(got)
	if len(got) != len(want) {
		return fmt.Errorf("tools/list returned %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("tools/list returned %v, want exactly %v", got, want)
		}
	}
	return nil
}
