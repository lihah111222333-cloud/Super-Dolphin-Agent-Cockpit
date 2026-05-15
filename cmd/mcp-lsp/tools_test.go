package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestLSPToolManifestsExposeVisibleLegacyNames(t *testing.T) {
	got := make([]string, 0, len(lspToolManifests))
	for _, manifest := range lspToolManifests {
		got = append(got, manifest.Name)
	}
	want := []string{"lsp_file", "lsp_inspect", "lsp_xref", "lsp_grep", "lsp_structure", "lsp_edit", "lsp_completion", "code_run", "code_run_test"}
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
	for _, want := range []string{"code_run", "code_run_test"} {
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
			scope, ok := common.ToolScopeFromContext(ctx)
			if !ok {
				t.Fatal("ToolScopeFromContext() missing scope")
			}
			if scope.AgentID != "trusted-agent" || scope.ThreadID != "trusted-thread" || scope.CallID != "trusted-call" {
				t.Fatalf("scope identity = %#v, want trusted top-level metadata", scope)
			}
			if scope.CWD != "/trusted/lsp" {
				t.Fatalf("scope cwd = %q, want /trusted/lsp", scope.CWD)
			}
			if got := common.WorkspaceRootFromContext(ctx, "/fallback"); got != "/trusted/lsp" {
				t.Fatalf("WorkspaceRootFromContext() = %q, want /trusted/lsp", got)
			}
			if _, ok := reflect.TypeOf(common.ToolScope{}).FieldByName("SessionID"); ok {
				t.Fatal("common.ToolScope unexpectedly exposes SessionID")
			}
			if _, ok := reflect.TypeOf(common.ToolCallParams{}).FieldByName("SessionID"); ok {
				t.Fatal("common.ToolCallParams unexpectedly exposes SessionID")
			}
			var payload struct {
				AgentID   string `json:"agent_id"`
				ThreadID  string `json:"thread_id"`
				CWD       string `json:"cwd"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(args, &payload); err != nil {
				t.Fatalf("unmarshal args: %v", err)
			}
			if payload.AgentID != "forged-agent" || payload.ThreadID != "forged-thread" || payload.CWD != "/forged/root" || payload.SessionID != "forged-session" {
				t.Fatalf("arguments = %#v, want forged values preserved only as arguments", payload)
			}
			if scope.AgentID == payload.AgentID || scope.ThreadID == payload.ThreadID || scope.CWD == payload.CWD {
				t.Fatalf("scope = %#v was overwritten by forged arguments %#v", scope, payload)
			}
			return map[string]any{"ok": true}, nil
		},
	}}
	params := json.RawMessage(`{"name":"file","arguments":{"agent_id":"forged-agent","thread_id":"forged-thread","cwd":"/forged/root","session_id":"forged-session"},"_agentId":"trusted-agent","_threadId":"trusted-thread","_callId":"trusted-call","_cwd":"/trusted/lsp","sessionId":"top-level-forged-session","session_id":"top-level-forged-session"}`)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	if err != nil {
		t.Fatalf("handleScopedToolsCall() error = %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	payload, ok := result.(map[string]any)
	if !ok || payload["structuredContent"] == nil {
		t.Fatalf("handleScopedToolsCall() result = %#v, want structured content", result)
	}
}

func TestHandleScopedToolsCallRoutesTrustedScopeToManagerPool(t *testing.T) {
	trustedRoot := t.TempDir()
	evilRoot := t.TempDir()
	trustedRoot = canonicalToolTestRoot(t, trustedRoot)
	evilRoot = canonicalToolTestRoot(t, evilRoot)
	writeTestFile(t, filepath.Join(trustedRoot, "go.mod"), "module example.test/trusted\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(trustedRoot, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(evilRoot, "go.mod"), "module example.test/evil\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(evilRoot, "main.go"), "package evil\n")

	registry := lspmanager.NewRegistry(nil)
	mgr := multilsp.NewManager(multilsp.Config{WorkspaceRoot: evilRoot})
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("manager.Close(): %v", err)
		}
	})
	registry.Register("go", mgr, multilsp.NewRegistryScopedResolver(mgr))

	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var payload struct {
				FilePath string `json:"file_path"`
				AgentID  string `json:"agent_id"`
				CWD      string `json:"cwd"`
			}
			if err := json.Unmarshal(args, &payload); err != nil {
				return nil, err
			}
			if payload.AgentID != "agent-forged" || payload.CWD != evilRoot {
				t.Fatalf("test setup did not pass forged arguments: %#v", payload)
			}
			scoped, err := registry.ResolveManagerForFile(ctx, payload.FilePath)
			if err != nil {
				return nil, err
			}
			resolved := scoped.ResolvedScope
			if resolved.AgentID != "agent-trusted" || resolved.ThreadID != "thread-trusted" {
				t.Fatalf("resolved identity = %#v, want trusted top-level scope", resolved)
			}
			if resolved.CWD != trustedRoot {
				t.Fatalf("resolved CWD = %q, want trusted root %q", resolved.CWD, trustedRoot)
			}
			if resolved.TargetPath != filepath.Join(trustedRoot, "main.go") {
				t.Fatalf("resolved TargetPath = %q, want trusted target", resolved.TargetPath)
			}
			if strings.Contains(resolved.ManagerKey, "agent-forged") || strings.Contains(resolved.ManagerKey, evilRoot) {
				t.Fatalf("ManagerKey includes forged argument data: %q", resolved.ManagerKey)
			}
			if _, err := scoped.Manager.Diagnostics(ctx, nil); err != nil {
				return nil, err
			}
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
	if _, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params); err != nil {
		t.Fatalf("handleScopedToolsCall: %v", err)
	}
}

func TestDirectStdioServerMcpLSPFamilyRoutesTrustedScopeToManagerPool(t *testing.T) {
	trustedRoot := canonicalToolTestRoot(t, t.TempDir())
	evilRoot := canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(trustedRoot, "go.mod"), "module example.test/trusted\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(trustedRoot, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(evilRoot, "go.mod"), "module example.test/evil\n\ngo 1.25.0\n")
	writeTestFile(t, filepath.Join(evilRoot, "main.go"), "package evil\n")

	registry := lspmanager.NewRegistry(nil)
	mgr := multilsp.NewManager(multilsp.Config{WorkspaceRoot: evilRoot})
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("manager.Close(): %v", err)
		}
	})
	registry.Register("go", mgr, multilsp.NewRegistryScopedResolver(mgr))

	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			called = true
			var payload struct {
				FilePath string `json:"file_path"`
				AgentID  string `json:"agent_id"`
				CWD      string `json:"cwd"`
			}
			if err := json.Unmarshal(args, &payload); err != nil {
				return nil, err
			}
			if payload.AgentID != "agent-forged" || payload.CWD != evilRoot {
				t.Fatalf("test setup did not pass forged arguments: %#v", payload)
			}
			scoped, err := registry.ResolveManagerForFile(ctx, payload.FilePath)
			if err != nil {
				return nil, err
			}
			resolved := scoped.ResolvedScope
			if resolved.Family != "lsp" {
				t.Fatalf("resolved family = %q, want lsp", resolved.Family)
			}
			if resolved.AgentID != "agent-trusted" || resolved.ThreadID != "thread-trusted" {
				t.Fatalf("resolved identity = %#v, want trusted direct server metadata", resolved)
			}
			if resolved.CWD != trustedRoot {
				t.Fatalf("resolved CWD = %q, want trusted root %q", resolved.CWD, trustedRoot)
			}
			if strings.Contains(resolved.ManagerKey, "mcp-lsp") {
				t.Fatalf("ManagerKey contains raw server family: %q", resolved.ManagerKey)
			}
			if _, err := scoped.Manager.Diagnostics(ctx, nil); err != nil {
				return nil, err
			}
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
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var output bytes.Buffer
	server := common.NewServer("mcp-lsp", "dev", common.NewStdioTransport(bytes.NewBuffer(request), &output), registryToolProvider{defs: defs})
	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !called {
		t.Fatal("direct stdio tools/call did not reach handler")
	}
	if bytes.Contains(output.Bytes(), []byte("unsupported LSP scope family")) {
		t.Fatalf("direct stdio tools/call returned raw family error: %s", output.String())
	}
	if !bytes.Contains(output.Bytes(), []byte(`"ok":true`)) && !bytes.Contains(output.Bytes(), []byte(`"ok\":true`)) {
		t.Fatalf("Run() output = %s", output.String())
	}
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
