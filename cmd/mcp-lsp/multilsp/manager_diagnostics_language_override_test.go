package multilsp

import (
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
)

func TestDiagnosticsRefreshPreservesExplicitJavaScriptReactOwnerForJSFile(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "const value = 1\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    factory,
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := ctxWithCWD(root, "agent-workspace-sync", "thread-workspace-sync")
	scope, err := ResolveLSPToolScope(LSPToolScope{
		AgentID:               "agent-workspace-sync",
		ThreadID:              "thread-workspace-sync",
		CWD:                   root,
		WorkspaceRoots:        []string{root},
		Family:                "lsp",
		LanguageID:            "javascriptreact",
		TargetPath:            target,
		WorkspaceRoot:         root,
		LanguageWorkspaceRoot: root,
		ProjectRoot:           root,
		RootKind:              "jsts_project",
		LanguageSpecific: map[string]string{
			"adapterRoot":     root,
			"adapterRootKind": "jsts_project",
		},
	})
	if err != nil {
		t.Fatalf("resolve javascriptreact scope: %v", err)
	}
	ctx = lspmanager.WithResolvedToolScope(ctx, lspmanager.ResolvedToolScope{
		ToolScope:    managerToolScope(scope.LSPToolScope),
		ScopeKey:     scope.ScopeKey,
		WorkspaceKey: scope.WorkspaceKey,
		ShardKey:     scope.ShardKey,
		ManagerKey:   scope.ManagerKey,
	})
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascriptreact", 1, text); err != nil {
		t.Fatalf("DidOpen javascriptreact document: %v", err)
	}

	err = mgr.refreshExistingDiagnosticTargets(ctx, []string{uri}, diagnosticFilter{})
	if err != nil {
		t.Fatalf("refreshExistingDiagnosticTargets() error = %v, want explicit javascriptreact ownership to be preserved", err)
	}
}
