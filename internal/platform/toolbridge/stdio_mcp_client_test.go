package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestStdioMCPClientListToolsRejectsMalformedResult(t *testing.T) {
	cases := []struct {
		name    string
		result  string
		wantErr string
	}{
		{name: "missing tools", result: `{}`, wantErr: "tools array is required"},
		{name: "tools not array", result: `{"tools":null}`, wantErr: "tools array is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := newFakeStdioTransport(
				json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":` + tc.result + `}`),
			)
			client := newTestStdioMCPClient(t, transport)

			_, err := client.ListTools(context.Background())
			if err == nil {
				t.Fatalf("ListTools() error = nil, want %s", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ListTools() error = %v, want %s", err, tc.wantErr)
			}
		})
	}
}

func TestStdioMCPClientCallToolForwardsWorkspaceRootsMetadata(t *testing.T) {
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`),
	)
	client := newTestStdioMCPClient(t, transport)

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
	writes := transport.Writes()
	if len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
	write, ok := writes[0].(map[string]any)
	if !ok {
		t.Fatalf("write type = %T, want map[string]any", writes[0])
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
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"success\":false,\"code\":\"path_outside_workspace\"}"}],"isError":true,"structuredContent":{"success":false,"code":"path_outside_workspace"}}}`),
	)
	client := newTestStdioMCPClient(t, transport)

	got, err := client.CallTool(context.Background(), "file", json.RawMessage(`{}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false", got)
	}
}

func TestStdioMCPClientCallToolConvertsJSONRPCErrorToToolResult(t *testing.T) {
	const marker = "token=sk-test-secret dsn=postgres://alice:secret@db/private path=/Users/private/secret.go"
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"` + marker + `"}}`),
	)
	client := newTestStdioMCPClient(t, transport)

	got, err := client.CallTool(context.Background(), "launch_agent", json.RawMessage(`{"name":"worker"}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v, want tool failure result", err)
	}
	if got == nil || got.Success {
		t.Fatalf("CallTool() success = %#v, want false", got)
	}
	if len(got.ContentItems) != 1 || strings.Contains(got.ContentItems[0].Text, marker) || !strings.HasPrefix(got.ContentItems[0].Text, "Tool execution failed. Diagnostic ID: ") {
		t.Fatalf("CallTool() content = %#v, want public diagnostic error without marker", got.ContentItems)
	}
}

func TestStdioMCPClientCallToolCarriesTrustedTopLevelMetaWithoutStructuredContent(t *testing.T) {
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ERROR code=authorization_required retryable=0"}],"isError":true,"_meta":{"authorization_required":true,"windows_error_code":5,"windows_permission_kind":"access_denied"}}}`),
	)
	client := newTestStdioMCPClient(t, transport)
	got, err := client.CallTool(context.Background(), "file", json.RawMessage(`{}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got == nil || got.Success || len(got.ContentItems) != 1 {
		t.Fatalf("CallTool() result = %#v, want failed text result", got)
	}
	if len(got.StructuredContent) != 0 {
		t.Fatalf("CallTool() restored structuredContent: %s", got.StructuredContent)
	}
	var meta map[string]any
	if err := json.Unmarshal(got.ContentItems[0].Meta, &meta); err != nil {
		t.Fatalf("content _meta = %s: %v", got.ContentItems[0].Meta, err)
	}
	if meta["authorization_required"] != true || meta["windows_error_code"] != float64(5) || meta["windows_permission_kind"] != "access_denied" {
		t.Fatalf("content _meta = %#v, want ACL metadata", meta)
	}
}

func TestStdioMCPClientCallToolCarriesProtocolErrorDataMetadata(t *testing.T) {
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"permission denied at C:\\private\\secret.go","data":{"meta":{"authorization_required":true,"windows_error_code":1314,"windows_permission_kind":"privilege_not_held"}}}}`),
	)
	client := newTestStdioMCPClient(t, transport)
	got, err := client.CallTool(context.Background(), "file", json.RawMessage(`{}`), ToolCallRequest{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got == nil || got.Success || len(got.ContentItems) != 1 {
		t.Fatalf("CallTool() result = %#v, want failed text result", got)
	}
	if strings.Contains(got.ContentItems[0].Text, "secret.go") {
		t.Fatalf("protocol error leaked user-visible path: %q", got.ContentItems[0].Text)
	}
	var meta map[string]any
	if err := json.Unmarshal(got.ContentItems[0].Meta, &meta); err != nil {
		t.Fatalf("protocol error _meta = %s: %v", got.ContentItems[0].Meta, err)
	}
	if meta["authorization_required"] != true || meta["windows_error_code"] != float64(1314) || meta["windows_permission_kind"] != "privilege_not_held" {
		t.Fatalf("protocol error _meta = %#v, want ACL metadata", meta)
	}
}

func TestStdioMCPClientCallToolRejectsNullSuccessPayload(t *testing.T) {
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"null"}],"structuredContent":{}}}`),
	)
	client := newTestStdioMCPClient(t, transport)

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
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"blob":"` + strings.Repeat("x", 1048577) + `"}}`),
	)
	client := newTestStdioMCPClient(t, transport)

	_, err := client.request(context.Background(), "tools/list", map[string]any{})
	if err == nil {
		t.Fatal("request() error = nil, want oversize peer response error")
	}
	if !strings.Contains(err.Error(), "exceeds stdio message limit") {
		t.Fatalf("request() error = %v, want stdio message limit", err)
	}
}

func TestStdioMCPClientCancellationPublishesReason(t *testing.T) {
	transport := newFakeStdioTransport()
	client := newTestStdioMCPClient(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan stdioRequestOutcome, 1)
	var requestWG sync.WaitGroup
	requestWG.Go(func() {
		raw, err := client.request(ctx, "tools/list", map[string]any{})
		result <- stdioRequestOutcome{raw: raw, err: err}
	})
	t.Cleanup(requestWG.Wait)

	request := waitForStdioWrites(t, transport, 1)[0]
	requestID := stdioWriteID(t, request)
	cancel()
	if err := waitForStdioOutcome(t, result).err; err != context.Canceled {
		t.Fatalf("request() error = %v, want context canceled", err)
	}
	cancellation := waitForStdioWrites(t, transport, 2)[1]
	assertStdioCancellationWithReason(t, cancellation, requestID, context.Canceled.Error())
}

func TestDefaultStdioClientFactoryRejectsUntrustedRuntimeCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h := &Handler{}

	_, err := h.defaultStdioClientFactory(ctx, providerdto.MCPBinary{
		Name:    "shell",
		Command: []string{os.Args[0], "-test.run=^$"},
	})
	if err == nil {
		t.Fatal("defaultStdioClientFactory() error = nil, want untrusted runtime command rejection")
	}
	if !strings.Contains(err.Error(), "trusted server id") {
		t.Fatalf("defaultStdioClientFactory() error = %v, want trusted server id rejection", err)
	}
}

// TestDefaultStdioClientFactoryRejectsTrustedUnsafeRuntimeCommand 是 exec.Command 前的最后一道防线，
// 确认可信来源标记不能放行危险 stdio argv。
func TestDefaultStdioClientFactoryRejectsTrustedUnsafeRuntimeCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h := &Handler{}

	_, err := h.defaultStdioClientFactory(ctx, providerdto.MCPBinary{
		Name:            "shell",
		TrustedServerID: "shell",
		Command:         []string{os.Args[0], "-test.run=^$"},
	})
	if err == nil {
		t.Fatal("defaultStdioClientFactory() error = nil, want unsafe trusted runtime command rejection")
	}
	if !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("defaultStdioClientFactory() error = %v, want unsupported stdio command", err)
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
	client, err := newStdioMCPClientForValidatedBinary(context.Background(), providerdto.MCPBinary{
		Name:            "helper",
		TrustedServerID: "helper",
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
		t.Fatalf("newStdioMCPClientForValidatedBinary() error = %v", err)
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
	t.Setenv("OPENAI_API_KEY", "parent-openai-secret")
	t.Setenv("ANTHROPIC_API_KEY", "parent-anthropic-secret")

	envPath := filepath.Join(t.TempDir(), "stdio-env.txt")
	client, err := newStdioMCPClientForValidatedBinary(context.Background(), providerdto.MCPBinary{
		Name:            "env-helper",
		TrustedServerID: "env-helper",
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
		t.Fatalf("newStdioMCPClientForValidatedBinary() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	env := waitForEnvFile(t, envPath)
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"} {
		requireEnvKeyAbsent(t, env, key)
	}
	for _, key := range []string{"TOOLBRIDGE_SAFE_PARENT", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		requireEnvKeyAbsent(t, env, key)
	}
	requireEnvValueInSlice(t, env, "TOOLBRIDGE_SAFE_MANIFEST", "keep-manifest")
}

type stdioRequestOutcome struct {
	raw json.RawMessage
	err error
}

type fakeStdioRead struct {
	raw json.RawMessage
	err error
}

type fakeStdioTransport struct {
	readCh    chan fakeStdioRead
	closedCh  chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	writes    []any
	closed    bool
}

func newFakeStdioTransport(reads ...json.RawMessage) *fakeStdioTransport {
	transport := &fakeStdioTransport{
		readCh:   make(chan fakeStdioRead, 128),
		closedCh: make(chan struct{}),
	}
	for _, raw := range reads {
		transport.Enqueue(raw)
	}
	return transport
}

func newTestStdioMCPClient(t *testing.T, transport stdioTransport) *stdioMCPClient {
	t.Helper()
	client := &stdioMCPClient{
		transport: transport,
		pending:   make(map[int64]chan stdioRequestResult),
		closed:    make(chan struct{}),
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return client
}

func (t *fakeStdioTransport) ReadMessage() (json.RawMessage, error) {
	select {
	case next := <-t.readCh:
		return next.raw, next.err
	case <-t.closedCh:
		return nil, io.ErrClosedPipe
	}
}

func (t *fakeStdioTransport) WriteMessage(payload any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return io.ErrClosedPipe
	}
	t.writes = append(t.writes, payload)
	return nil
}

func (t *fakeStdioTransport) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closed = true
		close(t.closedCh)
		t.mu.Unlock()
	})
	return nil
}

func (t *fakeStdioTransport) Enqueue(raw json.RawMessage) {
	t.readCh <- fakeStdioRead{raw: raw}
}

func (t *fakeStdioTransport) EnqueueError(err error) {
	t.readCh <- fakeStdioRead{err: err}
}

func (t *fakeStdioTransport) Writes() []any {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]any(nil), t.writes...)
}

func (t *fakeStdioTransport) Closed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func waitForStdioWrites(t *testing.T, transport *fakeStdioTransport, count int) []any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		writes := transport.Writes()
		if len(writes) >= count {
			return writes
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d stdio writes; got %d", count, len(transport.Writes()))
	return nil
}

func waitForStdioOutcome(t *testing.T, outcomes <-chan stdioRequestOutcome) stdioRequestOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stdio request outcome")
		return stdioRequestOutcome{}
	}
}

func waitForStdioTransportClosed(t *testing.T, transport *fakeStdioTransport) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if transport.Closed() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for stdio transport close")
}

func stdioWriteID(t *testing.T, payload any) int64 {
	t.Helper()
	write, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("stdio write type = %T, want map[string]any", payload)
	}
	id, ok := write["id"].(int64)
	if !ok || id <= 0 {
		t.Fatalf("stdio write id = %#v, want positive int64", write["id"])
	}
	return id
}

func stdioRequestIDsByMethod(t *testing.T, writes []any) map[string]int64 {
	t.Helper()
	requestIDs := make(map[string]int64, len(writes))
	for _, payload := range writes {
		write, ok := payload.(map[string]any)
		if !ok {
			t.Fatalf("stdio write type = %T, want map[string]any", payload)
		}
		method, ok := write["method"].(string)
		if !ok || method == "" {
			t.Fatalf("stdio write method = %#v, want non-empty string", write["method"])
		}
		requestIDs[method] = stdioWriteID(t, payload)
	}
	return requestIDs
}

func assertStdioCancellation(t *testing.T, payload any, wantID int64) {
	t.Helper()
	write, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("stdio cancellation type = %T, want map[string]any", payload)
	}
	if method := write["method"]; method != "notifications/cancelled" {
		t.Fatalf("stdio cancellation method = %#v, want notifications/cancelled", method)
	}
	params, ok := write["params"].(map[string]any)
	if !ok {
		t.Fatalf("stdio cancellation params = %#v, want map[string]any", write["params"])
	}
	if requestID := params["requestId"]; requestID != wantID {
		t.Fatalf("stdio cancellation requestId = %#v, want %d", requestID, wantID)
	}
}

func assertStdioCancellationWithReason(t *testing.T, payload any, wantID int64, wantReason string) {
	t.Helper()
	assertStdioCancellation(t, payload, wantID)
	write := payload.(map[string]any)
	params := write["params"].(map[string]any)
	if reason := params["reason"]; reason != wantReason {
		t.Fatalf("stdio cancellation reason = %#v, want %q", reason, wantReason)
	}
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
	if err := os.WriteFile(marker, fmt.Appendf(nil, "%d", child.Process.Pid), 0o644); err != nil {
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
	client, err := newStdioMCPClientForValidatedBinary(context.Background(), providerdto.MCPBinary{
		Name:            "version-helper",
		TrustedServerID: "version-helper",
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
		t.Fatalf("newStdioMCPClientForValidatedBinary() error = %v", err)
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

func TestNewStdioMCPClientRejectsUnsupportedInitializeProtocolVersion(t *testing.T) {
	if os.Getenv("TOOLBRIDGE_STDIO_MCP_HELPER") == "1" {
		runStdioMCPTestHelper()
		return
	}

	_, err := newStdioMCPClientForValidatedBinary(context.Background(), providerdto.MCPBinary{
		Name:            "unsupported-version-helper",
		TrustedServerID: "unsupported-version-helper",
		Command: []string{
			os.Args[0],
			"-test.run=^TestNewStdioMCPClientRejectsUnsupportedInitializeProtocolVersion$",
		},
		Env: map[string]string{
			"TOOLBRIDGE_STDIO_MCP_HELPER":       "1",
			"TOOLBRIDGE_STDIO_PROTOCOL_VERSION": "2099-01-01",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "is not supported") {
		t.Fatalf("newStdioMCPClientForValidatedBinary() error = %v, want unsupported protocol version", err)
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
			protocolVersion := os.Getenv("TOOLBRIDGE_STDIO_PROTOCOL_VERSION")
			if protocolVersion == "" {
				protocolVersion = ProxyProtocolVersion
			}
			_ = encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": protocolVersion,
				},
			})
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
		if value, ok := strings.CutPrefix(item, prefix); ok {
			return value, true
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
