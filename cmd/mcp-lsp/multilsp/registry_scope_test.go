package multilsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestRegistryScopedResolverForToolScopeUsesGoRootAndTrustedIdentity(t *testing.T) {
	root := scopedResolverTestRoot(t)
	primary := newManagerPoolTestManager(t, root)
	resolver := NewRegistryScopedResolver(primary)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}

	scope := registryGoToolScope(root, "agent-a", "thread-a", "call-a")
	scoped := mustResolveRegistryToolScope(t, resolver, scope)
	assertScopedResolverResult(t, scoped, primary, root)
	assertRegistryCurrentManager(t, resolver, scope, scoped.Manager)
	assertRegistryOtherAgentHasNoManagers(t, resolver, root)
}

func scopedResolverTestRoot(t *testing.T) string {
	t.Helper()
	root := canonicalTestRoot(t, t.TempDir())
	writeFile(t, filepath.Join(root, "go.mod"), "module example.test/scoped\n\ngo 1.25.0\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	return root
}

func registryGoToolScope(root, agentID, threadID, callID string) lspmanager.ToolScope {
	return lspmanager.ToolScope{
		AgentID:    agentID,
		ThreadID:   threadID,
		CallID:     callID,
		CWD:        root,
		Family:     "lsp",
		LanguageID: "go",
		TargetPath: "main.go",
	}
}

func mustResolveRegistryToolScope(t *testing.T, resolver lspmanager.ScopedManagerResolver, scope lspmanager.ToolScope) lspmanager.ScopedManager {
	t.Helper()
	scoped, err := resolver.ForToolScope(scope)
	if err != nil {
		t.Fatalf("ForToolScope: %v", err)
	}
	return scoped
}

func assertScopedResolverResult(t *testing.T, scoped lspmanager.ScopedManager, primary lspmanager.Manager, root string) {
	t.Helper()
	if scoped.Manager == primary {
		t.Fatalf("ForToolScope returned primary singleton manager; want scoped clone")
	}
	if scoped.ResolvedScope.TargetPath != filepath.Join(root, "main.go") {
		t.Fatalf("TargetPath = %q, want trusted-root main.go", scoped.ResolvedScope.TargetPath)
	}
	if scoped.ResolvedScope.WorkspaceRoot != root || scoped.ResolvedScope.LanguageWorkspaceRoot != root {
		t.Fatalf("resolved roots = %#v, want go.mod root %q", scoped.ResolvedScope, root)
	}
	if !strings.Contains(scoped.ResolvedScope.ScopeKey, "agent-a") || !strings.Contains(scoped.ResolvedScope.ScopeKey, "thread-a") {
		t.Fatalf("ScopeKey = %q, want trusted agent/thread", scoped.ResolvedScope.ScopeKey)
	}
	if strings.Contains(scoped.ResolvedScope.ManagerKey, "call-a") {
		t.Fatalf("ManagerKey must not include call identity: %q", scoped.ResolvedScope.ManagerKey)
	}
}

func assertRegistryCurrentManager(t *testing.T, resolver lspmanager.ScopedManagerResolver, scope lspmanager.ToolScope, want lspmanager.Manager) {
	t.Helper()
	current, err := resolver.CurrentManagersForToolScope(scope)
	if err != nil {
		t.Fatalf("CurrentManagersForToolScope: %v", err)
	}
	if len(current) != 1 || current[0].Manager != want {
		t.Fatalf("current scoped managers = %#v, want the existing scoped clone", current)
	}
}

func assertRegistryOtherAgentHasNoManagers(t *testing.T, resolver lspmanager.ScopedManagerResolver, root string) {
	t.Helper()
	other, err := resolver.CurrentManagersForToolScope(lspmanager.ToolScope{
		AgentID:  "agent-b",
		ThreadID: "thread-a",
		CWD:      root,
		Family:   "lsp",
	})
	if err != nil {
		t.Fatalf("CurrentManagersForToolScope(other): %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("other agent current managers = %d, want 0", len(other))
	}
}

func canonicalTestRoot(t *testing.T, root string) string {
	t.Helper()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	return realRoot
}

func TestLSPScopeKeyFromContextUsesCommonToolScope(t *testing.T) {
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  "agent-common",
		ThreadID: "thread-common",
		Family:   "lsp",
	})
	if got, want := lspScopeKeyFromContext(ctx), "lsp\x00agent-common\x00thread-common"; got != want {
		t.Fatalf("lspScopeKeyFromContext = %q, want %q", got, want)
	}
}
