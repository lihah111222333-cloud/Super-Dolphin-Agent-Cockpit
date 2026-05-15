package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func TestDiagnosticsStoreDoesNotCrossAgentScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module scoped\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctxA := scopedDiagnosticsTestContext("agent-a", "thread-1")
	ctxB := scopedDiagnosticsTestContext("agent-b", "thread-1")
	refA, _, scopeA, err := mgr.resolvedScopeForURI(ctxA, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve scope A: %v", err)
	}
	uri := refA.uri
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(uri, scopeA, "fp-a")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "agent-a-only",
		}},
	}); err != nil {
		t.Fatalf("publish diagnostics: %v", err)
	}

	itemsA, err := mgr.Diagnostics(ctxA, []string{uri})
	if err != nil {
		t.Fatalf("diagnostics A: %v", err)
	}
	if len(itemsA) != 1 || len(itemsA[0].Diagnostics) != 1 {
		t.Fatalf("diagnostics A = %#v, want one scoped diagnostic", itemsA)
	}
	itemsB, err := mgr.Diagnostics(ctxB, []string{uri})
	if err != nil {
		t.Fatalf("diagnostics B: %v", err)
	}
	if len(itemsB) != 0 {
		t.Fatalf("diagnostics B = %#v, want no cross-scope diagnostics", itemsB)
	}
}

func TestDeletedDiagnosticsCleanupRemovesOldAndCurrentScopedCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module cleanup\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "stale.go")
	if err := os.WriteFile(target, []byte("package cleanup\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()
	coordinator := bootstrapCoordinatorFor(mgr)
	ctxOld := scopedDiagnosticsTestContext("agent-old", "thread-1")
	ctxCurrent := scopedDiagnosticsTestContext("agent-current", "thread-1")
	refOld, _, oldScope, err := mgr.resolvedScopeForURI(ctxOld, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve old scope: %v", err)
	}
	uri := refOld.uri
	_, _, currentScope, err := mgr.resolvedScopeForURI(ctxCurrent, uri, "")
	if err != nil {
		t.Fatalf("resolve current scope: %v", err)
	}
	oldKey := oldScope.cacheKey("go", uri)
	currentKey := currentScope.cacheKey("go", uri)
	coordinator.cache.Upsert(lspCacheValue{Key: oldKey, Fingerprint: "old"})
	coordinator.cache.Upsert(lspCacheValue{Key: currentKey, Fingerprint: "current"})
	coordinator.states.complete(oldScope.bootstrapKey(), uri, "old", 1)
	coordinator.states.complete(currentScope.bootstrapKey(), uri, "current", 1)
	coordinator.cache.RememberDocumentScope(uri, oldScope, "old")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "stale old scope",
		}},
	}); err != nil {
		t.Fatalf("publish diagnostics: %v", err)
	}

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	items, err := mgr.Diagnostics(ctxCurrent, []string{uri})
	if err != nil {
		t.Fatalf("deleted diagnostics: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("deleted diagnostics = %#v, want empty result", items)
	}
	if _, ok := coordinator.cache.Load(oldKey); ok {
		t.Fatalf("old scoped cache key survived deleted-file cleanup")
	}
	if _, ok := coordinator.cache.Load(currentKey); ok {
		t.Fatalf("current scoped cache key survived deleted-file cleanup")
	}
	if got := coordinator.states.status(oldScope.bootstrapKey(), uri); got != bootstrapPending {
		t.Fatalf("old bootstrap state = %s, want pending/deleted", got)
	}
	if got := coordinator.states.status(currentScope.bootstrapKey(), uri); got != bootstrapPending {
		t.Fatalf("current bootstrap state = %s, want pending/deleted", got)
	}
}

func TestDiagnosticsScopeIgnoresPrivateAgentKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module privatekeys\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctxTrusted := scopedDiagnosticsTestContext("agent-a", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctxTrusted, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve trusted scope: %v", err)
	}
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "trusted")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: ref.uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "trusted-scope-only",
		}},
	}); err != nil {
		t.Fatalf("publish diagnostics: %v", err)
	}

	ctxPrivateOnly := context.WithValue(context.Background(), "_agentId", "agent-a")
	ctxPrivateOnly = context.WithValue(ctxPrivateOnly, "_threadId", "thread-1")
	items, err := mgr.Diagnostics(ctxPrivateOnly, []string{ref.uri})
	if err != nil {
		t.Fatalf("diagnostics with private keys: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("diagnostics with private keys = %#v, want no trusted-scope match", items)
	}
}

func TestDiagnosticsResolvedScopeCanBeInjected(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module injected\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	canonical, err := ResolveLSPToolScope(LSPToolScope{
		AgentID:               "agent-injected",
		ThreadID:              "thread-injected",
		Family:                defaultLSPToolFamily,
		LanguageID:            "go",
		WorkspaceRoot:         root,
		LanguageWorkspaceRoot: root,
		ProjectRoot:           root,
		RootKind:              goRootKindGoMod,
	})
	if err != nil {
		t.Fatalf("canonical scope: %v", err)
	}
	ctx := WithResolvedLSPToolScope(context.Background(), canonical)

	_, _, got, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve injected scope: %v", err)
	}
	if got.ManagerKey != canonical.ManagerKey || got.WorkspaceKey != canonical.WorkspaceKey || got.ScopeKey != canonical.ScopeKey {
		t.Fatalf("resolved scope = %#v, want injected canonical %#v", got, canonical)
	}
}

func TestDiagnosticsManagerResolvedScopeCanBeInjected(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module generic\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctx := lspmanager.WithResolvedToolScope(context.Background(), lspmanager.ResolvedToolScope{
		ToolScope: lspmanager.ToolScope{
			AgentID:               "agent-generic",
			ThreadID:              "thread-generic",
			Family:                defaultLSPToolFamily,
			LanguageID:            "go",
			WorkspaceRoot:         root,
			LanguageWorkspaceRoot: root,
			ProjectRoot:           root,
			RootKind:              goRootKindGoMod,
		},
		ScopeKey:     "lsp\x00agent-generic\x00thread-generic",
		WorkspaceKey: "workspace-generic",
		ManagerKey:   "manager-generic",
	})

	_, _, got, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve injected manager scope: %v", err)
	}
	if got.ManagerKey != "manager-generic" || got.WorkspaceKey != "workspace-generic" || got.ScopeKey != "lsp\x00agent-generic\x00thread-generic" {
		t.Fatalf("resolved manager scope = %#v, want generic injected scope", got)
	}
}

func scopedDiagnosticsTestContext(agentID, threadID string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  agentID,
		ThreadID: threadID,
		Family:   defaultLSPToolFamily,
	})
}
