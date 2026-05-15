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

type captureToolProvider struct {
	call func(context.Context, string, json.RawMessage) (any, error)
}

func (p captureToolProvider) ListTools(context.Context) ([]MCPTool, error) {
	return nil, nil
}

func (p captureToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return p.call(ctx, name, args)
}

func TestServerHandlesToolsList(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"tools":[{"name":"demo_tool"`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
}

func TestServerHandlesToolsCall(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"demo_tool","arguments":{}}}`)
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"content":[{"type":"text","text":"{\"ok\":true}"`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
}

func TestServerToolsCallUsesTrustedTopLevelScope(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"demo_tool","arguments":{"agent_id":"evil","cwd":"/evil"},"_agentId":"trusted-agent","_threadId":"trusted-thread","_callId":"trusted-call","_cwd":"/trusted/root"}}`)
	var output bytes.Buffer
	called := false
	provider := captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		called = true
		if name != "demo_tool" {
			t.Fatalf("CallTool() name = %q, want demo_tool", name)
		}
		scope, ok := ToolScopeFromContext(ctx)
		if !ok {
			t.Fatal("ToolScopeFromContext() missing trusted scope")
		}
		if scope.AgentID != "trusted-agent" || scope.ThreadID != "trusted-thread" || scope.CallID != "trusted-call" {
			t.Fatalf("scope identity = %#v, want trusted top-level metadata", scope)
		}
		if scope.CWD != "/trusted/root" {
			t.Fatalf("scope cwd = %q, want /trusted/root", scope.CWD)
		}
		if got := WorkspaceRootFromContext(ctx, "/fallback"); got != "/trusted/root" {
			t.Fatalf("WorkspaceRootFromContext() = %q, want /trusted/root", got)
		}
		if !bytes.Contains(args, []byte(`"agent_id":"evil"`)) || !bytes.Contains(args, []byte(`"cwd":"/evil"`)) {
			t.Fatalf("CallTool() arguments = %s, want original untrusted payload preserved as args only", args)
		}
		return map[string]any{"ok": true}, nil
	}}

	server := NewServer("test", "dev", NewStdioTransport(input, &output), provider)
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !called {
		t.Fatal("CallTool() was not called")
	}
	if !bytes.Contains(output.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
}
