package multilsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestDiagnosticsAllRefreshesStaleScopedDiagnosticBeforeReturn(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-stale"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "function staleName() { return 1; }\n")
	factory := &diagnosticsRefreshClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-stale-all", "thread-1")
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
	requireEmptyDiagnosticSnapshot(t, "Diagnostics(all)", items)
}

func TestDiagnosticsAllBootstrapsUntrackedExistingDiagnosticURI(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-cache"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2DiagnosticsFactory{publishOnOpen: "bootstrapped"}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-cache", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "typescript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	mustBootstrapCoordinator(t, mgr).cache.Upsert(lspCacheValue{Key: scope.cacheKey("typescript", ref.uri), Version: 1, UpdatedAt: time.Now()})
	mustBootstrapCoordinator(t, mgr).cache.RememberDocumentScope(ref.uri, scope, "fp")

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
	ctx := scopedDiagnosticsTestContext(root, "agent-deleted-all", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "go")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("go", ref.uri)
	mustBootstrapCoordinator(t, mgr).cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()})
	mustBootstrapCoordinator(t, mgr).cache.RememberDocumentScope(ref.uri, scope, "fp")
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
	if _, ok := mustBootstrapCoordinator(t, mgr).cache.Load(key); ok {
		t.Fatalf("deleted file cache key still loads after Diagnostics(all)")
	}
	mustBootstrapCoordinator(t, mgr).cache.mu.RLock()
	_, tombstoned := mustBootstrapCoordinator(t, mgr).cache.tombstones[key.String()]
	mustBootstrapCoordinator(t, mgr).cache.mu.RUnlock()
	if !tombstoned {
		t.Fatalf("deleted file cache key was not tombstoned")
	}
}

func TestDiagnosticsAllDeletedFilePropagatesPersistentTombstoneError(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module deletedwrite\n\ngo 1.25.0\n")
	target := filepath.Join(root, "deleted.go")
	writeGenericTestFile(t, target, "package deletedwrite\n")
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-deleted-write", "thread-1")
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, fileURIFromPath(target), "go")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("go", ref.uri)
	coordinator := mustBootstrapCoordinator(t, mgr)
	if err := coordinator.cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := coordinator.cache.RememberDocumentScope(ref.uri, scope, "fp"); err != nil {
		t.Fatalf("remember scope: %v", err)
	}
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: ref.uri, Diagnostics: []protocol.Diagnostic{{Message: "deleted"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	makeCacheDirUnwritableForTest(t, cacheDir)

	_, err = mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), nil)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") {
		t.Fatalf("Diagnostics(all) error = %v, want persistent cache tombstone failure", err)
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
	ctx := scopedDiagnosticsTestContext(root, "agent-change", "thread-1")
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
	record, ok := mustBootstrapCoordinator(t, mgr).cache.Load(scope.cacheKey("javascript", ref.uri))
	if !ok {
		t.Fatalf("cache record missing after full disk-backed DidChange")
	}
	if record.Version != 7 || record.Fingerprint != hashDocument([]byte(nextText)) {
		t.Fatalf("cache record = %#v, want version 7 and changed fingerprint", record)
	}
	if got := mustBootstrapCoordinator(t, mgr).states.status(scope.bootstrapKey(), ref.uri); got != bootstrapReady {
		t.Fatalf("bootstrap state = %s, want ready after full DidChange", got)
	}
}

func TestDiagnosticsDiscardStaleNilVersionAfterFullDidChange(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-nil-version"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-nil-version", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, uri, "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: uri,
		Diagnostics: []protocol.Diagnostic{{
			Message: "stale nil-version",
		}},
	}); err != nil {
		t.Fatalf("PublishDiagnostics(nil version): %v", err)
	}
	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), []string{uri})
	if err != nil {
		t.Fatalf("Diagnostics(before DidChange): %v", err)
	}
	if len(items) != 1 || len(items[0].Diagnostics) != 1 {
		t.Fatalf("Diagnostics(before DidChange) = %#v, want seeded nil-version diagnostic", items)
	}

	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	if err := mgr.DidChange(ctx, target, 7, []protocol.TextDocumentContentChangeEvent{{Text: nextText}}); err != nil {
		t.Fatalf("DidChange(full): %v", err)
	}
	items, err = mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), []string{ref.uri})
	if err != nil {
		t.Fatalf("Diagnostics(after DidChange): %v", err)
	}
	requireNoDiagnosticItems(t, "nil-version publish before full DidChange", items)
}

func TestPreChangeEpochNilVersionPublishAfterFullDidChangeIsIgnored(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-pre-change-epoch"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-pre-change-epoch", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, uri, "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	firstClient := factory.clientAt(t, 0)
	capturer, ok := firstClient.handler.(interface {
		capturePublishDiagnostics(protocol.PublishDiagnosticsParams) (capturedPublishDiagnostics, error)
	})
	if !ok {
		t.Fatalf("handler %T does not expose captured diagnostic epoch", firstClient.handler)
	}
	latePublish, err := capturer.capturePublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI: ref.uri,
		Diagnostics: []protocol.Diagnostic{{
			Message: "late pre-change nil-version",
		}},
	})
	if err != nil {
		t.Fatalf("capturePublishDiagnostics: %v", err)
	}

	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	if err := mgr.DidChange(ctx, target, 7, []protocol.TextDocumentContentChangeEvent{{Text: nextText}}); err != nil {
		t.Fatalf("DidChange(full): %v", err)
	}

	publisher, ok := firstClient.handler.(interface {
		publishCapturedDiagnostics(capturedPublishDiagnostics) error
	})
	if !ok {
		t.Fatalf("handler %T does not publish captured diagnostics", firstClient.handler)
	}
	if err := publisher.publishCapturedDiagnostics(latePublish); err != nil {
		t.Fatalf("publishCapturedDiagnostics: %v", err)
	}
	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, scope), []string{ref.uri})
	if err != nil {
		t.Fatalf("Diagnostics(after late publish): %v", err)
	}
	requireNoDiagnosticItems(t, "pre-change epoch nil-version publish after full DidChange", items)
}

func TestPublishDiagnosticsIgnoresOlderDocumentVersion(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-version"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-version", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, uri, "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("javascript", ref.uri)
	firstRecord := requireCacheRecord(t, mgr, key, "initial")

	writeGenericTestFile(t, target, "let value = 2\n")
	if _, err := mgr.Diagnostics(ctx, []string{uri}); err != nil {
		t.Fatalf("Diagnostics(refresh changed file): %v", err)
	}
	changedRecord := requireCacheRecord(t, mgr, key, "changed")
	if got := factory.clientAt(t, 0).didChangeCount(); got == 0 {
		t.Fatalf("Diagnostics did not refresh changed file before accepting diagnostics")
	}
	if changedRecord.Version <= firstRecord.Version {
		t.Fatalf("changed cache record = %#v, want newer than %#v", changedRecord, firstRecord)
	}

	oldVersion := firstRecord.Version
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI:     uri,
		Version: &oldVersion,
		Diagnostics: []protocol.Diagnostic{{
			Message: "stale-v1",
		}},
	}); err != nil {
		t.Fatalf("PublishDiagnostics(old version): %v", err)
	}
	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("Diagnostics(after old publish): %v", err)
	}
	requireNoDiagnosticItems(t, "old-version publish", items)
}

func TestPublishDiagnosticsDropsOlderVersionPublishedBeforeCacheRefresh(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"diagnostics-version-race"}`)
	target := filepath.Join(root, "app.js")
	writeGenericTestFile(t, target, "let value = 1\n")
	factory := &p2DiagnosticsFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-version-race", "thread-1")
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	ref, _, scope, err := mgr.resolvedScopeForURI(ctx, uri, "javascript")
	if err != nil {
		t.Fatalf("resolvedScopeForURI: %v", err)
	}
	key := scope.cacheKey("javascript", ref.uri)
	firstRecord := requireCacheRecord(t, mgr, key, "initial")

	writeGenericTestFile(t, target, "let value = 2\n")
	oldVersion := firstRecord.Version
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{
		URI:     uri,
		Version: &oldVersion,
		Diagnostics: []protocol.Diagnostic{{
			Message: "stale-before-cache-refresh",
		}},
	}); err != nil {
		t.Fatalf("PublishDiagnostics(old before cache refresh): %v", err)
	}

	items, err := mgr.Diagnostics(ctx, []string{uri})
	if err != nil {
		t.Fatalf("Diagnostics(refresh changed file): %v", err)
	}
	if got := factory.clientAt(t, 0).didChangeCount(); got == 0 {
		t.Fatalf("Diagnostics did not refresh changed file before returning diagnostics")
	}
	changedRecord := requireCacheRecord(t, mgr, key, "changed")
	if changedRecord.Version <= oldVersion {
		t.Fatalf("changed cache record = %#v, want version newer than %d", changedRecord, oldVersion)
	}
	requireNoDiagnosticItems(t, "old-version publish before cache refresh", items)
}

func requireCacheRecord(t *testing.T, mgr *manager, key lspCacheKey, label string) lspCacheValue {
	t.Helper()
	record, ok := mustBootstrapCoordinator(t, mgr).cache.Load(key)
	if !ok {
		t.Fatalf("%s cache record missing for key %#v", label, key)
	}
	return record
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
	if err := mgr.DidChange(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target, 2, []protocol.TextDocumentContentChangeEvent{{Range: &rng, Text: "var"}}); err != nil {
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
	if err := mgr.DidChange(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target, 2, []protocol.TextDocumentContentChangeEvent{{Text: "let value = 1\n"}}); err != nil {
		t.Fatalf("DidChange(memory-only): %v", err)
	}
	assertPersistentCacheDocumentCount(t, cacheDir, 0)
}

func TestManagedDidChangeFailureIsFailFastWithoutReopenOrRestart(t *testing.T) {
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
	if err := mgr.BootstrapDocument(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target); err != nil {
		t.Fatalf("BootstrapDocument: %v", err)
	}
	nextText := "let value = 2\n"
	writeGenericTestFile(t, target, nextText)
	err := mgr.DidChange(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, 3, []protocol.TextDocumentContentChangeEvent{{Text: nextText}})
	if err == nil || !strings.Contains(err.Error(), "change failed") {
		t.Fatalf("managed DidChange failure = %v, want explicit wire error", err)
	}
	if factory.callCount() != 1 {
		t.Fatalf("factory calls = %d, want no restart", factory.callCount())
	}
	client := factory.clientAt(t, 0)
	if client.openCount() != 1 || client.didCloseCount() != 0 {
		t.Fatalf("managed failure replayed lifecycle notifications: opens=%d closes=%d", client.openCount(), client.didCloseCount())
	}
}

func TestManagedDidChangeFailureDoesNotReconnectOrReplayWorkspace(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"restore-workspace"}`)
	changed := filepath.Join(root, "app.js")
	peer := filepath.Join(root, "peer.js")
	writeGenericTestFile(t, changed, "let value = 1\n")
	writeGenericTestFile(t, peer, "let peer = 1\n")
	factory := &p2DiagnosticsFactory{
		didChangeErrors: []error{errors.New("change failed")},
		didCloseErrors:  []error{ErrTransportClosed},
	}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := scopedDiagnosticsTestContext(root, "agent-restore", "thread-1")
	if err := mgr.BootstrapDocument(ctx, changed); err != nil {
		t.Fatalf("BootstrapDocument(changed): %v", err)
	}
	if err := mgr.BootstrapDocument(ctx, peer); err != nil {
		t.Fatalf("BootstrapDocument(peer): %v", err)
	}

	nextText := "let value = 2\n"
	writeGenericTestFile(t, changed, nextText)
	err := mgr.DidChange(ctx, changed, 3, []protocol.TextDocumentContentChangeEvent{{Text: nextText}})
	if err == nil || !strings.Contains(err.Error(), "change failed") {
		t.Fatalf("managed workspace DidChange failure = %v, want explicit wire error", err)
	}
	if factory.callCount() != 1 {
		t.Fatalf("factory calls = %d, want no reconnect", factory.callCount())
	}
	client := factory.clientAt(t, 0)
	if client.openCount() != 2 || client.didCloseCount() != 0 {
		t.Fatalf("managed workspace failure replayed lifecycle notifications: opens=%d closes=%d", client.openCount(), client.didCloseCount())
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
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
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
	if err := mgr.BootstrapDocumentOpenOnly(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly(first): %v", err)
	}
	if err := mgr.DidClose(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	if err := mgr.BootstrapDocumentOpenOnly(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target); err != nil {
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
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
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
	if _, ok := mustBootstrapCoordinator(t, mgr).cache.Load(key); !ok {
		t.Fatalf("existing file cache was removed by DidClose")
	}
	mustBootstrapCoordinator(t, mgr).cache.mu.RLock()
	_, tombstoned := mustBootstrapCoordinator(t, mgr).cache.tombstones[key.String()]
	mustBootstrapCoordinator(t, mgr).cache.mu.RUnlock()
	if tombstoned {
		t.Fatalf("existing file was tombstoned by DidClose")
	}
}

func TestBootstrapBootstrappingTimeoutAllowsRetry(t *testing.T) {
	store := newBootstrapStateStore()
	wait := newBootstrapWait()
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
	case <-wait.done:
	default:
		t.Fatalf("stale bootstrapping waiter was not released")
	}
}

func TestPersistentCacheCorruptFileFailsFast(t *testing.T) {
	runPersistentCacheCorruptFileReturnsLoadError(t)
}

func TestPersistentCacheCorruptFileReturnsLoadError(t *testing.T) {
	runPersistentCacheCorruptFileReturnsLoadError(t)
}

func runPersistentCacheCorruptFileReturnsLoadError(t *testing.T) {
	t.Helper()
	cacheDir := t.TempDir()
	t.Setenv(lspCachePersistentEnv, "1")
	t.Setenv(lspCacheDirEnv, cacheDir)
	path := filepath.Join(cacheDir, lspCacheFileName)
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}
	_, err := newLSPCacheStoreFromEnv(nil)
	if err == nil || !strings.Contains(err.Error(), "persistent cache") || !strings.Contains(err.Error(), "load") {
		t.Fatalf("newLSPCacheStoreFromEnv() error = %v, want persistent load failure", err)
	}
}

func TestPersistentCacheWritesAtomically(t *testing.T) {
	root, cacheDir := setupPersistentCacheEnv(t)
	store := mustLSPCacheStoreFromEnv(t)
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

	_ = mustLSPCacheStoreFromEnv(t)
	disk := readCacheDiskState(t, cacheDir)
	if len(disk.Documents) != 1 || disk.Documents[0].Key.URI != "file:///fresh.go" {
		t.Fatalf("persisted cache documents = %#v, want only fresh entry", disk.Documents)
	}
}

func TestDeletedPersistentCacheRecordDoesNotResurrectAfterRestart(t *testing.T) {
	_, cacheDir := setupPersistentCacheEnv(t)
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	key := scope.cacheKey("go", "file:///deleted.go")
	store := mustLSPCacheStoreFromEnv(t)
	store.Upsert(lspCacheValue{Key: key, Version: 1})
	store.Tombstone(key)
	restarted := mustLSPCacheStore(t, lspCacheConfig{Persistent: true, Dir: cacheDir})
	if _, ok := restarted.Load(key); ok {
		t.Fatalf("deleted persistent cache record resurrected after restart")
	}
}

func TestDiagnosticsAfterServerExitDoesNotReturnOldGeneration(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	uri := "file:///repo/stale.go"
	if err := mgr.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "old"}}}); err != nil {
		t.Fatalf("PublishDiagnostics: %v", err)
	}
	mgr.AdvanceDiagnosticGeneration()
	items, err := mgr.Diagnostics(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: mgr.workspaceRoot}), common.ToolScope{CWD: root}), nil)
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

	items, err := mgr.Diagnostics(WithResolvedLSPToolScope(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: mgr.workspaceRoot}), common.ToolScope{CWD: root}), tsScope), nil)
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
	store := mustLSPCacheStoreFromEnv(t)
	store.Upsert(lspCacheValue{Key: goKey, Version: 1})
	store.Upsert(lspCacheValue{Key: tsKey, Version: 2})
	store.Tombstone(tsKey)

	restarted := mustLSPCacheStore(t, lspCacheConfig{Persistent: true, Dir: cacheDir})
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
			if err := mgr.BootstrapDocument(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target); err != nil {
				t.Fatalf("BootstrapDocument(%s): %v", tc.languageID, err)
			}
			client := factory.clientAt(t, 0)
			if !client.opened(fileURIFromPath(target), tc.languageID) {
				t.Fatalf("%s target was not opened during bootstrap; opens=%#v", tc.languageID, client.openEvents())
			}
			indexed, ok := mustBootstrapCoordinator(t, mgr).cache.LastResolvedScope(fileURIFromPath(target))
			if !ok {
				t.Fatalf("%s bootstrap did not remember resolved scope", tc.languageID)
			}
			if indexed.LastResolvedScope.LanguageID != tc.languageID || indexed.LastResolvedScope.WorkspaceKey == "" {
				t.Fatalf("%s resolved scope = %#v", tc.languageID, indexed.LastResolvedScope)
			}
		})
	}
}
