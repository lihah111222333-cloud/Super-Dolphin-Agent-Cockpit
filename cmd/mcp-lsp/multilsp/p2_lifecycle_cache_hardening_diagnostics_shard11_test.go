package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func TestDiagnosticsAllRefreshesStaleScopedDiagnosticBeforeReturn(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-stale"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "function staleName() { return 1; }\n")
	factory := &diagnosticsRefreshClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-stale-all", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "stale"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	writeGenericTestFile(t, target, "function freshName() { return 2; }\n")

	items, err := mgr.Diagnostics(ctx, nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if got := factory.currentClient().changeCount(); got == 0 {
		t.Fatalf("Diagnostics(all) did not refresh stale scoped diagnostic; returned %#v", items)
	}
	if len(items) != 0 {
		t.Fatalf("Diagnostics(all) = %#v, want empty after refresh cleared stale diagnostics", items)
	}
}

func TestDiagnosticsAllBootstrapsUntrackedExistingDiagnosticURI(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-cache"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2DiagnosticsFactory{publishOnOpen: "bootstrapped"}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-cache", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "typescript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	bootstrapCoordinatorFor(mgr).cache.Upsert(lspCacheValue{Key: scope.cacheKey("typescript", ref.uri), Version: 1, UpdatedAt: time.Now()})
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "fp")

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(items) != 1 || len(items[0].Diagnostics) != 1 || items[0].Diagnostics[0].Message != "bootstrapped" {
		t.Fatalf("Diagnostics(all) = %#v, want bootstrapped diagnostic from cached URI", items)
	}
}

func TestDiagnosticsAllDeletedFileClearsDiagnosticsAndTombstones(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module deletedall\n\ngo 1.25.0\n")
	target := filepath.Join(root, "deleted.go")
	writeGenericTestFile(t, target, "package deletedall\n")
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-deleted-all", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "go")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("go", ref.uri)
	bootstrapCoordinatorFor(mgr).cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()})
	bootstrapCoordinatorFor(mgr).cache.RememberDocumentScope(ref.uri, scope, "fp")
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: ref.uri, Diagnostics: []protocol.Diagnostic{{Message: "deleted"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Diagnostics(all) = %#v, want deleted diagnostic cleared", items)
	}
	if _, ok := bootstrapCoordinatorFor(mgr).cache.Load(key); ok {
		t.Fatalf("deleted file cache key still loads after Diagnostics(all)")
	}
	bootstrapCoordinatorFor(mgr).cache.mu.RLock()
	_, tombstoned := bootstrapCoordinatorFor(mgr).cache.tombstones[key.String()]
	bootstrapCoordinatorFor(mgr).cache.mu.RUnlock()
	if !tombstoned {
		t.Fatalf("deleted file cache key was not tombstoned")
	}
}

func TestDidChangeAdvancesBootstrapCacheVersionForFullDiskBackedText(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"change"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext("agent-change", "thread-1")
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	if err := mgr.DidChange(ctx, target, 7, []protocol.TextDocumentContentChangeEvent{{Text: nextText}}); err != nil {
		t.Fatalf("DidChange(full): %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	record, ok := bootstrapCoordinatorFor(mgr).cache.Load(scope.cacheKey("javascript", ref.uri))
	if !ok {
		t.Fatalf("cache record missing after full disk-backed DidChange")
	}
	if record.Version != 7 || record.Fingerprint != hashDocument([]byte(nextText)) {
		t.Fatalf("cache record = %#v, want version 7 and changed fingerprint", record)
	}
	if got := bootstrapCoordinatorFor(mgr).states.status(scope.bootstrapKey(), ref.uri); got != bootstrapReady {
		t.Fatalf("bootstrap state = %s, want ready after full DidChange", got)
	}
}

func TestIncrementalDidChangeDoesNotWritePersistentCache(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"incremental"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	rng := protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: 3}}
	if err := mgr.DidChange(context.Background(), target, 2, []protocol.TextDocumentContentChangeEvent{{Range: &rng, Text: "var"}}); err != nil {
		t.Fatalf("DidChange(incremental): %v", err)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 0)
}

func TestMemoryOnlyDidChangeDoesNotWritePersistentCache(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"memory"}`)
	target := filepath.Join(root, "virtual.js")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.DidChange(context.Background(), target, 2, []protocol.TextDocumentContentChangeEvent{{Text: "let value = 1\n"}}); err != nil {
		t.Fatalf("DidChange(memory-only): %v", err)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 0)
}

func TestDidChangeFailureFallsBackToReopenThenRestart(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"fallback"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{
		didChangeErrors: []error{errors.New("change failed")},
		didCloseErrors:  []error{ErrTransportClosed},
	}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.BootstrapDocument(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	if err := mgr.DidChange(context.Background(), target, 3, []protocol.TextDocumentContentChangeEvent{{Text: nextText}}); err != nil {
		t.Fatalf("DidChange should reopen/restart after failure: %v", err)
	}
	if factory.callCount() < 2 {
		t.Fatalf("factory calls = %d, want restart after close/reopen failure", factory.callCount())
	}
	if !factory.clientAt(t, 1).opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("restarted client did not reopen changed document")
	}
}

func TestDidChangeDeadClientDoesNotAutoReplayNotify(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"dead-change"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{didChangeErrors: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := context.Background()
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}

	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	err := mgr.DidChange(ctx, target, 2, []protocol.TextDocumentContentChangeEvent{{Text: nextText}})
	if err == nil {
		t.Fatalf("DidChange error = nil, want retryable dead-client error without notify replay")
	}
	first := factory.clientAt(t, 0)
	if got := first.didChangeCount(); got != 1 {
		t.Fatalf("first client DidChange count = %d, want exactly one attempt", got)
	}
	if got := first.openCount(); got != 1 {
		t.Fatalf("first client DidOpen count = %d, want no reopen replay on dead DidChange", got)
	}
	if !first.closed {
		t.Fatalf("dead DidChange client was not closed/detached")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want repair rebuild without replay", got)
	}
	second := factory.clientAt(t, 1)
	if got := second.didChangeCount(); got != 0 {
		t.Fatalf("rebuilt client DidChange count = %d, want no automatic DidChange replay", got)
	}
	if !second.opened(fileURIFromPath(target), "javascript") {
		t.Fatalf("rebuilt client did not restore safe bootstrap open; opens=%#v", second.opens)
	}
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("DidChange error = %v, want retryable ErrClientClosed marker", err)
	}
}

func TestDidCloseClearsBootstrapReadyAndNextOpenReopens(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"close"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.BootstrapDocumentOpenOnly(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly(first): %v", err)
	}
	if err := mgr.DidClose(context.Background(), target); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if err := mgr.BootstrapDocumentOpenOnly(context.Background(), target); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly(second): %v", err)
	}
	if got := factory.clientAt(t, 0).openCount(); got != 2 {
		t.Fatalf("DidOpen count = %d, want 2 after DidClose cleared ready state", got)
	}
}

func TestDidCloseDoesNotTombstoneExistingFile(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"close"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := context.Background()
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("javascript", ref.uri)
	if err := mgr.DidClose(ctx, target); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if _, ok := bootstrapCoordinatorFor(mgr).cache.Load(key); !ok {
		t.Fatalf("existing file cache was removed by DidClose")
	}
	bootstrapCoordinatorFor(mgr).cache.mu.RLock()
	_, tombstoned := bootstrapCoordinatorFor(mgr).cache.tombstones[key.String()]
	bootstrapCoordinatorFor(mgr).cache.mu.RUnlock()
	if tombstoned {
		t.Fatalf("existing file was tombstoned by DidClose")
	}
}

func TestBootstrapBootstrappingTimeoutAllowsRetry(t *testing.T) {
	store := newBootstrapStateStore()
	wait := make(chan struct{})
	key := bootstrapKey{workspace: "workspace", uri: "file:///stuck.go"}
	store.entries[key] = &bootstrapEntry{
		status:    bootstrapBootstrapping,
		updatedAt: time.Now().Add(-time.Hour),
		wait:      wait,
	}
	decision := store.prepare(key.workspace, key.uri, "fp")
	if decision.action != bootstrapActionRun {
		t.Fatalf("prepare(stale bootstrapping) action = %v, want run retry", decision.action)
	}
	select {
	case <-wait:
	default:
		t.Fatalf("stale bootstrapping waiter was not released")
	}
}

func TestPersistentCacheCorruptFileQuarantinedAndRewritten(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	path := filepath.Join(cacheDir, lspCacheFileName)
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	store := newLSPCacheStoreFromEnv(nil)
	if !store.persistent || !store.persistentReady {
		t.Fatalf("persistent cache fell back to memory after corruption")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten cache: %v", err)
	}
	var disk lspCacheDiskState
	if err := json.Unmarshal(payload, &disk); err != nil {
		t.Fatalf("rewritten cache is not valid JSON: %s err=%v", payload, err)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, lspCacheFileName+".corrupt-*"))
	if err != nil {
		t.Fatalf("glob corrupt quarantine: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("corrupt quarantine files = %#v, want one", matches)
	}
}

func TestPersistentCacheWritesAtomically(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	store := newLSPCacheStoreFromEnv(nil)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	store.Upsert(lspCacheValue{Key: scope.cacheKey("go", fileURIFromPath(filepath.Join(root, "main.go"))), Version: 1})
	path := filepath.Join(cacheDir, lspCacheFileName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat persistent cache: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, lspCacheFileName+".tmp-*"))
	if err != nil {
		t.Fatalf("glob tmp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic cache write left tmp files: %#v", matches)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 1)
}

func TestPersistentCacheExpiredEntryFilteredAndPersisted(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	expired := lspCacheValue{Key: scope.cacheKey("go", "file:///expired.go"), Version: 1, UpdatedAt: time.Now().Add(-8 * 24 * time.Hour)}
	fresh := lspCacheValue{Key: scope.cacheKey("go", "file:///fresh.go"), Version: 1, UpdatedAt: time.Now()}
	writeCacheDiskState(t, cacheDir, lspCacheDiskState{Documents: []lspCacheValue{expired, fresh}})

	_ = newLSPCacheStoreFromEnv(nil)
	disk := readCacheDiskState(t, cacheDir)
	if len(disk.Documents) != 1 || disk.Documents[0].Key.URI != "file:///fresh.go" {
		t.Fatalf("persisted cache documents = %#v, want only fresh entry", disk.Documents)
	}
}

func TestDeletedPersistentCacheRecordDoesNotResurrectAfterRestart(t *testing.T) {
	_, cacheDir := setupPersistentCacheEnv(t)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	key := scope.cacheKey("go", "file:///deleted.go")
	store := newLSPCacheStoreFromEnv(nil)
	store.Upsert(lspCacheValue{Key: key, Version: 1})
	store.Tombstone(key)
	restarted := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: cacheDir})
	if _, ok := restarted.Load(key); ok {
		t.Fatalf("deleted persistent cache record resurrected after restart")
	}
}

func TestDiagnosticsAfterServerExitDoesNotReturnOldGeneration(t *testing.T) {
	mgr := NewManager(Config{WorkspaceRoot: canonicalScopePath(t.TempDir(), "")}).(*manager)
	uri := "file:///repo/stale.go"
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "old"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	mgr.AdvanceDiagnosticGeneration()
	items, err := mgr.Diagnostics(context.Background(), nil)
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("Diagnostics returned old-generation entries after server exit: %#v", items)
	}
}

func TestDiagnosticsAllDoesNotReturnCrossLanguageSameURI(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	sharedPath := filepath.Join(root, "shared.txt")
	writeGenericTestFile(t, sharedPath, "shared\n")
	uri := fileURIFromPath(sharedPath)
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	gen := mgr.CurrentDiagnosticGeneration()
	goScope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "go-workspace", LSPToolScope: LSPToolScope{LanguageID: "go", WorkspaceRoot: root}}
	tsScope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "ts-workspace", LSPToolScope: LSPToolScope{LanguageID: "typescript", WorkspaceRoot: root}}
	mgr.diagnostics[diagnosticStoreKeyFor(goScope, uri).String()] = diagnosticSnapshot{scopeKey: goScope.ScopeKey, workspaceKey: goScope.WorkspaceKey, language: "go", uri: uri, generation: gen, state: diagnosticStateReady, params: protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "go"}}}}
	mgr.diagnostics[diagnosticStoreKeyFor(tsScope, uri).String()] = diagnosticSnapshot{scopeKey: tsScope.ScopeKey, workspaceKey: tsScope.WorkspaceKey, language: "typescript", uri: uri, generation: gen, state: diagnosticStateReady, params: protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "ts"}}}}

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(context.Background(), tsScope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(items) != 1 || items[0].Diagnostics[0].Message != "ts" {
		t.Fatalf("Diagnostics(all) = %#v, want only TypeScript same-URI diagnostic", items)
	}
}

func TestDeletedPersistentCacheRecordDoesNotResurrectAcrossLanguages(t *testing.T) {
	_, cacheDir := setupPersistentCacheEnv(t)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	goKey := scope.cacheKey("go", "file:///shared")
	tsKey := scope.cacheKey("typescript", "file:///shared")
	store := newLSPCacheStoreFromEnv(nil)
	store.Upsert(lspCacheValue{Key: goKey, Version: 1})
	store.Upsert(lspCacheValue{Key: tsKey, Version: 2})
	store.Tombstone(tsKey)

	restarted := newLSPCacheStore(lspCacheConfig{Persistent: true, Dir: cacheDir})
	if _, ok := restarted.Load(tsKey); ok {
		t.Fatalf("deleted TypeScript cache record resurrected after restart")
	}
	if _, ok := restarted.Load(goKey); !ok {
		t.Fatalf("Go cache record was incorrectly removed by TypeScript tombstone")
	}
}

func TestBootstrapCacheMatrixForRegisteredLanguageIDs(t *testing.T) {
	for _, tc := range []struct {
		languageID string
		fileName   string
		markerName string
		markerBody string
	}{
		{languageID: "go", fileName: "main.go", markerName: "go.mod", markerBody: "module example.test/matrix\n\ngo 1.25.0\n"},
		{languageID: "javascript", fileName: "app.js", markerName: "package.json", markerBody: `{"name":"matrix"}`},
		{languageID: "typescript", fileName: "app.ts", markerName: "tsconfig.json", markerBody: `{"compilerOptions":{}}`},
		{languageID: "python", fileName: "app.py", markerName: "pyproject.toml", markerBody: "[project]\nname = \"matrix\"\n"},
		{languageID: "rust", fileName: "main.rs", markerName: "Cargo.toml", markerBody: "[package]\nname = \"matrix\"\nversion = \"0.1.0\"\n"},
		{languageID: "java", fileName: filepath.Join("src", "Main.java"), markerName: "pom.xml", markerBody: "<project></project>\n"},
		{languageID: "css", fileName: "style.css", markerName: "package.json", markerBody: `{"name":"matrix-css"}`},
	} {
		t.Run(tc.languageID, func(t *testing.T) {
			root := canonicalScopePath(t.TempDir(), "")
			writeGenericTestFile(t, filepath.Join(root, tc.markerName), tc.markerBody)
			target := filepath.Join(root, tc.fileName)
			writeGenericTestFile(t, target, "symbol\n")
			factory := &genericMatrixClientFactory{}
			mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
			t.Cleanup(func() { _ = mgr.Close() })
			if err := mgr.BootstrapDocument(context.Background(), target); err != nil {
				t.Fatalf("BootstrapDocument(%s): %v", tc.languageID, err)
			}
			client := factory.clientAt(t, 0)
			if !client.opened(fileURIFromPath(target), tc.languageID) {
				t.Fatalf("%s target was not opened during bootstrap; opens=%#v", tc.languageID, client.openEvents())
			}
			indexed, ok := bootstrapCoordinatorFor(mgr).cache.LastResolvedScope(fileURIFromPath(target))
			if !ok {
				t.Fatalf("%s bootstrap did not remember resolved scope", tc.languageID)
			}
			if indexed.LastResolvedScope.LanguageID != tc.languageID || indexed.LastResolvedScope.WorkspaceKey == "" {
				t.Fatalf("%s resolved scope = %#v", tc.languageID, indexed.LastResolvedScope)
			}
		})
	}
}
