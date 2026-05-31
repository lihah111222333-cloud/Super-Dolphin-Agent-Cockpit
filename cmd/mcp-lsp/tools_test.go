package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	lsptools "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/stretchr/testify/require"
)

func TestLSPToolManifestsExposeShortNames(t *testing.T) {
	got := make([]string, 0, len(lspToolManifests))
	for _, manifest := range lspToolManifests {
		got = append(got, manifest.Name)
	}
	want := []string{"file", "inspect", "xref", "grep", "structure", "edit", "completion", "code_run", "code_run_test"}
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
	for _, want := range []string{"file", "grep", "code_run", "code_run_test"} {
		if !got[want] {
			t.Fatalf("tools/list missing non-semantic helper %q; got %#v", want, got)
		}
	}
}

func TestToolsListPackagedAvailabilityIgnoresSystemOnlyLanguageServers(t *testing.T) {
	systemBin := t.TempDir()
	writeMcpLSPExecutable(t, systemBin, "gopls")
	t.Setenv("PATH", systemBin)
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", filepath.Join(t.TempDir(), "manifest.json"))
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
			t.Fatalf("tools/list exposed semantic LSP tool %q from system PATH in packaged mode; got %#v", hidden, got)
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
	for _, want := range []string{"code_run", "code_run_test"} {
		if !got[want] {
			t.Fatalf("tools/list missing execution helper %q; got %#v", want, got)
		}
	}
}

func TestCodeRunTestUsesRuntimeWorkspaceRoots(t *testing.T) {
	workspaceRoot := canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(workspaceRoot, "go.mod"), "module example.test/coderuntest\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(workspaceRoot, "sample_test.go"), `package coderuntest

import "testing"

func TestCodeRunTestTarget(t *testing.T) {}
`)
	rawRoots, err := json.Marshal([]string{workspaceRoot})
	require.NoError(t, err)
	t.Setenv("GO_AGENT_LSP_ROOTS", string(rawRoots))

	handlers, err := newToolHandlers(&Manager{root: t.TempDir()})
	require.NoError(t, err)
	result, err := registryToolProvider{defs: toolDefinitions(handlers)}.CallTool(context.Background(), "code_run_test", mustJSON(t, map[string]any{
		"test_func": "TestCodeRunTestTarget",
	}))
	require.NoError(t, err)

	payload, ok := result.(lsptools.CodeRunResult)
	require.Truef(t, ok, "code_run_test result = %#v, want CodeRunResult", result)
	require.Truef(t, payload.Success, "code_run_test failed: output=%q exit=%d", payload.Output, payload.ExitCode)
	require.Equal(t, "go", payload.Language)
	require.Equal(t, "test", payload.Mode)
	require.Contains(t, payload.Output, "example.test/coderuntest")
}

func TestCodeRunTestRejectsAbsoluteTestPkgOutsideWorkspaceRoot(t *testing.T) {
	workspaceRoot := canonicalToolTestRoot(t, t.TempDir())
	outsideRoot := canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(workspaceRoot, "go.mod"), "module example.test/workspace\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(outsideRoot, "go.mod"), "module example.test/outside\n\ngo 1.25.0\n")
	rawRoots, err := json.Marshal([]string{workspaceRoot})
	require.NoError(t, err)
	t.Setenv("GO_AGENT_LSP_ROOTS", string(rawRoots))

	handlers, err := newToolHandlers(&Manager{root: workspaceRoot})
	require.NoError(t, err)
	result, err := registryToolProvider{defs: toolDefinitions(handlers)}.CallTool(context.Background(), "code_run_test", mustJSON(t, map[string]any{
		"test_func": "TestShouldNotRun",
		"test_pkg":  outsideRoot,
	}))
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "outside allowed workspace roots")
}

func TestDirectStdioServerCodeRunTestUsesRuntimeWorkspaceRoots(t *testing.T) {
	workspaceRoot := canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(workspaceRoot, "go.mod"), "module example.test/directcoderuntest\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(workspaceRoot, "direct_test.go"), `package directcoderuntest

import "testing"

func TestDirectCodeRunTestTarget(t *testing.T) {}
`)
	rawRoots, err := json.Marshal([]string{workspaceRoot})
	require.NoError(t, err)
	t.Setenv("GO_AGENT_LSP_ROOTS", string(rawRoots))
	handlers, err := newToolHandlers(&Manager{root: t.TempDir()})
	require.NoError(t, err)
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "code_run_test",
			"arguments": map[string]any{
				"test_func": "TestDirectCodeRunTestTarget",
			},
		},
	})
	require.NoError(t, err)

	var output bytes.Buffer
	server := common.NewServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: toolDefinitions(handlers)})
	require.NoError(t, server.Run(context.Background()))
	require.Contains(t, output.String(), "example.test/directcoderuntest")
	require.NotContains(t, output.String(), "unknown tool")
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
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

func TestDirectStdioServerMcpLSPFamilyUsesRuntimeWorkspaceRootsWhenMetadataMissing(t *testing.T) {
	trustedRoot, evilRoot := setupTrustedAndEvilToolRoots(t)
	extraRoot := canonicalToolTestRoot(t, t.TempDir())
	rawRoots, err := json.Marshal([]string{trustedRoot, extraRoot})
	require.NoError(t, err)
	t.Setenv("GO_AGENT_LSP_ROOT", trustedRoot)
	t.Setenv("GO_AGENT_LSP_ROOTS", string(rawRoots))

	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			called = true
			roots, err := common.WorkspaceRootsFromContextStrict(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{trustedRoot, extraRoot}, roots)
			scope := requireToolScope(t, ctx)
			require.Equal(t, "lsp", scope.Family)
			require.Equal(t, trustedRoot, scope.CWD)
			payload := decodeScopedToolCallPayload(t, args)
			require.Equal(t, "agent-forged", payload.AgentID)
			require.Equal(t, evilRoot, payload.CWD)
			return map[string]any{"ok": true}, nil
		},
	}}
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "file",
			"arguments": map[string]any{
				"file_path":       "main.go",
				"agent_id":        "agent-forged",
				"cwd":             evilRoot,
				"_workspaceRoots": []string{evilRoot},
				"workspaceRoots":  []string{evilRoot},
			},
		},
	})
	require.NoError(t, err)
	var output bytes.Buffer
	server := common.NewServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs})
	require.NoError(t, server.Run(context.Background()))
	require.True(t, called, "direct stdio tools/call did not reach handler")
	require.NotContains(t, output.String(), common.ErrMissingWorkspaceRoots.Error())
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

func TestEditSchemaExposesPatchDiskFieldsOnly(t *testing.T) {
	props, ok := lspEditSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("edit schema properties type = %T", lspEditSchema["properties"])
	}
	for _, field := range []string{"patch", "version"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("edit schema missing runtime field %q", field)
		}
	}
	for _, field := range []string{"action", "line", "column", "end_line", "end_column", "edits", "new_name", "new_text", "only", "persist_to_disk", "force"} {
		if _, ok := props[field]; ok {
			t.Fatalf("edit schema exposes removed non-patch field %q", field)
		}
	}
	required, ok := lspEditSchema["required"].([]string)
	if !ok {
		t.Fatalf("edit schema required type = %T", lspEditSchema["required"])
	}
	if !reflect.DeepEqual(required, []string{"file_path", "patch"}) {
		t.Fatalf("edit schema required = %#v, want file_path and patch", required)
	}
}
