package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestStdioMCPClientRequestSkipsNotificationsUntilMatchingResponse(t *testing.T) {
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"p1"}}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	raw, err := client.request(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		t.Fatalf("request() error = %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("request() result = %s, want {\"ok\":true}", raw)
	}
	if len(transport.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(transport.writes))
	}
}

func TestStdioMCPClientCallToolForwardsWorkspaceRootsMetadata(t *testing.T) {
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	_, err := client.CallTool(context.Background(), "grep", json.RawMessage(`{"query":"x","_workspaceRoots":["/forged"]}`), ToolCallRequest{
		AgentID:        "agent-1",
		ThreadID:       "thread-1",
		CallID:         "call-1",
		CWD:            "/repo",
		WorkspaceRoots: []string{"/repo", "/repo/extra"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(transport.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(transport.writes))
	}
	write, ok := transport.writes[0].(map[string]any)
	if !ok {
		t.Fatalf("write type = %T, want map[string]any", transport.writes[0])
	}
	params, ok := write["params"].(map[string]any)
	if !ok {
		t.Fatalf("write params = %#v, want map[string]any", write["params"])
	}
	got, ok := params[MetadataKeyWorkspaceRoots].([]string)
	if !ok {
		t.Fatalf("params _workspaceRoots = %#v, want []string", params[MetadataKeyWorkspaceRoots])
	}
	want := []string{"/repo", "/repo/extra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("params _workspaceRoots = %#v, want %#v", got, want)
	}
}

func TestStdioMCPClientCallToolPreservesMCPIsError(t *testing.T) {
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"success\":false,\"code\":\"path_outside_workspace\"}"}],"isError":true,"structuredContent":{"success":false,"code":"path_outside_workspace"}}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	got, err := client.CallTool(context.Background(), "file", json.RawMessage(`{}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false", got)
	}
}

func TestStdioMCPClientCallToolConvertsJSONRPCErrorToToolResult(t *testing.T) {
	const message = "context_mode=focused requires non-empty context field"
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"` + message + `"}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	got, err := client.CallTool(context.Background(), "launch_agent", json.RawMessage(`{"name":"worker"}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want tool failure result", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false", got)
	}
	if len(got.ContentItems) != 1 || got.ContentItems[0].Text != message {
		t.Fatalf("CallTool() content = %#v, want %q", got.ContentItems, message)
	}
}

func TestStdioMCPClientCallToolRejectsNullSuccessPayload(t *testing.T) {
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"null"}],"structuredContent":{}}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	got, err := client.CallTool(context.Background(), "launch_agent", json.RawMessage(`{"name":"worker"}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false for null success payload", got)
	}
	if len(got.ContentItems) != 1 || !strings.Contains(got.ContentItems[0].Text, "empty result") {
		t.Fatalf("CallTool() content = %#v, want empty result error", got.ContentItems)
	}
}

func TestStdioMCPClientRejectsOversizePeerResponse(t *testing.T) {
	transport := &fakeStdioTransport{reads: []json.RawMessage{
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"blob":"` + strings.Repeat("x", 1048577) + `"}}`),
	}}
	client := &stdioMCPClient{transport: transport}

	_, err := client.request(context.Background(), "tools/list", map[string]any{})
	if err == nil {
		t.Fatal("request() error = nil, want oversize peer response error")
	}
	if !strings.Contains(err.Error(), "exceeds stdio message limit") {
		t.Fatalf("request() error = %v, want stdio message limit", err)
	}
}

func TestStdioMCPClientCloseTerminatesChildProcesses(t *testing.T) {
	if os.Getenv("TOOLBRIDGE_STDIO_CHILD_HELPER") == "1" {
		runStdioChildTestHelper()
		return
	}
	if os.Getenv("TOOLBRIDGE_STDIO_MCP_HELPER") == "1" {
		runStdioMCPTestHelper()
		return
	}

	marker := filepath.Join(t.TempDir(), "child.pid")
	client, err := newStdioMCPClient(context.Background(), providerdto.MCPBinary{
		Name: "helper",
		Command: []string{
			os.Args[0],
			"-test.run=TestStdioMCPClientCloseTerminatesChildProcesses",
		},
		Env: map[string]string{
			"TOOLBRIDGE_STDIO_MCP_HELPER": "1",
			"TOOLBRIDGE_STDIO_PID_FILE":   marker,
		},
	})
	if err != nil {
		t.Fatalf("newStdioMCPClient() error = %v", err)
	}
	childPID := waitForPIDFile(t, marker)
	if childPID <= 0 {
		t.Fatalf("child pid = %d, want > 0", childPID)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertProcessExited(t, childPID)
}

func TestStdioMCPClientScrubsDatabaseEnvFromParentAndManifest(t *testing.T) {
	if os.Getenv("TOOLBRIDGE_STDIO_MCP_HELPER") == "1" {
		runStdioMCPTestHelper()
		return
	}

	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "parent.db"))
	t.Setenv("SUPER_DOLPHIN_INTERNAL_SQLITE_PATH", filepath.Join(t.TempDir(), "parent-internal.db"))
	t.Setenv("TOOLBRIDGE_SAFE_PARENT", "keep-parent")

	envPath := filepath.Join(t.TempDir(), "stdio-env.txt")
	client, err := newStdioMCPClient(context.Background(), providerdto.MCPBinary{
		Name: "env-helper",
		Command: []string{
			os.Args[0],
			"-test.run=^TestStdioMCPClientScrubsDatabaseEnvFromParentAndManifest$",
		},
		Env: map[string]string{
			"TOOLBRIDGE_STDIO_MCP_HELPER":        "1",
			"TOOLBRIDGE_STDIO_ENV_FILE":          envPath,
			"TOOLBRIDGE_SAFE_MANIFEST":           "keep-manifest",
			"DATABASE_URL":                       "postgres://manifest@localhost/super_dolphin",
			"POSTGRES_CONNECTION_STRING":         "postgres://manifest-compat@localhost/super_dolphin",
			"SUPER_DOLPHIN_SQLITE_PATH":          filepath.Join(t.TempDir(), "manifest.db"),
			"SUPER_DOLPHIN_INTERNAL_SQLITE_PATH": filepath.Join(t.TempDir(), "manifest-internal.db"),
		},
	})
	if err != nil {
		t.Fatalf("newStdioMCPClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	env := waitForEnvFile(t, envPath)
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"} {
		requireEnvKeyAbsent(t, env, key)
	}
	requireEnvValueInSlice(t, env, "TOOLBRIDGE_SAFE_PARENT", "keep-parent")
	requireEnvValueInSlice(t, env, "TOOLBRIDGE_SAFE_MANIFEST", "keep-manifest")
}

type fakeStdioTransport struct {
	reads  []json.RawMessage
	writes []any
	closed bool
}

func (t *fakeStdioTransport) ReadMessage() (json.RawMessage, error) {
	next := t.reads[0]
	t.reads = t.reads[1:]
	return next, nil
}

func (t *fakeStdioTransport) WriteMessage(payload any) error {
	t.writes = append(t.writes, payload)
	return nil
}

func (t *fakeStdioTransport) Close() error {
	t.closed = true
	return nil
}

func runStdioMCPTestHelper() {
	if envFile := os.Getenv("TOOLBRIDGE_STDIO_ENV_FILE"); envFile != "" {
		if err := os.WriteFile(envFile, []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write env file: %v\n", err)
			os.Exit(2)
		}
	}
	marker := os.Getenv("TOOLBRIDGE_STDIO_PID_FILE")
	if marker == "" {
		serveMinimalStdioMCP()
		return
	}
	child := exec.Command(os.Args[0], "-test.run=TestStdioMCPClientCloseTerminatesChildProcesses")
	child.Env = append(withoutEnvKey(os.Environ(), "TOOLBRIDGE_STDIO_MCP_HELPER"), "TOOLBRIDGE_STDIO_CHILD_HELPER=1")
	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start child: %v\n", err)
		os.Exit(2)
	}
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("%d", child.Process.Pid)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write marker: %v\n", err)
		os.Exit(2)
	}
	serveMinimalStdioMCP()
	_ = child.Wait()
}

func TestStdioMCPClientInitializeUsesProxyProtocolVersion(t *testing.T) {
	if os.Getenv("TOOLBRIDGE_STDIO_MCP_HELPER") == "1" {
		runStdioMCPTestHelper()
		return
	}

	initFile := filepath.Join(t.TempDir(), "init.json")
	client, err := newStdioMCPClient(context.Background(), providerdto.MCPBinary{
		Name: "version-helper",
		Command: []string{
			os.Args[0],
			"-test.run=^TestStdioMCPClientInitializeUsesProxyProtocolVersion$",
		},
		Env: map[string]string{
			"TOOLBRIDGE_STDIO_MCP_HELPER": "1",
			"TOOLBRIDGE_STDIO_INIT_FILE":  initFile,
		},
	})
	if err != nil {
		t.Fatalf("newStdioMCPClient() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	deadline := time.Now().Add(5 * time.Second)
	var raw []byte
	for time.Now().Before(deadline) {
		raw, err = os.ReadFile(initFile)
		if err == nil && len(raw) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(raw) == 0 {
		t.Fatal("timed out waiting for init.json")
	}
	var req struct {
		Params struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal init.json: %v", err)
	}
	if req.Params.ProtocolVersion != ProxyProtocolVersion {
		t.Fatalf("protocolVersion = %q, want %q (must match HTTP proxy; was hardcoded 2024-11-05)", req.Params.ProtocolVersion, ProxyProtocolVersion)
	}
}

func serveMinimalStdioMCP() {
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := decoder.Decode(&req); err != nil {
			return
		}
		if req.Method == "initialize" {
			if initFile := os.Getenv("TOOLBRIDGE_STDIO_INIT_FILE"); initFile != "" {
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": req.Method, "params": req.Params})
				_ = os.WriteFile(initFile, raw, 0o600)
			}
		}
		switch req.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		case "tools/list":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"tools": []any{}}})
		case "tools/call":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"content": []any{}}})
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
		}
	}
}

func runStdioChildTestHelper() {
	select {}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			var pid int
			if _, scanErr := fmt.Sscanf(string(raw), "%d", &pid); scanErr == nil {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pid file %s", path)
	return 0
}

func waitForEnvFile(t *testing.T, path string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			text := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
			if text == "" {
				return nil
			}
			return strings.Split(text, "\n")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for env file %s", path)
	return nil
}

func assertProcessExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !stdioTestProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process %d still alive after stdio MCP client close", pid)
}

func requireEnvKeyAbsent(t *testing.T, env []string, key string) {
	t.Helper()
	if value, ok := envValueInSlice(env, key); ok {
		t.Fatalf("%s leaked with value %q in env %#v", key, value, env)
	}
}

func requireEnvValueInSlice(t *testing.T, env []string, key, want string) {
	t.Helper()
	got, ok := envValueInSlice(env, key)
	if !ok {
		t.Fatalf("%s missing from env %#v", key, env)
	}
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func envValueInSlice(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix), true
		}
	}
	return "", false
}

func withoutEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, item := range env {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			continue
		}
		out = append(out, item)
	}
	return out
}
