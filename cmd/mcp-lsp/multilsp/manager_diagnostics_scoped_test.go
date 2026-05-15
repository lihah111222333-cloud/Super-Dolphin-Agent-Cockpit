package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
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

func scopedDiagnosticsTestContext(agentID, threadID string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, lspScopeAgentIDContextKey, agentID)
	ctx = context.WithValue(ctx, lspScopeThreadIDContextKey, threadID)
	return ctx
}
