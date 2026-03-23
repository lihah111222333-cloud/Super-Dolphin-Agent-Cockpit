package common

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

type testToolProvider struct{}

func (testToolProvider) ListTools(context.Context) ([]MCPTool, error) {
	return []MCPTool{{Name: "demo_tool", InputSchema: json.RawMessage(`{}`)}}, nil
}

func (testToolProvider) CallTool(context.Context, string, json.RawMessage) (any, error) {
	return map[string]any{"ok": true}, nil
}

func TestServerHandlesToolsList(t *testing.T) {
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var out bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(in, &out), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"tools":[{"name":"demo_tool"`)) {
		t.Fatalf("Run() output = %s", out.String())
	}
}

func TestServerHandlesToolsCall(t *testing.T) {
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`)
	var out bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(in, &out), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"content":[{"type":"text","text":"{\"ok\":true}"`)) {
		t.Fatalf("Run() output = %s", out.String())
	}
}
