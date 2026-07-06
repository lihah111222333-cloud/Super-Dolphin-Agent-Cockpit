package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	lsptools "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/stretchr/testify/require"
)

func TestLSPToolManifestsExposeShortNames(t *testing.T) {
	got := make([]string, 0, len(lspToolManifests))
	for _, manifest := range lspToolManifests {
		got = append(got, manifest.Name)
	}
	want := []string{"file", "inspect", "xref", "grep", "structure", "edit", "completion"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest names = %#v, want %#v", got, want)
	}
}

func TestToolsListExposesShortLSPNamesWhenSemanticLSPIsAvailable(t *testing.T) {
	provider := registryToolProvider{
		defs: toolDefinitions(ToolHandlers{}),
		semanticToolsAvailable: func(context.Context) bool {
			return true
		},
	}
	list, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := make(map[string]bool, len(list))
	for _, tool := range list {
		got[tool.Name] = true
	}
	for _, want := range []string{"file", "inspect", "xref", "grep", "structure", "edit", "completion"} {
		if !got[want] {
			t.Fatalf("tools/list missing short tool %q; got %#v", want, got)
		}
	}
	for _, legacy := range []string{"lsp_file", "lsp_inspect", "lsp_xref", "lsp_grep", "lsp_structure", "lsp_edit", "lsp_completion"} {
		if got[legacy] {
			t.Fatalf("tools/list exposed legacy alias %q; got %#v", legacy, got)
		}
	}
}

func TestToolsListHidesSemanticLSPToolsWhenLanguageServersUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}
	list, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	got := make(map[string]bool, len(list))
	for _, tool := range list {
		got[tool.Name] = true
	}
	for _, hidden := range []string{"inspect", "xref", "structure", "edit", "completion"} {
		if got[hidden] {
			t.Fatalf("tools/list exposed semantic LSP tool %q without a language server; got %#v", hidden, got)
		}
	}
	for _, want := range []string{"file", "grep"} {
		if !got[want] {
			t.Fatalf("tools/list missing non-semantic helper %q; got %#v", want, got)
		}
	}
}

func TestRuntimeSemanticLSPBinariesDerivedFromAdapters(t *testing.T) {
	got, err := runtimeSemanticLSPServerBinaries()
	if err != nil {
		t.Fatalf("runtimeSemanticLSPServerBinaries() error = %v", err)
	}
	want := []string{
		"gopls",
		"typescript-language-server",
		"pyright-langserver",
		"vscode-css-language-server",
		"rust-analyzer",
		"jdtls",
		"bash-language-server",
		"sql-language-server",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtimeSemanticLSPServerBinaries() = %#v, want adapter-derived %#v", got, want)
	}
}

func TestToolsListPackagedAvailabilityFailsFastInsteadOfUsingSystemOnlyLanguageServers(t *testing.T) {
	systemBin := t.TempDir()
	writeMcpLSPExecutable(t, systemBin, "gopls")
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	t.Setenv("PATH", systemBin)
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", manifest)
	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}

	_, err := provider.ListTools(context.Background())
	if err == nil {
		t.Fatal("ListTools() error = nil, want packaged manifest failure before system PATH lookup")
	}
	if !strings.Contains(err.Error(), manifest) {
		t.Fatalf("ListTools() error = %v, want manifest path %q", err, manifest)
	}
}

func TestToolsListPackagedStandardBundleExposesSemanticToolsWithoutJDTLS(t *testing.T) {
	bundle := t.TempDir()
	writeMcpLSPBundleManifest(t, bundle, `{
  "servers": {
    "gopls": {"path": "bin/gopls", "languages": ["go", "gomod", "gosum", "gowork"]},
    "typescript-language-server": {"path": "bin/typescript-language-server", "languages": ["javascript", "javascriptreact", "typescript", "typescriptreact"]},
    "vscode-langservers-extracted": {"path": "bin/vscode-css-language-server", "languages": ["css"]},
    "pyright": {"path": "bin/pyright-langserver", "languages": ["python"]},
    "rust-analyzer": {"path": "bin/rust-analyzer", "languages": ["rust"]},
    "bash-language-server": {"path": "bin/bash-language-server", "languages": ["shellscript"]},
    "sql-language-server": {"path": "bin/sql-language-server", "languages": ["sql"]}
  }
}
`)
	for _, name := range []string{
		"gopls",
		"typescript-language-server",
		"vscode-css-language-server",
		"pyright-langserver",
		"rust-analyzer",
		"bash-language-server",
		"sql-language-server",
	} {
		writeMcpLSPExecutable(t, filepath.Join(bundle, "bin"), name)
	}
	if _, err := os.Stat(filepath.Join(bundle, "bin", "jdtls")); !os.IsNotExist(err) {
		t.Fatalf("standard packaged fixture unexpectedly contains jdtls: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", bundle)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", filepath.Join(bundle, "manifest.json"))

	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}
	list, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	got := make(map[string]bool, len(list))
	for _, tool := range list {
		got[tool.Name] = true
	}
	for _, want := range []string{"file", "inspect", "xref", "grep", "structure", "edit", "completion"} {
		if !got[want] {
			t.Fatalf("tools/list missing %q for standard packaged bundle; got %#v", want, got)
		}
	}
}

func TestToolsListPackagedInvalidManifestFailsFast(t *testing.T) {
	systemBin := t.TempDir()
	writeMcpLSPExecutable(t, systemBin, "gopls")
	bundle := t.TempDir()
	manifest := filepath.Join(bundle, "missing-manifest.json")
	t.Setenv("PATH", systemBin)
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", bundle)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", manifest)
	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}

	_, err := provider.ListTools(context.Background())
	if err == nil {
		t.Fatal("ListTools() error = nil, want invalid packaged manifest failure")
	}
	for _, want := range []string{"missing bundled LSP manifest", manifest} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ListTools() error = %v, want substring %q", err, want)
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

func TestHandleScopedToolsCallSetsIsErrorForToolEnvelope(t *testing.T) {
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, common.NewCodedToolError("path_outside_workspace", errors.New("outside"), false, "stay inside roots")
		},
	}}
	params := json.RawMessage(`{"name":"file","arguments":{}}`)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("handleScopedToolsCall() result = %T, want map", result)
	}
	if payload["isError"] != true {
		t.Fatalf("isError = %#v, want true; result=%#v", payload["isError"], payload)
	}
}

func TestHandleScopedToolsCallPreservesStructuredErrorResult(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "dup.txt")
	if err := os.WriteFile(target, []byte("same\nsame\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	handler := lsptools.NewEditHandlerWithRoot(root, lspmanager.NewRegistry(nil))
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "edit"},
		Handler:  ToolHandler(handler),
	}}
	args, err := json.Marshal(map[string]any{
		"file_path": target,
		"patch":     "@@\n-same\n+changed\n",
	})
	require.NoError(t, err)
	params, err := json.Marshal(map[string]any{
		"name":            "edit",
		"arguments":       json.RawMessage(args),
		"_cwd":            root,
		"_workspaceRoots": []string{root},
	})
	require.NoError(t, err)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	payload, ok := result.(map[string]any)
	require.Truef(t, ok, "handleScopedToolsCall result = %T, want map", result)
	require.Equal(t, true, payload["isError"])
	contentList, ok := payload["content"].([]map[string]string)
	require.True(t, ok)
	require.Contains(t, contentList[0]["text"], "Candidate locations:")
	require.Contains(t, contentList[0]["text"], target+":1-L1")
	structured, ok := payload["structuredContent"].(json.RawMessage)
	require.True(t, ok)
	require.True(t, strings.Contains(string(structured), "candidate_locations"), "structuredContent = %s", structured)
}

func TestLSPOnToolsCallInjectsScopeContext(t *testing.T) {
	trustedRoot := t.TempDir()
	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			called = true
			scope := requireToolScope(t, ctx)
			assertTrustedToolScopeContext(t, ctx, scope, trustedRoot)
			assertToolScopeHasNoSessionID(t)
			payload := decodeScopedToolCallPayload(t, args)
			assertForgedToolArgumentsPreserved(t, scope, payload)
			return map[string]any{"ok": true}, nil
		},
	}}
	params, err := json.Marshal(map[string]any{
		"name": "file",
		"arguments": map[string]any{
			"agent_id":   "forged-agent",
			"thread_id":  "forged-thread",
			"cwd":        "/forged/root",
			"session_id": "forged-session",
		},
		"_agentId":   "trusted-agent",
		"_threadId":  "trusted-thread",
		"_callId":    "trusted-call",
		"_cwd":       trustedRoot,
		"sessionId":  "top-level-forged-session",
		"session_id": "top-level-forged-session",
	})
	require.NoError(t, err)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	require.True(t, called, "handler was not called")
	assertStructuredToolResult(t, result)
}

func TestHandleScopedToolsCallUsesRuntimeRootForDirectMCPClient(t *testing.T) {
	trustedRoot := t.TempDir()
	unsetEnvForTest(t, "GO_AGENT_LSP_ROOTS")
	t.Setenv("GO_AGENT_LSP_ROOT", trustedRoot)
	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			called = true
			roots, err := common.WorkspaceRootsFromContextStrict(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{trustedRoot}, roots)
			scope := requireToolScope(t, ctx)
			require.Equal(t, trustedRoot, scope.CWD)
			require.Equal(t, []string{trustedRoot}, scope.WorkspaceRoots)
			payload := decodeScopedToolCallPayload(t, args)
			require.Equal(t, "/forged/root", payload.CWD)
			return map[string]any{"ok": true}, nil
		},
	}}
	params := json.RawMessage(`{"name":"file","arguments":{"cwd":"/forged/root"}}`)

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

func assertTrustedToolScopeContext(t *testing.T, ctx context.Context, scope common.ToolScope, trustedRoot string) {
	t.Helper()
	require.Equal(t, "trusted-agent", scope.AgentID)
	require.Equal(t, "trusted-thread", scope.ThreadID)
	require.Equal(t, "trusted-call", scope.CallID)
	require.Equal(t, trustedRoot, scope.CWD)
	require.Equal(t, trustedRoot, common.WorkspaceRootFromContext(ctx, "/fallback"))
}

func assertToolScopeHasNoSessionID(t *testing.T) {
	t.Helper()
	_, scopeHasSession := reflect.TypeFor[common.ToolScope]().FieldByName("SessionID")
	require.False(t, scopeHasSession, "common.ToolScope unexpectedly exposes SessionID")
	_, paramsHasSession := reflect.TypeFor[common.ToolCallParams]().FieldByName("SessionID")
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
	raw, ok := payload["structuredContent"].(json.RawMessage)
	require.Truef(t, ok, "structuredContent = %T, want json.RawMessage", payload["structuredContent"])
	var object map[string]any
	require.NoErrorf(t, json.Unmarshal(raw, &object), "structuredContent = %s, want JSON object", raw)
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
