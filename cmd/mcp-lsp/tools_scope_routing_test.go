package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	lsptools "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/stretchr/testify/require"
)

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

func TestHandleScopedToolsCallKeepsMetadataRootsImmutableWhenRuntimeEnvIsInvalid(t *testing.T) {
	metadataRoot := canonicalToolTestRoot(t, t.TempDir())
	runtimeRoot := canonicalToolTestRoot(t, t.TempDir())
	t.Setenv("GO_AGENT_LSP_ROOT", runtimeRoot)
	t.Setenv("GO_AGENT_LSP_ROOTS", "[")

	called := false
	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			called = true
			scope := requireToolScope(t, ctx)
			require.Equal(t, metadataRoot, scope.CWD)
			require.Equal(t, []string{metadataRoot}, scope.WorkspaceRoots)
			roots, err := common.WorkspaceRootsFromContextStrict(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{metadataRoot}, roots)
			return map[string]any{"ok": true}, nil
		},
	}}
	params, err := json.Marshal(map[string]any{
		"name":            "file",
		"arguments":       map[string]any{"file_path": "main.go"},
		"_cwd":            metadataRoot,
		"_workspaceRoots": []string{metadataRoot},
	})
	require.NoError(t, err)

	_, err = handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	require.True(t, called, "tools/call did not reach handler")
}

func TestHandleScopedToolsCallRejectsRuntimeRootWorkDirWhenMetadataRootsPresent(t *testing.T) {
	metadataRoot := canonicalToolTestRoot(t, t.TempDir())
	runtimeRoot := canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(runtimeRoot, "main.go"), "package main\n")
	rawRoots, err := json.Marshal([]string{runtimeRoot})
	require.NoError(t, err)
	t.Setenv("GO_AGENT_LSP_ROOT", runtimeRoot)
	t.Setenv("GO_AGENT_LSP_ROOTS", string(rawRoots))

	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: ToolHandler(lsptools.NewFileHandler(lsptools.Config{
			WorkspaceRoot: metadataRoot,
		})),
	}}
	params, err := json.Marshal(map[string]any{
		"name": "file",
		"arguments": map[string]any{
			"action":    "read_file",
			"file_path": "main.go",
			"scope":     "lines",
			"limit":     5,
			"work_dir":  runtimeRoot,
		},
		"_cwd":            metadataRoot,
		"_workspaceRoots": []string{metadataRoot},
	})
	require.NoError(t, err)

	result, err := handleScopedToolsCall(context.Background(), registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(raw), "outside workspace roots")
	require.NotContains(t, string(raw), "package main")
}

func TestHandleScopedToolsCallAllowsRuntimeRootWorkDirWithExplicitCapability(t *testing.T) {
	metadataRoot := canonicalToolTestRoot(t, t.TempDir())
	runtimeRoot := canonicalToolTestRoot(t, t.TempDir())
	writeTestFile(t, filepath.Join(runtimeRoot, "main.go"), "package main\n")

	defs := []toolDefinition{{
		Manifest: ToolManifest{Name: "file"},
		Handler: ToolHandler(lsptools.NewFileHandler(lsptools.Config{
			WorkspaceRoot: metadataRoot,
		})),
	}}
	params, err := json.Marshal(map[string]any{
		"name": "file",
		"arguments": map[string]any{
			"action":    "read_file",
			"file_path": "main.go",
			"scope":     "lines",
			"limit":     5,
			"work_dir":  runtimeRoot,
		},
		"_cwd":            metadataRoot,
		"_workspaceRoots": []string{metadataRoot},
	})
	require.NoError(t, err)
	ctx, err := lsptools.WithRuntimeWorkspaceRootCapability(context.Background(), []string{runtimeRoot})
	require.NoError(t, err)

	result, err := handleScopedToolsCall(ctx, registryToolProvider{defs: defs}, "lsp", params)
	require.NoError(t, err)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(raw), "package main")
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
