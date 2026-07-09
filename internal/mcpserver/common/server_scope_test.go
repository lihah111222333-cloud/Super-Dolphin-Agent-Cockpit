package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestToolCallParamsScopeNormalizesKnownMCPFamilies(t *testing.T) {
	params := ToolCallParams{}
	cases := map[string]string{
		"mcp-lsp":  "lsp",
		"MCP-ORCH": "orch",
		"mcp-ida":  "ida",
		"custom":   "custom",
	}
	for input, want := range cases {
		if got := params.Scope(input).Family; got != want {
			t.Fatalf("Scope(%q).Family = %q, want %q", input, got, want)
		}
	}
}

func mustJSONRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return raw
}

func TestDecodeToolCallParamsIgnoresDeclaredLegacySessionMetadata(t *testing.T) {
	raw := mustJSONRaw(t, map[string]any{
		"name":       "demo_tool",
		"arguments":  map[string]any{"session_id": "argument-session"},
		"sessionId":  "legacy-camel",
		"session_id": "legacy-snake",
	})

	params, err := DecodeToolCallParams(raw)
	if err != nil {
		t.Fatalf("DecodeToolCallParams() error = %v", err)
	}
	if params.Name != "demo_tool" {
		t.Fatalf("Name = %q, want demo_tool", params.Name)
	}
	if !bytes.Contains(params.Arguments, []byte(`"session_id":"argument-session"`)) {
		t.Fatalf("Arguments = %s, want preserved argument session_id", params.Arguments)
	}
}

func TestDecodeToolCallParamsAcceptsStandardMCPMeta(t *testing.T) {
	raw := mustJSONRaw(t, map[string]any{
		"name":      "demo_tool",
		"arguments": map[string]any{"query": "ToolCallParams"},
		"_meta": map[string]any{
			"progressToken": "codex-call-1",
		},
	})

	params, err := DecodeToolCallParams(raw)
	if err != nil {
		t.Fatalf("DecodeToolCallParams() error = %v", err)
	}
	if params.Name != "demo_tool" {
		t.Fatalf("Name = %q, want demo_tool", params.Name)
	}
	if !bytes.Contains(params.Arguments, []byte(`"query":"ToolCallParams"`)) {
		t.Fatalf("Arguments = %s, want preserved tool arguments", params.Arguments)
	}
}

func TestDecodeToolCallParamsRejectsUnknownTopLevelFields(t *testing.T) {
	_, err := DecodeToolCallParams(json.RawMessage(`{"name":"demo_tool","dryRun":true}`))
	if err == nil {
		t.Fatal("DecodeToolCallParams() error = nil, want unknown field rejection")
	}
	if !strings.Contains(err.Error(), `unknown field "dryRun"`) {
		t.Fatalf("DecodeToolCallParams() error = %v, want unknown dryRun field", err)
	}
}

func TestDecodeToolCallParamsRejectsMissingOrBlankName(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"arguments":{}}`),
		json.RawMessage(`{"name":"  ","arguments":{}}`),
	} {
		_, err := DecodeToolCallParams(raw)
		if err == nil {
			t.Fatalf("DecodeToolCallParams(%s) error = nil, want missing name rejection", raw)
		}
		if !strings.Contains(err.Error(), "tool name is required") {
			t.Fatalf("DecodeToolCallParams(%s) error = %v, want tool name required", raw, err)
		}
	}
}

func TestToolCallParamsScopeUsesTrustedWorkspaceRootsMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	extra := filepath.Join(root, "packages", "..", "packages", "api")
	raw := mustJSONRaw(t, map[string]any{
		"name": "demo_tool",
		"arguments": map[string]any{
			"_workspaceRoots": []string{"/forged/arg"},
			"workspaceRoots":  []string{"/forged/public"},
		},
		"_cwd":            root,
		"_workspaceRoots": []string{extra, root, "  "},
	})

	params, err := DecodeToolCallParams(raw)
	if err != nil {
		t.Fatalf("DecodeToolCallParams() error = %v", err)
	}
	got := params.Scope("mcp-lsp").WorkspaceRoots
	want := []string{filepath.Clean(root), filepath.Clean(extra)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceRoots = %#v, want %#v", got, want)
	}
}

func TestToolCallParamsScopeAcceptsSnakeWorkspaceRootsMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	extra := filepath.Join(root, "extra")
	raw := mustJSONRaw(t, map[string]any{
		"name":             "demo_tool",
		"_cwd":             root,
		"_workspace_roots": []string{extra},
	})

	params, err := DecodeToolCallParams(raw)
	if err != nil {
		t.Fatalf("DecodeToolCallParams() error = %v", err)
	}
	got := params.Scope("mcp-lsp").WorkspaceRoots
	want := []string{filepath.Clean(root), filepath.Clean(extra)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceRoots = %#v, want %#v", got, want)
	}
}

func TestToolCallParamsScopeDoesNotTrustRelativePrimaryRoot(t *testing.T) {
	raw := mustJSONRaw(t, map[string]any{
		"name":             "demo_tool",
		"_cwd":             ".",
		"_workspace_roots": []string{"packages/api"},
	})

	params, err := DecodeToolCallParams(raw)
	if err != nil {
		t.Fatalf("DecodeToolCallParams() error = %v", err)
	}
	got := params.Scope("mcp-lsp")
	if got.CWD != "" || len(got.WorkspaceRoots) != 0 {
		t.Fatalf("scope = %#v, want no trusted cwd or workspace roots", got)
	}
}

func TestToolCallParamsScopeDoesNotPromoteAdditionalRootWithoutTrustedPrimary(t *testing.T) {
	extra := filepath.Join(t.TempDir(), "extra")
	for name, payload := range map[string]map[string]any{
		"missing cwd": {
			"name":             "demo_tool",
			"_workspace_roots": []string{extra},
		},
		"relative cwd": {
			"name":             "demo_tool",
			"_cwd":             ".",
			"_workspace_roots": []string{extra},
		},
	} {
		t.Run(name, func(t *testing.T) {
			params, err := DecodeToolCallParams(mustJSONRaw(t, payload))
			if err != nil {
				t.Fatalf("DecodeToolCallParams() error = %v", err)
			}
			got := params.Scope("mcp-lsp")
			if got.CWD != "" || len(got.WorkspaceRoots) != 0 {
				t.Fatalf("scope = %#v, want no trusted cwd or workspace roots", got)
			}
		})
	}
}

func TestToolCallParamsScopeResolvesRelativeAdditionalRootsAgainstPrimaryRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	raw := mustJSONRaw(t, map[string]any{
		"name":             "demo_tool",
		"_cwd":             root,
		"_workspace_roots": []string{"packages/api"},
	})

	params, err := DecodeToolCallParams(raw)
	if err != nil {
		t.Fatalf("DecodeToolCallParams() error = %v", err)
	}
	got := params.Scope("mcp-lsp").WorkspaceRoots
	want := []string{filepath.Clean(root), filepath.Join(filepath.Clean(root), "packages", "api")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WorkspaceRoots = %#v, want %#v", got, want)
	}
}

func TestWorkspaceRootsFromContextStrictFailsFastWhenMissing(t *testing.T) {
	if got, err := WorkspaceRootsFromContextStrict(context.Background()); err == nil {
		t.Fatalf("WorkspaceRootsFromContextStrict() = %#v, nil error; want fail-fast", got)
	}
}

func TestWorkspaceRootForPathFromContextStrictSelectsPrimaryOrLongestContainingRoot(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "repo")
	nested := filepath.Join(root, "packages", "api")
	ctx := WithToolScope(context.Background(), ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{nested, root},
	})

	got, err := WorkspaceRootForPathFromContextStrict(ctx, "go.mod")
	if err != nil {
		t.Fatalf("relative WorkspaceRootForPathFromContextStrict() error = %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("relative root = %q, want %q", got, filepath.Clean(root))
	}

	target := filepath.Join(nested, "main.go")
	got, err = WorkspaceRootForPathFromContextStrict(ctx, target)
	if err != nil {
		t.Fatalf("absolute WorkspaceRootForPathFromContextStrict() error = %v", err)
	}
	if got != filepath.Clean(nested) {
		t.Fatalf("absolute root = %q, want longest containing root %q", got, filepath.Clean(nested))
	}
}

func TestWorkspaceRootForPathFromContextStrictRejectsOutsidePathWithAllowedRoots(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	outside := filepath.Join(t.TempDir(), "outside.go")
	ctx := WithToolScope(context.Background(), ToolScope{CWD: root})

	_, err := WorkspaceRootForPathFromContextStrict(ctx, outside)
	if err == nil {
		t.Fatal("WorkspaceRootForPathFromContextStrict() error = nil, want outside path rejection")
	}
	msg := err.Error()
	if !strings.Contains(msg, filepath.Clean(outside)) || !strings.Contains(msg, filepath.Clean(root)) {
		t.Fatalf("error = %q, want requested path and allowed roots", msg)
	}
}

func TestWorkspaceRootForPathFromContextStrictAllowsCanonicalEquivalentRoot(t *testing.T) {
	tmp := t.TempDir()
	primary := filepath.Join(tmp, "primary")
	realExtra := filepath.Join(tmp, "real-extra")
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatalf("mkdir primary: %v", err)
	}
	if err := os.MkdirAll(realExtra, 0o700); err != nil {
		t.Fatalf("mkdir real extra: %v", err)
	}
	linkedExtra := filepath.Join(tmp, "linked-extra")
	if err := os.Symlink(realExtra, linkedExtra); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ctx := WithToolScope(context.Background(), ToolScope{
		CWD:            primary,
		WorkspaceRoots: []string{linkedExtra},
	})

	got, err := WorkspaceRootForPathFromContextStrict(ctx, realExtra)
	if err != nil {
		t.Fatalf("WorkspaceRootForPathFromContextStrict() error = %v", err)
	}
	if got != filepath.Clean(linkedExtra) {
		t.Fatalf("root = %q, want trusted root spelling %q", got, filepath.Clean(linkedExtra))
	}
}

func decodeJSONRPCOutput(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(bytes.TrimSpace(raw), out); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
}

func decodeToolErrorEnvelopeFromOutput(t *testing.T, raw []byte) ToolErrorEnvelope {
	t.Helper()
	var resp struct {
		Error  *jsonRPCError `json:"error,omitempty"`
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &resp); err != nil {
		t.Fatalf("unmarshal response %s: %v", raw, err)
	}
	if resp.Error != nil {
		t.Fatalf("response has JSON-RPC error %#v, want structured tool result; raw=%s", resp.Error, raw)
	}
	if len(resp.Result.StructuredContent) == 0 {
		t.Fatalf("response missing structuredContent; raw=%s", raw)
	}
	var envelope ToolErrorEnvelope
	if err := json.Unmarshal(resp.Result.StructuredContent, &envelope); err != nil {
		t.Fatalf("unmarshal structuredContent %s: %v", resp.Result.StructuredContent, err)
	}
	return envelope
}

func TestServerToolsCallUsesTrustedTopLevelScope(t *testing.T) {
	trustedRoot := t.TempDir()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "demo_tool",
			"arguments": map[string]any{"agent_id": "evil", "cwd": "/evil"},
			"_agentId":  "trusted-agent",
			"_threadId": "trusted-thread",
			"_callId":   "trusted-call",
			"_cwd":      trustedRoot,
		},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}
	input := bytes.NewBuffer(raw)
	var output bytes.Buffer
	called := false
	provider := trustedTopLevelScopeProvider(t, &called, trustedRoot)

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

func trustedTopLevelScopeProvider(t *testing.T, called *bool, trustedRoot string) captureToolProvider {
	t.Helper()
	return captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		*called = true
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
		if scope.CWD != trustedRoot {
			t.Fatalf("scope cwd = %q, want %q", scope.CWD, trustedRoot)
		}
		if got := WorkspaceRootFromContext(ctx, "/fallback"); got != trustedRoot {
			t.Fatalf("WorkspaceRootFromContext() = %q, want %q", got, trustedRoot)
		}
		if !bytes.Contains(args, []byte(`"agent_id":"evil"`)) || !bytes.Contains(args, []byte(`"cwd":"/evil"`)) {
			t.Fatalf("CallTool() arguments = %s, want original untrusted payload preserved as args only", args)
		}
		return map[string]any{"ok": true}, nil
	}}
}

func TestHTTPDirectPeerRequiresTrustedScopeMetadata(t *testing.T) {
	trustedRoot := t.TempDir()
	called := false
	provider := captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		called = true
		scope, ok := ToolScopeFromContext(ctx)
		if !ok {
			t.Fatal("ToolScopeFromContext() missing trusted metadata for direct HTTP peer")
		}
		if scope.AgentID != "agent-http" || scope.ThreadID != "thread-http" || scope.CallID != "call-http" {
			t.Fatalf("scope = %#v, want trusted direct HTTP metadata", scope)
		}
		if scope.CWD != trustedRoot {
			t.Fatalf("scope.CWD = %q, want %q", scope.CWD, trustedRoot)
		}
		return map[string]any{"isolation_pass": true}, nil
	}}
	server := NewHTTPServer("mcp-lsp", "dev", provider)
	rec := httptest.NewRecorder()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "file",
			"arguments": map[string]any{"cwd": "/forged"},
			"_agentId":  "agent-http",
			"_threadId": "thread-http",
			"_callId":   "call-http",
			"_cwd":      trustedRoot,
		},
	})
	if err != nil {
		t.Fatalf("marshal HTTP tools/call: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("CallTool() was not called")
	}
	if !strings.Contains(rec.Body.String(), `"isolation_pass":true`) {
		t.Fatalf("HTTP body = %s, want isolation pass payload", rec.Body.String())
	}
}

func TestHTTPDirectPeerWithoutTrustedMetadataIsNotMultiAgentIsolationPass(t *testing.T) {
	provider := captureToolProvider{call: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		if scope, ok := ToolScopeFromContext(ctx); ok {
			if scope.AgentID != "" || scope.ThreadID != "" || scope.CallID != "" || scope.CWD != "" {
				t.Fatalf("ToolScopeFromContext() = %#v, want family-only scope without trusted identity/cwd", scope)
			}
		}
		return map[string]any{"isolation_pass": false, "reason": "scope_missing"}, nil
	}}
	server := NewHTTPServer("mcp-lsp", "dev", provider)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"file","arguments":{"agent_id":"forged-agent","cwd":"/forged"}}}`))

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"isolation_pass":false`) || !strings.Contains(rec.Body.String(), `"scope_missing"`) {
		t.Fatalf("HTTP body = %s, want explicit non-isolation result", rec.Body.String())
	}
}

func TestRawJSONRPCInvalidRequestReturnsErrorAndContinues(t *testing.T) {
	input := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		``,
	}, "\n"))
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"code":-32600`)) {
		t.Fatalf("Run() output = %s, want invalid request error", output.String())
	}
	if !bytes.Contains(output.Bytes(), []byte(`"id":2`)) || !bytes.Contains(output.Bytes(), []byte(`"result":{}`)) {
		t.Fatalf("Run() output = %s, want later valid ping response", output.String())
	}
}

func TestRawJSONStreamSyntaxErrorClosesConnection(t *testing.T) {
	input := bytes.NewBufferString(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		``,
	}, "\n"))
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want raw syntax/desync to close connection")
	}
	if bytes.Contains(output.Bytes(), []byte(`"id":2`)) {
		t.Fatalf("Run() output = %s, want no response after raw syntax error", output.String())
	}
}

func TestFramedStdioMalformedFrameStopsConnection(t *testing.T) {
	var input bytes.Buffer
	input.WriteString(framedPayload(`{"jsonrpc":"2.0","id":1,"method":`))
	input.WriteString(framedPayload(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	var output bytes.Buffer

	server := NewServer("test", "dev", NewStdioTransport(&input, &output), testToolProvider{})
	if err := server.Run(context.Background()); err == nil {
		t.Fatal("Run() error = nil, want malformed framed JSON to stop connection")
	}
	if bytes.Contains(output.Bytes(), []byte(`"id":2`)) {
		t.Fatalf("Run() output = %s, want no response after malformed frame", output.String())
	}
}

func TestRollbackKeepsHTTPMCPCompatibility(t *testing.T) {
	server := NewHTTPServer("mcp-lsp", "dev", testToolProvider{})
	addr, err := server.Start(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	resp, err := http.Post("http://"+addr+"/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("POST /mcp error = %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /mcp status = %d body=%s, want 200", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(`"demo_tool"`)) {
		t.Fatalf("POST /mcp body = %s, want tools/list response", body)
	}
}

func framedPayload(payload string) string {
	return "Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n" + payload
}
