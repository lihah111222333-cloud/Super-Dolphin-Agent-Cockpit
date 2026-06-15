package multilsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func writeDiagnosticsTestFile(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func newDiagnosticsTestManager(t *testing.T, cfg Config) *manager {
	t.Helper()
	mgr := NewManager(cfg).(*manager)
	t.Cleanup(func() {
		if err := mgr.Close(); err != nil {
			t.Fatalf("close manager: %v", err)
		}
	})
	return mgr
}

func resolveDiagnosticsScopeForTarget(t *testing.T, mgr *manager, ctx context.Context, target, fingerprint string) (string, ResolvedLSPToolScope) {
	t.Helper()
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve scope: %v", err)
	}
	mustBootstrapCoordinator(t, mgr).cache.RememberDocumentScope(ref.uri, scope, fingerprint)
	return ref.uri, scope
}

func publishDiagnosticMessage(t *testing.T, mgr *manager, uri, message string) {
	t.Helper()
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  message,
		}},
	}); err != nil {
		t.Fatalf("publish diagnostics: %v", err)
	}
}

func diagnosticsItemsForURI(t *testing.T, mgr *manager, ctx context.Context, uri, label string) []protocol.PublishDiagnosticsParams {
	t.Helper()
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("%s diagnostics: %v", label, err)
	}
	return items
}

func requireNoDiagnosticItems(t *testing.T, label string, items []protocol.PublishDiagnosticsParams) {
	t.Helper()
	if len(items) != 0 {
		t.Fatalf("%s diagnostics = %#v, want empty", label, items)
	}
}

func requireDiagnosticMessage(t *testing.T, items []protocol.PublishDiagnosticsParams, message string) {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("diagnostics = %#v, want one diagnostic item", items)
	}
	if len(items[0].Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one diagnostic", items)
	}
	if items[0].Diagnostics[0].Message != message {
		t.Fatalf("diagnostic message = %q, want %q", items[0].Diagnostics[0].Message, message)
	}
}

func requireEmptyDiagnosticSnapshot(t *testing.T, label string, items []protocol.PublishDiagnosticsParams) {
	t.Helper()
	if len(items) != 1 || len(items[0].Diagnostics) != 0 {
		t.Fatalf("%s diagnostics = %#v, want one empty ready item", label, items)
	}
}

func requireBootstrapPending(t *testing.T, coordinator *bootstrapCoordinator, scope ResolvedLSPToolScope, uri, label string) {
	t.Helper()
	if got := coordinator.states.status(scope.bootstrapKey(), uri); got != bootstrapPending {
		t.Fatalf("%s bootstrap state = %s, want pending/deleted", label, got)
	}
}

func TestDiagnosticsStoreDoesNotCrossAgentScope(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "go.mod", "module scoped\n")
	target := writeDiagnosticsTestFile(t, root, "main.go", "package main\n")
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root})

	ctxA := scopedDiagnosticsTestContext(root, "agent-a", "thread-1")
	ctxB := scopedDiagnosticsTestContext(root, "agent-b", "thread-1")
	uri, _ := resolveDiagnosticsScopeForTarget(t, mgr, ctxA, target, "fp-a")
	publishDiagnosticMessage(t, mgr, uri, "agent-a-only")

	requireDiagnosticMessage(t, diagnosticsItemsForURI(t, mgr, ctxA, uri, "agent A"), "agent-a-only")
	requireNoDiagnosticItems(t, "agent B", diagnosticsItemsForURI(t, mgr, ctxB, uri, "agent B"))
}

func TestDiagnosticsDropsOldGeneration(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "go.mod", "module generation\n")
	target := writeDiagnosticsTestFile(t, root, "main.go", "package main\n")
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root})

	ctx := scopedDiagnosticsTestContext(root, "agent-generation", "thread-1")
	uri, _ := resolveDiagnosticsScopeForTarget(t, mgr, ctx, target, "fp-generation")

	oldGeneration := mgr.CurrentDiagnosticGeneration()
	currentGeneration := mgr.AdvanceDiagnosticGeneration()
	if currentGeneration <= oldGeneration {
		t.Fatalf("AdvanceDiagnosticGeneration() = %d, want > %d", currentGeneration, oldGeneration)
	}

	if err := mgr.publishDiagnosticsForGeneration(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "old-generation",
		}},
	}, oldGeneration); err != nil {
		t.Fatalf("publish old generation diagnostics: %v", err)
	}
	requireNoDiagnosticItems(t, "old generation", diagnosticsItemsForURI(t, mgr, ctx, uri, "old generation"))

	if err := mgr.publishDiagnosticsForGeneration(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "current-generation",
		}},
	}, currentGeneration); err != nil {
		t.Fatalf("publish current generation diagnostics: %v", err)
	}
	items := diagnosticsItemsForURI(t, mgr, ctx, uri, "current generation")
	requireDiagnosticMessage(t, items, "current-generation")
}

func TestDiagnosticsRefreshesStaleFileBeforeReturn(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"diagnostics-stale"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", "function staleName() { return 1; }\n")

	factory := &diagnosticsRefreshClientFactory{}
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})

	ctx := scopedDiagnosticsTestContext(root, "agent-stale", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("bootstrap stale document: %v", err)
	}
	client := factory.currentClient()
	if client == nil {
		t.Fatal("expected bootstrap to create a refresh client")
	}
	publishDiagnosticMessage(t, mgr, uri, "stale-diagnostic")

	if err := os.WriteFile(target, []byte("function freshName() { return 2; }\n"), 0o600); err != nil {
		t.Fatalf("write fresh app.js: %v", err)
	}
	items := diagnosticsItemsForURI(t, mgr, ctx, uri, "after stale file edit")
	if got := client.changeCount(); got == 0 {
		t.Fatalf("Diagnostics did not refresh stale file before return; returned %#v", items)
	}
	requireEmptyDiagnosticSnapshot(t, "after stale file refresh", items)
}

func TestJSTSDiagnosticsDoesNotReturnStaleSnapshotWithoutBootstrapCache(t *testing.T) {
	for _, tc := range []struct {
		languageID string
		fileName   string
		initial    string
		fresh      string
	}{
		{languageID: "javascript", fileName: "CreateBacktestModal.js", initial: "export const payload = { name: 'demo' };\n", fresh: "export const payload = { name: 'demo', schema_version: 1 };\n"},
		{languageID: "typescript", fileName: "CreateBacktestModal.ts", initial: "type CreateBacktestParams = { name: string };\nconst payload: CreateBacktestParams = { name: 'demo', schema_version: 1 };\n", fresh: "type CreateBacktestParams = { name: string; schema_version: number };\nconst payload: CreateBacktestParams = { name: 'demo', schema_version: 1 };\n"},
		{languageID: "javascriptreact", fileName: "CreateBacktestModal.jsx", initial: "import React from 'react';\nexport function CreateBacktestModal() { return <form data-schema=\"old\" />; }\n", fresh: "import React from 'react';\nexport function CreateBacktestModal() { return <form data-schema=\"1\" />; }\n"},
		{languageID: "typescriptreact", fileName: "CreateBacktestModal.tsx", initial: strings.Join([]string{
			"import React, { FormEvent } from 'react';",
			"type CreateBacktestParams = { name: string };",
			"const payload: CreateBacktestParams = { name: 'demo', schema_version: 1 };",
			"export function CreateBacktestModal() { return <form />; }",
			"",
		}, "\n"), fresh: strings.Join([]string{
			"import type { FormEvent } from 'react';",
			"type CreateBacktestParams = { name: string; schema_version: number };",
			"const payload: CreateBacktestParams = { name: 'demo', schema_version: 1 };",
			"export function CreateBacktestModal() { return <form />; }",
			"",
		}, "\n")},
	} {
		t.Run(tc.languageID, func(t *testing.T) {
			runJSTSStaleDiagnosticsRepro(t, tc.languageID, tc.fileName, tc.initial, tc.fresh)
		})
	}
}

func runJSTSStaleDiagnosticsRepro(t *testing.T, languageID, fileName, initial, fresh string) {
	t.Helper()
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"jsts-diagnostics-stale"}`)
	target := writeDiagnosticsTestFile(t, root, fileName, initial)
	factory := &diagnosticsRefreshClientFactory{}
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1})
	ctx := scopedDiagnosticsTestContext(root, "agent-"+languageID+"-stale", "thread-1")
	uri := fileURIFromPath(target)
	publishDiagnosticMessage(t, mgr, uri, "schema_version does not exist in type CreateBacktestParams")

	if err := os.WriteFile(target, []byte(fresh), 0o600); err != nil {
		t.Fatalf("write fresh %s file: %v", languageID, err)
	}
	items := diagnosticsItemsForURI(t, mgr, ctx, uri, "after "+languageID+" file edit")
	client := factory.currentClient()
	if client == nil {
		t.Fatalf("expected diagnostics refresh to create a %s client", languageID)
	}
	if got := client.changeCount(); got == 0 {
		t.Fatalf("Diagnostics refreshed %s via open-only path; stale diagnostics returned %#v", languageID, items)
	}
	requireEmptyDiagnosticSnapshot(t, "after "+languageID+" stale refresh", items)
}

func TestDeletedDiagnosticsCleanupRemovesOldAndCurrentScopedCache(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "go.mod", "module cleanup\n")
	target := writeDiagnosticsTestFile(t, root, "stale.go", "package cleanup\n")
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root})
	coordinator := mustBootstrapCoordinator(t, mgr)
	ctxOld := scopedDiagnosticsTestContext(root, "agent-old", "thread-1")
	ctxCurrent := scopedDiagnosticsTestContext(root, "agent-current", "thread-1")
	uri, oldScope := resolveDiagnosticsScopeForTarget(t, mgr, ctxOld, target, "old")
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
	publishDiagnosticMessage(t, mgr, uri, "stale old scope")

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	items := diagnosticsItemsForURI(t, mgr, ctxCurrent, uri, "deleted")
	requireNoDiagnosticItems(t, "deleted", items)
	if _, ok := coordinator.cache.Load(oldKey); ok {
		t.Fatalf("old scoped cache key survived deleted-file cleanup")
	}
	if _, ok := coordinator.cache.Load(currentKey); ok {
		t.Fatalf("current scoped cache key survived deleted-file cleanup")
	}
	requireBootstrapPending(t, coordinator, oldScope, uri, "old")
	requireBootstrapPending(t, coordinator, currentScope, uri, "current")
}

func TestDiagnosticsClearsDeletedFile(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "go.mod", "module deleted\n")
	target := writeDiagnosticsTestFile(t, root, "deleted.go", "package deleted\n")
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root})

	ctx := scopedDiagnosticsTestContext(root, "agent-deleted", "thread-1")
	uri, _ := resolveDiagnosticsScopeForTarget(t, mgr, ctx, target, "fp-deleted")
	publishDiagnosticMessage(t, mgr, uri, "deleted-file")
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	items := diagnosticsItemsForURI(t, mgr, ctx, uri, "after delete")
	requireNoDiagnosticItems(t, "after delete", items)
	mgr.diagMu.RLock()
	defer mgr.diagMu.RUnlock()
	for key, snapshot := range mgr.diagnostics {
		if snapshot.uri == uri {
			t.Fatalf("diagnostic snapshot %q survived deleted-file cleanup: %#v", key, snapshot)
		}
	}
}

func canonicalDeleteScope(root, label string) ResolvedLSPToolScope {
	return ResolvedLSPToolScope{
		LSPToolScope: LSPToolScope{
			AgentID:               "agent-" + label,
			ThreadID:              "thread-" + label,
			Family:                defaultLSPToolFamily,
			LanguageID:            "go",
			WorkspaceRoot:         root,
			LanguageWorkspaceRoot: root,
			ProjectRoot:           root,
			RootKind:              goRootKindGoMod,
			LanguageSpecific:      map[string]string{"canonical": label},
		},
		ScopeKey:     "canonical-scope-" + label,
		WorkspaceKey: "canonical-workspace-" + label,
		ShardKey:     "canonical-shard-" + label,
		ManagerKey:   "canonical-manager-" + label,
	}
}

func assertDeletedCacheKey(t *testing.T, coordinator *bootstrapCoordinator, name string, key lspCacheKey) {
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

func TestDeletedFileClearsBootstrapAndCache(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "go.mod", "module canonicaldelete\n")
	target := writeDiagnosticsTestFile(t, root, "canonical.go", "package canonicaldelete\n")
	uri := fileURIFromPath(target)

	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root})
	coordinator := mustBootstrapCoordinator(t, mgr)

	oldScope := canonicalDeleteScope(root, "old")
	currentScope := canonicalDeleteScope(root, "current")
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
	ctx := WithResolvedLSPToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), currentScope)
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("diagnostics after canonical delete: %v", err)
	}
	requireNoDiagnosticItems(t, "deleted file", items)

	assertDeletedCacheKey(t, coordinator, "old LastResolvedScope", oldKey)
	assertDeletedCacheKey(t, coordinator, "current ResolvedLSPToolScope", currentKey)

	requireBootstrapPending(t, coordinator, oldScope, uri, "old")
	requireBootstrapPending(t, coordinator, currentScope, uri, "current")
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

	ctxTrusted := scopedDiagnosticsTestContext(root, "agent-a", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctxTrusted, fileURIFromPath(target), "")
	if err != nil {
		t.Fatalf("resolve trusted scope: %v", err)
	}
	mustBootstrapCoordinator(t, mgr).cache.RememberDocumentScope(ref.uri, scope, "trusted")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: ref.uri,
		Diagnostics: []protocol.Diagnostic{{
			Severity: protocol.SeverityError,
			Message:  "trusted-scope-only",
		}},
	}); err != nil {
		t.Fatalf("publish diagnostics: %v", err)
	}

	ctxPrivateOnly := context.WithValue(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), "_agentId", "agent-a")
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
	ctx := WithResolvedLSPToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), canonical)

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

	ctx := lspmanager.WithResolvedToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), lspmanager.ResolvedToolScope{
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

func TestDefaultDiagnosticsMaxWaitIsOnePointFiveSeconds(t *testing.T) {
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: t.TempDir()})

	if got := mgr.diagMaxWait; got != 1500*time.Millisecond {
		t.Fatalf("default diagnostics max wait = %s, want 1.5s", got)
	}
}

func TestWaitDiagnosticsStableFailsWhenTargetNeverPublishes(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"missing-diagnostics"}`)
	target := writeDiagnosticsTestFile(t, root, "app.js", "const value = 1\n")
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:           root,
		DiagnosticsInitialDelay: time.Millisecond,
		DiagnosticsPollInterval: time.Millisecond,
		DiagnosticsMaxWait:      time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), time.Second)
	defer cancel()
	uri := fileURIFromPath(target)

	err := mgr.WaitDiagnosticsStable(ctx, []string{uri})
	if err == nil || !strings.Contains(err.Error(), "diagnostics") || !strings.Contains(err.Error(), "app.js") {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want no publish failure for %s", err, uri)
	}
}

func TestWaitDiagnosticsStableFailsWhenAnyRequestedTargetNeverPublishes(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"partial-diagnostics"}`)
	first := writeDiagnosticsTestFile(t, root, "one.js", "const one = 1\n")
	second := writeDiagnosticsTestFile(t, root, "two.js", "const two = 2\n")
	mgr := newDiagnosticsTestManager(t, Config{
		WorkspaceRoot:           root,
		DiagnosticsInitialDelay: time.Millisecond,
		DiagnosticsPollInterval: time.Millisecond,
		DiagnosticsMaxWait:      time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}}), time.Second)
	defer cancel()
	firstURI := fileURIFromPath(first)
	secondURI := fileURIFromPath(second)
	resolveDiagnosticsScopeForTarget(t, mgr, ctx, first, "first")
	resolveDiagnosticsScopeForTarget(t, mgr, ctx, second, "second")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: firstURI}); err != nil {
		t.Fatalf("PublishDiagnostics(first) error = %v", err)
	}

	err := mgr.WaitDiagnosticsStable(ctx, []string{firstURI, secondURI})
	if err == nil || !strings.Contains(err.Error(), secondURI) {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want missing second URI", err)
	}
}

func TestPublishEmptyDiagnosticsCountsAsObservedReadySnapshot(t *testing.T) {
	root := t.TempDir()
	writeDiagnosticsTestFile(t, root, "package.json", `{"name":"empty-diagnostics"}`)
	target := writeDiagnosticsTestFile(t, root, "empty.js", "const empty = 1\n")
	mgr := newDiagnosticsTestManager(t, Config{WorkspaceRoot: root, DiagnosticsMaxWait: time.Millisecond})
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root, WorkspaceRoots: []string{root}})
	uri := fileURIFromPath(target)
	resolveDiagnosticsScopeForTarget(t, mgr, ctx, target, "empty")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri}); err != nil {
		t.Fatalf("PublishDiagnostics() error = %v", err)
	}
	if err := mgr.WaitDiagnosticsStable(ctx, []string{uri}); err != nil {
		t.Fatalf("WaitDiagnosticsStable() error = %v, want observed empty diagnostics success", err)
	}
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("Diagnostics() error = %v", err)
	}
	if len(items) != 1 || len(items[0].Diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %#v, want one empty ready item", items)
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

func scopedDiagnosticsTestContext(root, agentID, threadID string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  agentID,
		ThreadID: threadID,
		Family:   defaultLSPToolFamily,
		CWD:      root,
	})
}
