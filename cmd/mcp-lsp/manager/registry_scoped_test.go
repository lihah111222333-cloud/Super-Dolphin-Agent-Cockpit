package manager

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestRegistryResolveManagerForFileUsesTrustedToolScope(t *testing.T) {
	singleton := &registryDiagnosticsManager{}
	scopedMgr := &registryDiagnosticsManager{}
	resolver := &recordingScopedResolver{manager: scopedMgr}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  "agent-trusted",
		ThreadID: "thread-trusted",
		CallID:   "call-trusted",
		CWD:      "/trusted/worktree",
		Family:   "lsp",
	})
	scoped, err := registry.ResolveManagerForFile(ctx, "main.go")
	if err != nil {
		t.Fatalf("ResolveManagerForFile: %v", err)
	}
	if scoped.Manager != scopedMgr {
		t.Fatalf("resolved manager = %p, want scoped manager %p", scoped.Manager, scopedMgr)
	}
	if resolver.lastScope.AgentID != "agent-trusted" || resolver.lastScope.ThreadID != "thread-trusted" {
		t.Fatalf("resolver scope identity = %#v, want trusted agent/thread", resolver.lastScope)
	}
	if resolver.lastScope.CWD != "/trusted/worktree" {
		t.Fatalf("resolver CWD = %q, want trusted cwd", resolver.lastScope.CWD)
	}
	if resolver.lastScope.LanguageID != "go" || resolver.lastScope.TargetPath != "main.go" {
		t.Fatalf("resolver target scope = %#v", resolver.lastScope)
	}
}

func TestRegistryDiagnosticsAllUsesCurrentScopedManagers(t *testing.T) {
	singleton := &registryDiagnosticsManager{}
	scopedMgr := &registryDiagnosticsManager{}
	resolver := &recordingScopedResolver{
		current: []ScopedManager{{
			Manager: scopedMgr,
			ResolvedScope: ResolvedToolScope{
				ScopeKey: "lsp\x00agent-a\x00thread-a",
			},
		}},
	}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  "agent-a",
		ThreadID: "thread-a",
		CWD:      "/trusted/worktree",
		Family:   "lsp",
	})
	if _, err := registry.Diagnostics(ctx, nil); err != nil {
		t.Fatalf("Diagnostics(ctx, nil): %v", err)
	}
	if singleton.diagnosticsContext != nil {
		t.Fatalf("Diagnostics(ctx,nil) called singleton manager; want current scoped managers only")
	}
	if scopedMgr.diagnosticsContext == nil {
		t.Fatalf("Diagnostics(ctx,nil) did not call scoped manager")
	}
	if resolver.currentScope.AgentID != "agent-a" || resolver.currentScope.ThreadID != "thread-a" {
		t.Fatalf("current scope = %#v, want trusted scope", resolver.currentScope)
	}
}

type recordingScopedResolver struct {
	manager      Manager
	current      []ScopedManager
	lastScope    ToolScope
	currentScope ToolScope
}

func (r *recordingScopedResolver) ForToolScope(scope ToolScope) (ScopedManager, error) {
	r.lastScope = scope
	return ScopedManager{
		Manager: r.manager,
		ResolvedScope: ResolvedToolScope{
			ToolScope:    scope,
			ScopeKey:     "scope-key",
			WorkspaceKey: "workspace-key",
			ShardKey:     "shard-key",
			ManagerKey:   "manager-key",
		},
	}, nil
}

func (r *recordingScopedResolver) CurrentManagersForToolScope(scope ToolScope) ([]ScopedManager, error) {
	r.currentScope = scope
	return r.current, nil
}
