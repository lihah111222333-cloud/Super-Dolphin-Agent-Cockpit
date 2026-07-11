package multilsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
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

func TestRegistryScopedResolverSelectsWorkspaceRootContainingAbsoluteTarget(t *testing.T) {
	tmp := canonicalTestRoot(t, t.TempDir())
	primary := filepath.Join(tmp, "primary")
	extra := filepath.Join(tmp, "extra")
	writeFile(t, filepath.Join(primary, "README.md"), "# primary\n")
	writeFile(t, filepath.Join(extra, "notes.md"), "# extra\n")
	primaryManager := newManagerPoolTestManager(t, primary)
	resolver := NewRegistryScopedResolver(primaryManager)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}

	target := filepath.Join(extra, "notes.md")
	scoped := mustResolveRegistryToolScope(t, resolver, lspmanager.ToolScope{
		AgentID:        "agent-a",
		ThreadID:       "thread-a",
		CWD:            primary,
		WorkspaceRoots: []string{primary, extra},
		Family:         "lsp",
		LanguageID:     "markdown",
		TargetPath:     target,
	})

	if scoped.ResolvedScope.CWD != extra {
		t.Fatalf("resolved CWD = %q, want target workspace root %q", scoped.ResolvedScope.CWD, extra)
	}
	if scoped.ResolvedScope.WorkspaceRoot != extra || scoped.ResolvedScope.ProjectRoot != extra {
		t.Fatalf("resolved roots = workspace:%q project:%q, want %q", scoped.ResolvedScope.WorkspaceRoot, scoped.ResolvedScope.ProjectRoot, extra)
	}
	if scoped.ResolvedScope.TargetPath != target {
		t.Fatalf("resolved target = %q, want %q", scoped.ResolvedScope.TargetPath, target)
	}
}

func TestRegistryScopedResolverRejectsAbsoluteTargetOutsideWorkspaceRoots(t *testing.T) {
	primary := canonicalTestRoot(t, t.TempDir())
	outside := canonicalTestRoot(t, t.TempDir())
	writeFile(t, filepath.Join(primary, "README.md"), "# primary\n")
	writeFile(t, filepath.Join(outside, "notes.md"), "# outside\n")
	primaryManager := newManagerPoolTestManager(t, primary)
	resolver := NewRegistryScopedResolver(primaryManager)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}

	_, err := resolver.ForToolScope(lspmanager.ToolScope{
		AgentID:        "agent-a",
		ThreadID:       "thread-a",
		CWD:            primary,
		WorkspaceRoots: []string{primary},
		Family:         "lsp",
		LanguageID:     "markdown",
		TargetPath:     filepath.Join(outside, "notes.md"),
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("ForToolScope error = %v, want outside workspace roots", err)
	}
}

func TestRegistryScopedResolverRejectsRelativeTargetEscapingWorkspaceRoots(t *testing.T) {
	tmp := canonicalTestRoot(t, t.TempDir())
	primary := filepath.Join(tmp, "primary")
	outside := filepath.Join(tmp, "outside")
	writeFile(t, filepath.Join(primary, "README.md"), "# primary\n")
	writeFile(t, filepath.Join(outside, "notes.md"), "# outside\n")
	primaryManager := newManagerPoolTestManager(t, primary)
	resolver := NewRegistryScopedResolver(primaryManager)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}

	_, err := resolver.ForToolScope(lspmanager.ToolScope{
		AgentID:        "agent-a",
		ThreadID:       "thread-a",
		CWD:            primary,
		WorkspaceRoots: []string{primary},
		Family:         "lsp",
		LanguageID:     "markdown",
		TargetPath:     "../outside/notes.md",
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("ForToolScope error = %v, want outside workspace roots", err)
	}
}

func TestRegistryScopedResolverRejectsResolvedProjectRootOutsideWorkspaceRoots(t *testing.T) {
	repo := canonicalTestRoot(t, t.TempDir())
	opened := filepath.Join(repo, "opened")
	writeFile(t, filepath.Join(repo, "package.json"), `{"name":"outer"}`+"\n")
	writeFile(t, filepath.Join(opened, "src", "app.ts"), "export const value = 1\n")
	primaryManager := newManagerPoolTestManager(t, opened)
	resolver := NewRegistryScopedResolver(primaryManager)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}

	_, err := resolver.ForToolScope(lspmanager.ToolScope{
		AgentID:        "agent-a",
		ThreadID:       "thread-a",
		CWD:            opened,
		WorkspaceRoots: []string{opened},
		Family:         "lsp",
		LanguageID:     "typescript",
		TargetPath:     "src/app.ts",
	})
	if err == nil || !strings.Contains(err.Error(), "outside workspace roots") {
		t.Fatalf("ForToolScope error = %v, want outside workspace roots", err)
	}
}

func TestRegistryScopedResolverCurrentManagersFiltersByTrustedRoots(t *testing.T) {
	tmp := canonicalTestRoot(t, t.TempDir())
	primary := filepath.Join(tmp, "primary")
	extra := filepath.Join(tmp, "extra")
	writeFile(t, filepath.Join(primary, "README.md"), "# primary\n")
	writeFile(t, filepath.Join(extra, "notes.md"), "# extra\n")
	primaryManager := newManagerPoolTestManager(t, primary)
	resolver := NewRegistryScopedResolver(primaryManager)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}

	extraScoped := mustResolveRegistryToolScope(t, resolver, lspmanager.ToolScope{
		AgentID:        "agent-a",
		ThreadID:       "thread-a",
		CWD:            primary,
		WorkspaceRoots: []string{primary, extra},
		Family:         "lsp",
		LanguageID:     "markdown",
		TargetPath:     filepath.Join(extra, "notes.md"),
	})
	current, err := resolver.CurrentManagersForToolScope(lspmanager.ToolScope{
		AgentID:        "agent-a",
		ThreadID:       "thread-a",
		CWD:            primary,
		WorkspaceRoots: []string{primary},
		Family:         "lsp",
		LanguageID:     "markdown",
	})
	if err != nil {
		t.Fatalf("CurrentManagersForToolScope: %v", err)
	}
	for _, scoped := range current {
		if scoped.Manager == extraScoped.Manager {
			t.Fatalf("current managers included stale extra-root manager after roots narrowed: %#v", current)
		}
	}
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
