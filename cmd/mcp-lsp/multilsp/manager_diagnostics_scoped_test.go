package multilsp

import (
	"context"
	"encoding/json"
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

func TestDiagnosticsDropsOldGeneration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module generation\n"), 0o600); err != nil {
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

	ctx := scopedDiagnosticsTestContext("agent-generation", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve scope: %v", err)
	}
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "fp-generation")

	oldGeneration := mgr.CurrentDiagnosticGeneration()
	currentGeneration := mgr.AdvanceDiagnosticGeneration()
	if currentGeneration <= oldGeneration {
		t.Fatalf("AdvanceDiagnosticGeneration() = %d, want > %d", currentGeneration, oldGeneration)
	}

	if err := mgr.publishDiagnosticsForGeneration(protocol.PublishDiagnosticsParams{
		URI: ref.uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "old-generation",
		}},
	}, oldGeneration); err != nil {
		t.Fatalf("publish old generation diagnostics: %v", err)
	}
	items, err := mgr.Diagnostics(ctx, []string{ref.uri})
	if err != nil {
		t.Fatalf("diagnostics after old generation publish: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("old generation diagnostics = %#v, want dropped", items)
	}

	if err := mgr.publishDiagnosticsForGeneration(protocol.PublishDiagnosticsParams{
		URI: ref.uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "current-generation",
		}},
	}, currentGeneration); err != nil {
		t.Fatalf("publish current generation diagnostics: %v", err)
	}
	items, err = mgr.Diagnostics(ctx, []string{ref.uri})
	if err != nil {
		t.Fatalf("diagnostics after current generation publish: %v", err)
	}
	if len(items) != 1 || len(items[0].Diagnostics) != 1 || items[0].Diagnostics[0].Message != "current-generation" {
		t.Fatalf("current generation diagnostics = %#v, want current-generation", items)
	}
}

func TestDiagnosticsRefreshesStaleFileBeforeReturn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"diagnostics-stale"}`), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	target := filepath.Join(root, "app.js")
	if err := os.WriteFile(target, []byte("function staleName() { return 1; }\n"), 0o600); err != nil {
		t.Fatalf("write stale app.js: %v", err)
	}

	factory := &diagnosticsRefreshClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctx := scopedDiagnosticsTestContext("agent-stale", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("bootstrap stale document: %v", err)
	}
	client := factory.currentClient()
	if client == nil {
		t.Fatal("expected bootstrap to create a refresh client")
	}
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "stale-diagnostic",
		}},
	}); err != nil {
		t.Fatalf("publish stale diagnostics: %v", err)
	}

	if err := os.WriteFile(target, []byte("function freshName() { return 2; }\n"), 0o600); err != nil {
		t.Fatalf("write fresh app.js: %v", err)
	}
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("diagnostics after stale file edit: %v", err)
	}
	if got := client.changeCount(); got == 0 {
		t.Fatalf("Diagnostics did not refresh stale file before return; returned %#v", items)
	}
	if len(items) != 0 {
		t.Fatalf("diagnostics after stale file refresh = %#v, want empty after refresh publish", items)
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

func TestDiagnosticsClearsDeletedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module deleted\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "deleted.go")
	if err := os.WriteFile(target, []byte("package deleted\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()

	ctx := scopedDiagnosticsTestContext("agent-deleted", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve scope: %v", err)
	}
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "fp-deleted")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: ref.uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "deleted-file",
		}},
	}); err != nil {
		t.Fatalf("publish deleted diagnostics: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	items, err := mgr.Diagnostics(ctx, []string{ref.uri})
	if err != nil {
		t.Fatalf("diagnostics after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("diagnostics after delete = %#v, want empty", items)
	}
	mgr.diagMu.RLock()
	defer mgr.diagMu.RUnlock()
	for key, snapshot := range mgr.diagnostics {
		if snapshot.uri == ref.uri {
			t.Fatalf("diagnostic snapshot %q survived deleted-file cleanup: %#v", key, snapshot)
		}
	}
}

func TestDeletedFileClearsBootstrapAndCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module canonicaldelete\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	target := filepath.Join(root, "canonical.go")
	if err := os.WriteFile(target, []byte("package canonicaldelete\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	uri := fileURIFromPath(target)

	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	}()
	coordinator := bootstrapCoordinatorFor(mgr)

	oldScope := ResolvedLSPToolScope{
		LSPToolScope: LSPToolScope{
			AgentID:               "agent-old",
			ThreadID:              "thread-old",
			Family:                defaultLSPToolFamily,
			LanguageID:            "go",
			WorkspaceRoot:         root,
			LanguageWorkspaceRoot: root,
			ProjectRoot:           root,
			RootKind:              goRootKindGoMod,
			LanguageSpecific:      map[string]string{"canonical": "old"},
		},
		ScopeKey:     "canonical-scope-old",
		WorkspaceKey: "canonical-workspace-old",
		ShardKey:     "canonical-shard-old",
		ManagerKey:   "canonical-manager-old",
	}
	currentScope := ResolvedLSPToolScope{
		LSPToolScope: LSPToolScope{
			AgentID:               "agent-current",
			ThreadID:              "thread-current",
			Family:                defaultLSPToolFamily,
			LanguageID:            "go",
			WorkspaceRoot:         root,
			LanguageWorkspaceRoot: root,
			ProjectRoot:           root,
			RootKind:              goRootKindGoMod,
			LanguageSpecific:      map[string]string{"canonical": "current"},
		},
		ScopeKey:     "canonical-scope-current",
		WorkspaceKey: "canonical-workspace-current",
		ShardKey:     "canonical-shard-current",
		ManagerKey:   "canonical-manager-current",
	}
	oldKey := oldScope.cacheKey(oldScope.LanguageID, uri)
	currentKey := currentScope.cacheKey(currentScope.LanguageID, uri)
	coordinator.cache.Upsert(lspCacheValue{Key: oldKey, Fingerprint: "old"})
	coordinator.cache.Upsert(lspCacheValue{Key: currentKey, Fingerprint: "current"})
	coordinator.states.complete(oldScope.bootstrapKey(), uri, "old", 1)
	coordinator.states.complete(currentScope.bootstrapKey(), uri, "current", 1)
	coordinator.cache.RememberDocumentScope(uri, oldScope, "old")

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	ctx := WithResolvedLSPToolScope(context.Background(), currentScope)
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("diagnostics after canonical delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("deleted file diagnostics = %#v, want empty", items)
	}

	assertDeletedCacheKey := func(name string, key lspCacheKey) {
		t.Helper()
		if _, ok := coordinator.cache.Load(key); ok {
			t.Fatalf("%s canonical cache key survived deleted-file cleanup", name)
		}
		coordinator.cache.mu.RLock()
		_, tombstoned := coordinator.cache.tombstones[key.String()]
		coordinator.cache.mu.RUnlock()
		if !tombstoned {
			t.Fatalf("%s canonical cache key was not tombstoned", name)
		}
	}
	assertDeletedCacheKey("old LastResolvedScope", oldKey)
	assertDeletedCacheKey("current ResolvedLSPToolScope", currentKey)

	if got := coordinator.states.status(oldScope.bootstrapKey(), uri); got != bootstrapPending {
		t.Fatalf("old bootstrap state = %s, want pending/deleted", got)
	}
	if got := coordinator.states.status(currentScope.bootstrapKey(), uri); got != bootstrapPending {
		t.Fatalf("current bootstrap state = %s, want pending/deleted", got)
	}
	indexed, ok := coordinator.cache.LastResolvedScope(uri)
	if !ok {
		t.Fatalf("expected deleted-file cleanup to remember current scope index")
	}
	if indexed.LastResolvedScope.ManagerKey != currentScope.ManagerKey || indexed.LastResolvedScope.WorkspaceKey != currentScope.WorkspaceKey {
		t.Fatalf("last resolved scope = %#v, want current canonical scope %#v", indexed.LastResolvedScope, currentScope)
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

type diagnosticsRefreshClientFactory struct {
	client *diagnosticsRefreshClient
}

func (f *diagnosticsRefreshClientFactory) NewClient(_ string, handler protocol.NotificationHandler) (Client, error) {
	f.client = &diagnosticsRefreshClient{handler: handler}
	return f.client, nil
}

func (f *diagnosticsRefreshClientFactory) currentClient() *diagnosticsRefreshClient {
	return f.client
}

type diagnosticsRefreshClient struct {
	handler        protocol.NotificationHandler
	didChangeCount int
}

func (c *diagnosticsRefreshClient) Initialize(context.Context, string) error {
	return nil
}

func (c *diagnosticsRefreshClient) Shutdown(context.Context) error {
	return nil
}

func (c *diagnosticsRefreshClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}

func (c *diagnosticsRefreshClient) Notify(context.Context, string, any) error {
	return nil
}

func (c *diagnosticsRefreshClient) DidOpen(context.Context, string, string, int, string) error {
	return nil
}

func (c *diagnosticsRefreshClient) DidChange(ctx context.Context, uri string, _ int, _ []protocol.TextDocumentContentChangeEvent) error {
	c.didChangeCount++
	if c.handler == nil {
		return nil
	}
	return c.handler.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri})
}

func (c *diagnosticsRefreshClient) DidClose(context.Context, string) error {
	return nil
}

func (c *diagnosticsRefreshClient) Close() error {
	return nil
}

func (c *diagnosticsRefreshClient) changeCount() int {
	return c.didChangeCount
}

func scopedDiagnosticsTestContext(agentID, threadID string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  agentID,
		ThreadID: threadID,
		Family:   defaultLSPToolFamily,
	})
}
