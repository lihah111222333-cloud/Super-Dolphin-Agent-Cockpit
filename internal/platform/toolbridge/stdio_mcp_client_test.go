package toolbridge

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
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

type fakeStdioTransport struct {
	reads  []json.RawMessage
	writes []any
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
