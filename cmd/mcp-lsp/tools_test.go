package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/stretchr/testify/require"
)

func TestLSPToolManifestsExposeVisibleLegacyNames(t *testing.T) {
	got := make([]string, 0, len(lspToolManifests))
	for _, manifest := range lspToolManifests {
		got = append(got, manifest.Name)
	}
	want := []string{"lsp_file", "lsp_inspect", "lsp_xref", "lsp_grep", "lsp_structure", "lsp_edit", "lsp_completion", "code_run"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest names = %#v, want %#v", got, want)
	}
}

func TestToolsListExposesLegacyLSPNames(t *testing.T) {
	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}
	list, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := make(map[string]bool, len(list))
	for _, tool := range list {
		got[tool.Name] = true
	}
	for _, want := range []string{"lsp_file", "lsp_inspect", "lsp_xref", "lsp_grep", "lsp_structure", "lsp_edit", "lsp_completion"} {
		if !got[want] {
			t.Fatalf("tools/list missing visible legacy tool %q; got %#v", want, got)
		}
	}
	for _, hiddenShort := range []string{"file", "inspect", "xref", "grep", "structure", "edit", "completion"} {
		if got[hiddenShort] {
			t.Fatalf("tools/list exposed hidden short alias %q; got %#v", hiddenShort, got)
		}
	}
}

func TestToolsListKeepsCodeRunHelpersVisible(t *testing.T) {
	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}
	list, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := make(map[string]bool, len(list))
	for _, tool := range list {
		got[tool.Name] = true
	}
	for _, want := range []string{"code_run"} {
		if !got[want] {
			t.Fatalf("tools/list missing execution helper %q; got %#v", want, got)
		}
	}
}

func TestHandleToolCallAcceptsLegacyLSPAlias(t *testing.T) {
	defs := toolDefinitions(ToolHandlers{
		"file": func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})

	result, err := handleToolCall(context.Background(), defs, "lsp_file", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handleToolCall(lsp_file) error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok || payload["ok"] != true {
		t.Fatalf("handleToolCall(lsp_file) result = %#v, want ok payload", result)
	}
}

func TestToolsCallAcceptsShortAndLegacyLSPNames(t *testing.T) {
	defs := toolDefinitions(ToolHandlers{
		"file": func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"tool": "file"}, nil
		},
	})
	for _, name := range []string{"file", "lsp_file"} {
		result, err := handleToolCall(context.Background(), defs, name, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("handleToolCall(%q) error = %v", name, err)
		}
		payload, ok := result.(map[string]any)
		if !ok || payload["tool"] != "file" {
			t.Fatalf("handleToolCall(%q) result = %#v, want file payload", name, result)
		}
	}
}

func TestHandleScopedToolsCallUsesTrustedScope(t *testing.T) {
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			scope, ok := common.ToolScopeFromContext(ctx)
			if !ok {
				t.Fatal("ToolScopeFromContext() missing scope")
			}
			if scope.AgentID != "agent-1" || scope.ThreadID != "thread-1" || scope.CallID != "call-1" {
				t.Fatalf("scope = %#v, want trusted identity", scope)
			}
			if scope.CWD != "/trusted/lsp" {
				t.Fatalf("scope cwd = %q, want /trusted/lsp", scope.CWD)
			}
			if !json.Valid(args) {
				t.Fatalf("args is not valid json: %s", args)
			}
			return map[string]any{"ok": true}, nil
		},
	}}
	params := json.RawMessage(`{"name":"file","arguments":{"agent_id":"evil","cwd":"/evil"},"_agentId":"agent-1","_threadId":"thread-1","_callId":"call-1","_cwd":"/trusted/lsp"}`)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok || payload["structuredContent"] == nil {
		t.Fatalf("handleScopedToolsCall() result = %#v, want structured content", result)
	}
}

func TestLSPOnToolsCallInjectsScopeContext(t *testing.T) {
	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			called = true
			scope := requireToolScope(t, ctx)
			assertTrustedToolScopeContext(t, ctx, scope)
			assertToolScopeHasNoSessionID(t)
			payload := decodeScopedToolCallPayload(t, args)
			assertForgedToolArgumentsPreserved(t, scope, payload)
			return map[string]any{"ok": true}, nil
		},
	}}
	params := json.RawMessage(`{"name":"file","arguments":{"agent_id":"forged-agent","thread_id":"forged-thread","cwd":"/forged/root","session_id":"forged-session"},"_agentId":"trusted-agent","_threadId":"trusted-thread","_callId":"trusted-call","_cwd":"/trusted/lsp","sessionId":"top-level-forged-session","session_id":"top-level-forged-session"}`)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	require.True(t, called, "handler was not called")
	assertStructuredToolResult(t, result)
}

type scopedToolCallPayload struct {
	FilePath  string `json:"file_path"`
	AgentID   string `json:"agent_id"`
	ThreadID  string `json:"thread_id"`
	CWD       string `json:"cwd"`
	SessionID string `json:"session_id"`
}

func requireToolScope(t *testing.T, ctx context.Context) common.ToolScope {
	t.Helper()
	scope, ok := common.ToolScopeFromContext(ctx)
	require.True(t, ok, "ToolScopeFromContext() missing scope")
	return scope
}

func assertTrustedToolScopeContext(t *testing.T, ctx context.Context, scope common.ToolScope) {
	t.Helper()
	require.Equal(t, "trusted-agent", scope.AgentID)
	require.Equal(t, "trusted-thread", scope.ThreadID)
	require.Equal(t, "trusted-call", scope.CallID)
	require.Equal(t, "/trusted/lsp", scope.CWD)
	require.Equal(t, "/trusted/lsp", common.WorkspaceRootFromContext(ctx, "/fallback"))
}

func assertToolScopeHasNoSessionID(t *testing.T) {
	t.Helper()
	_, scopeHasSession := reflect.TypeOf(common.ToolScope{}).FieldByName("SessionID")
	require.False(t, scopeHasSession, "common.ToolScope unexpectedly exposes SessionID")
	_, paramsHasSession := reflect.TypeOf(common.ToolCallParams{}).FieldByName("SessionID")
	require.False(t, paramsHasSession, "common.ToolCallParams unexpectedly exposes SessionID")
}

func decodeScopedToolCallPayload(t *testing.T, args json.RawMessage) scopedToolCallPayload {
	t.Helper()
	var payload scopedToolCallPayload
	require.NoError(t, json.Unmarshal(args, &payload))
	return payload
}

func assertForgedToolArgumentsPreserved(t *testing.T, scope common.ToolScope, payload scopedToolCallPayload) {
	t.Helper()
	require.Equal(t, "forged-agent", payload.AgentID)
	require.Equal(t, "forged-thread", payload.ThreadID)
	require.Equal(t, "/forged/root", payload.CWD)
	require.Equal(t, "forged-session", payload.SessionID)
	require.NotEqual(t, scope.AgentID, payload.AgentID)
	require.NotEqual(t, scope.ThreadID, payload.ThreadID)
	require.NotEqual(t, scope.CWD, payload.CWD)
}

func assertStructuredToolResult(t *testing.T, result any) {
	t.Helper()
	payload, ok := result.(map[string]any)
	require.Truef(t, ok, "tool result type = %T, want map[string]any", result)
	require.NotNilf(t, payload["structuredContent"], "tool result = %#v, want structured content", result)
}

func TestHandleScopedToolsCallRoutesTrustedScopeToManagerPool(t *testing.T) {
	trustedRoot, evilRoot := setupTrustedAndEvilToolRoots(t)
	registry := newToolTestRegistry(t, evilRoot)

	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			payload := decodeScopedToolCallPayload(t, args)
			assertManagerPoolForgedPayload(t, payload, evilRoot)
			scoped, err := registry.ResolveManagerForFile(ctx, payload.FilePath)
			require.NoError(t, err)
			resolved := scoped.ResolvedScope
			assertTrustedManagerPoolScope(t, resolved, trustedRoot, evilRoot)
			_, err = scoped.Manager.Diagnostics(ctx, nil)
			require.NoError(t, err)
			return map[string]any{"manager_key": resolved.ManagerKey}, nil
		},
	}}
	params, err := json.Marshal(map[string]any{
		"name":      "file",
		"arguments": map[string]any{"file_path": "main.go", "agent_id": "agent-forged", "cwd": evilRoot},
		"_agentId":  "agent-trusted",
		"_threadId": "thread-trusted",
		"_callId":   "call-trusted",
		"_cwd":      trustedRoot,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	_, err = handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
}

func TestDirectStdioServerMcpLSPFamilyRoutesTrustedScopeToManagerPool(t *testing.T) {
	trustedRoot, evilRoot := setupTrustedAndEvilToolRoots(t)
	registry := newToolTestRegistry(t, evilRoot)

	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			called = true
			payload := decodeScopedToolCallPayload(t, args)
			assertManagerPoolForgedPayload(t, payload, evilRoot)
			scoped, err := registry.ResolveManagerForFile(ctx, payload.FilePath)
			require.NoError(t, err)
			resolved := scoped.ResolvedScope
			assertTrustedManagerPoolScope(t, resolved, trustedRoot, evilRoot)
			require.Equal(t, "lsp", resolved.Family)
			require.NotContains(t, resolved.ManagerKey, "mcp-lsp")
			_, err = scoped.Manager.Diagnostics(ctx, nil)
			require.NoError(t, err)
			return map[string]any{"ok": true}, nil
		},
	}}
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "file",
			"arguments": map[string]any{"file_path": "main.go", "agent_id": "agent-forged", "cwd": evilRoot},
			"_agentId":  "agent-trusted",
			"_threadId": "thread-trusted",
			"_callId":   "call-trusted",
			"_cwd":      trustedRoot,
		},
	})
	require.NoError(t, err)
	var output bytes.Buffer
	server := common.NewServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs})
	require.NoError(t, server.Run(context.Background()))
	require.True(t, called, "direct stdio tools/call did not reach handler")
	require.NotContains(t, output.String(), "unsupported LSP scope family")
	assertDirectToolOutputOK(t, output.Bytes())
}

func setupTrustedAndEvilToolRoots(t *testing.T) (trustedRoot string, evilRoot string) {
	t.Helper()
	trustedRoot = canonicalToolTestRoot(t, t.TempDir())
	evilRoot = canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(trustedRoot, "go.mod"), "module example.test/trusted\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(trustedRoot, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(evilRoot, "go.mod"), "module example.test/evil\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(evilRoot, "main.go"), "package evil\n")
	return trustedRoot, evilRoot
}

type toolTestScopedRegistry interface {
	ResolveManagerForFile(context.Context, string) (lspmanager.ScopedManager, error)
}

func newToolTestRegistry(t *testing.T, evilRoot string) toolTestScopedRegistry {
	t.Helper()
	registry := lspmanager.NewRegistry(nil)
	mgr := multilsp.NewManager(multilsp.Config{WorkspaceRoot: evilRoot})
	t.Cleanup(func() {
		require.NoError(t, mgr.Close())
	})
	registry.Register("go", mgr, multilsp.NewRegistryScopedResolver(mgr))
	return registry
}

func assertManagerPoolForgedPayload(t *testing.T, payload scopedToolCallPayload, evilRoot string) {
	t.Helper()
	require.Equal(t, "agent-forged", payload.AgentID)
	require.Equal(t, evilRoot, payload.CWD)
}

func assertTrustedManagerPoolScope(t *testing.T, resolved lspmanager.ResolvedToolScope, trustedRoot, evilRoot string) {
	t.Helper()
	require.Equal(t, "agent-trusted", resolved.AgentID)
	require.Equal(t, "thread-trusted", resolved.ThreadID)
	require.Equal(t, trustedRoot, resolved.CWD)
	require.Equal(t, filepath.Join(trustedRoot, "main.go"), resolved.TargetPath)
	require.NotContains(t, resolved.ManagerKey, "agent-forged")
	require.NotContains(t, resolved.ManagerKey, evilRoot)
}

func assertDirectToolOutputOK(t *testing.T, raw []byte) {
	t.Helper()
	if bytes.Contains(raw, []byte(`"ok":true`)) {
		return
	}
	if bytes.Contains(raw, []byte(`"ok\":true`)) {
		return
	}
	t.Fatalf("Run() output = %s", string(raw))
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func canonicalToolTestRoot(t *testing.T, root string) string {
	t.Helper()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return realRoot
}

func TestEditSchemaExposesRuntimeFields(t *testing.T) {
	props, ok := lspEditSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("edit schema properties type = %T", lspEditSchema["properties"])
	}
	for _, field := range []string{"persist_to_disk", "version"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing runtime field %q", field)
		}
	}
}

func TestEditSchemaIncludesForce(t *testing.T) {
	props, ok := lspEditSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("edit schema properties type = %T", lspEditSchema["properties"])
	}
	force, ok := props["force"].(map[string]any)
	if !ok {
		t.Fatalf("edit schema missing boolean force property; props=%#v", props)
	}
	if force["type"] != "boolean" {
		t.Fatalf("force schema type = %#v, want boolean", force["type"])
	}
}

func TestStructureSchemaExposesLegacyPathAlias(t *testing.T) {
	props, ok := lspStructureSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("structure schema properties type = %T", lspStructureSchema["properties"])
	}
	if _, ok := props["path"]; !ok {
		t.Fatalf("structure schema missing legacy path alias")
	}
}
