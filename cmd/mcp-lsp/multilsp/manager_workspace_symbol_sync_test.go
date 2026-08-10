package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestWorkspaceSymbolSynchronizesDirectDidOpenDiskChangeBeforeQuery(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		name := "language_scope"
		if resolved {
			name = "resolved_file_scope"
		}
		t.Run(name, func(t *testing.T) {
			root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function StaleWorkspaceSymbol() {}\n")
			factory := &strictWorkspaceSymbolFactory{}
			mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
			t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
			ctx := workspaceSymbolSyncContext(t, root, target, "javascript", resolved)
			uri := fileURIFromPath(target)

			if err := mgr.DidOpen(ctx, uri, "javascript", 1, stale); err != nil {
				t.Fatalf("DidOpen stale document: %v", err)
			}
			fresh := "function FreshWorkspaceSymbol() {}\n"
			writeGenericTestFile(t, target, fresh)

			results, err := mgr.WorkspaceSymbol(ctx, "FreshWorkspaceSymbol", "javascript")
			if err != nil {
				t.Fatalf("WorkspaceSymbol after external rewrite: %v", err)
			}
			assertStrictWorkspaceSymbolResult(t, results, "FreshWorkspaceSymbol", uri)
			client := factory.clientContainingURI(t, uri)
			if got, want := client.eventsSnapshot(), []string{
				"open:" + uri + ":1",
				"change:" + uri + ":2",
				"request:FreshWorkspaceSymbol",
			}; strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("wire sequence = %#v, want %#v", got, want)
			}

			if _, err := mgr.WorkspaceSymbol(ctx, "FreshWorkspaceSymbol", "javascript"); err != nil {
				t.Fatalf("repeated WorkspaceSymbol: %v", err)
			}
			if got := client.notificationCount(); got != 2 {
				t.Fatalf("notifications after repeated query = %d, want open+change only", got)
			}
		})
	}
}

func TestWorkspaceSymbolDeletedExplicitDocumentFailsBeforeQuery(t *testing.T) {
	root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function DeletedWorkspaceSymbol() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, stale); err != nil {
		t.Fatalf("DidOpen deleted document: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}

	if _, err := mgr.WorkspaceSymbol(ctx, "DeletedWorkspaceSymbol", "javascript"); err == nil {
		t.Fatal("WorkspaceSymbol after deletion succeeded, want foreground sync error")
	}
	client := factory.clientContainingURI(t, uri)
	if got := client.requestCount(); got != 0 {
		t.Fatalf("workspace/symbol request count = %d, want 0 after disk failure", got)
	}
}

func TestWorkspaceSymbolRepeatedLanguageBootstrapDoesNotDuplicateDidOpen(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function BootstrapWorkspaceSymbol() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := mgr.WorkspaceSymbol(ctx, "BootstrapWorkspaceSymbol", "javascript"); err != nil {
			t.Fatalf("WorkspaceSymbol attempt %d: %v", attempt, err)
		}
	}
	client := factory.clientContainingURI(t, fileURIFromPath(target))
	if got := client.notificationCount(); got != 1 {
		t.Fatalf("language bootstrap notifications = %d, want one DidOpen", got)
	}
}

func TestDidOpenAfterLanguageBootstrapUsesDidChange(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function BootstrapThenOpen() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	if _, err := mgr.WorkspaceSymbol(ctx, "BootstrapThenOpen", "javascript"); err != nil {
		t.Fatalf("bootstrap WorkspaceSymbol: %v", err)
	}
	uri := fileURIFromPath(target)
	fresh := "function BootstrapThenFreshOpen() {}\n"
	writeGenericTestFile(t, target, fresh)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, fresh); err != nil {
		t.Fatalf("DidOpen after language bootstrap: %v", err)
	}
	if got, want := factory.clientContainingURI(t, uri).eventsSnapshot(), []string{
		"open:" + uri + ":0",
		"request:BootstrapThenOpen",
		"change:" + uri + ":1",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("bootstrap then DidOpen wire sequence = %#v, want %#v", got, want)
	}
}

func TestDidOpenRepeatedExplicitDocumentUsesMonotonicDidChange(t *testing.T) {
	root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function RepeatedOpenStale() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, stale); err != nil {
		t.Fatalf("first DidOpen: %v", err)
	}
	fresh := "function RepeatedOpenFresh() {}\n"
	writeGenericTestFile(t, target, fresh)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, fresh); err != nil {
		t.Fatalf("repeated DidOpen: %v", err)
	}
	if got, want := factory.clientContainingURI(t, uri).eventsSnapshot(), []string{
		"open:" + uri + ":1",
		"change:" + uri + ":2",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("repeated DidOpen wire sequence = %#v, want %#v", got, want)
	}
}

func TestWorkspaceSymbolDoesNotOverwriteDirtyExplicitDocument(t *testing.T) {
	root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function StaleWorkspaceSymbol() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, stale); err != nil {
		t.Fatalf("DidOpen dirty document: %v", err)
	}
	dirty := "function DirtyWorkspaceSymbol() {}\n"
	if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: dirty}}); err != nil {
		t.Fatalf("DidChange dirty document: %v", err)
	}
	writeGenericTestFile(t, target, "function FreshDiskWorkspaceSymbol() {}\n")

	results, err := mgr.WorkspaceSymbol(ctx, "DirtyWorkspaceSymbol", "javascript")
	if err != nil {
		t.Fatalf("WorkspaceSymbol for dirty memory document: %v", err)
	}
	assertStrictWorkspaceSymbolResult(t, results, "DirtyWorkspaceSymbol", uri)
	client := factory.clientContainingURI(t, uri)
	if got := client.notificationCount(); got != 2 {
		t.Fatalf("dirty document notifications = %d, want original open+change without disk overwrite", got)
	}
}

func TestWorkspaceSymbolSynchronizesOnlyRequestedLanguage(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"workspace-symbol-sync"}`)
	writeGenericTestFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{}}`)
	jsTarget := filepath.Join(root, "app.js")
	tsTarget := filepath.Join(root, "app.ts")
	jsStale := "function StaleJavaScriptWorkspaceSymbol() {}\n"
	tsStale := "function StaleTypeScriptWorkspaceSymbol() {}\n"
	writeGenericTestFile(t, jsTarget, jsStale)
	writeGenericTestFile(t, tsTarget, tsStale)
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	jsCtx := workspaceSymbolSyncContext(t, root, jsTarget, "javascript", true)
	tsCtx := workspaceSymbolSyncContext(t, root, tsTarget, "typescript", true)
	jsURI := fileURIFromPath(jsTarget)
	tsURI := fileURIFromPath(tsTarget)
	if err := mgr.DidOpen(jsCtx, jsURI, "javascript", 1, jsStale); err != nil {
		t.Fatalf("DidOpen JavaScript: %v", err)
	}
	if err := mgr.DidOpen(tsCtx, tsURI, "typescript", 1, tsStale); err != nil {
		t.Fatalf("DidOpen TypeScript: %v", err)
	}
	writeGenericTestFile(t, jsTarget, "function FreshJavaScriptWorkspaceSymbol() {}\n")
	writeGenericTestFile(t, tsTarget, "function FreshTypeScriptWorkspaceSymbol() {}\n")

	if _, err := mgr.WorkspaceSymbol(jsCtx, "FreshJavaScriptWorkspaceSymbol", "javascript"); err != nil {
		t.Fatalf("WorkspaceSymbol JavaScript: %v", err)
	}
	if got := factory.clientContainingURI(t, jsURI).changeCount(); got != 1 {
		t.Fatalf("JavaScript change count = %d, want 1", got)
	}
	if got := factory.clientContainingURI(t, tsURI).changeCount(); got != 0 {
		t.Fatalf("TypeScript change count during JavaScript query = %d, want 0", got)
	}
}

func TestWorkspaceSymbolRestoresExplicitDocumentOnlyToReplacementClient(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function ReplacementWorkspaceSymbol() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen replacement document: %v", err)
	}
	oldClient := factory.clientContainingURI(t, uri)
	oldClient.setHealthy(false)

	results, err := mgr.WorkspaceSymbol(ctx, "ReplacementWorkspaceSymbol", "javascript")
	if err != nil {
		t.Fatalf("WorkspaceSymbol replacement client: %v", err)
	}
	assertStrictWorkspaceSymbolResult(t, results, "ReplacementWorkspaceSymbol", uri)
	clients := factory.clientsSnapshot()
	if len(clients) != 2 {
		t.Fatalf("factory clients = %d, want old+replacement", len(clients))
	}
	if got := oldClient.requestCount(); got != 0 {
		t.Fatalf("old client request count = %d, want 0", got)
	}
	if got, want := clients[1].eventsSnapshot(), []string{
		"open:" + uri + ":2",
		"request:ReplacementWorkspaceSymbol",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("replacement wire sequence = %#v, want %#v", got, want)
	}
}

func TestDirectDidChangeAfterWorkspaceSyncUsesNextManagedVersion(t *testing.T) {
	root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function VersionStale() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, stale); err != nil {
		t.Fatalf("DidOpen version fixture: %v", err)
	}
	writeGenericTestFile(t, target, "function VersionFresh() {}\n")
	if _, err := mgr.WorkspaceSymbol(ctx, "VersionFresh", "javascript"); err != nil {
		t.Fatalf("WorkspaceSymbol version sync: %v", err)
	}
	if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: "function VersionDirty() {}\n"}}); err != nil {
		t.Fatalf("DidChange with stale caller version: %v", err)
	}
	if got, want := factory.clientContainingURI(t, uri).eventsSnapshot(), []string{
		"open:" + uri + ":1", "change:" + uri + ":2", "request:VersionFresh", "change:" + uri + ":3",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("managed version events = %#v, want %#v", got, want)
	}
}

func TestWorkspaceSymbolSerializesSyncAndDirectDidChange(t *testing.T) {
	root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function ConcurrentStale() {}\n")
	changeEntered := make(chan struct{})
	changeRelease := make(chan struct{})
	factory := &strictWorkspaceSymbolFactory{changeEntered: changeEntered, changeRelease: changeRelease}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, stale); err != nil {
		t.Fatalf("DidOpen concurrent fixture: %v", err)
	}
	writeGenericTestFile(t, target, "function ConcurrentFresh() {}\n")
	group := newTestGoroutineGroup(t)
	workspaceDone := make(chan error, 1)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "ConcurrentFresh", "javascript")
		workspaceDone <- err
	})
	waitWorkspaceSymbolSignal(t, changeEntered, "workspace sync DidChange")
	directDone := make(chan error, 1)
	group.Go(func() {
		directDone <- mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: "function ConcurrentDirty() {}\n"}})
	})
	assertWorkspaceSymbolOperationBlocked(t, directDone, "direct DidChange")
	close(changeRelease)
	if err := awaitWorkspaceSymbolError(t, workspaceDone, "WorkspaceSymbol"); err == nil || !strings.Contains(err.Error(), "changed during workspace symbol request") {
		t.Fatalf("concurrent WorkspaceSymbol error = %v, want fail-fast concurrent mutation", err)
	}
	if err := awaitWorkspaceSymbolError(t, directDone, "direct DidChange"); err != nil {
		t.Fatalf("serialized direct DidChange: %v", err)
	}
	if got, want := factory.clientContainingURI(t, uri).eventsSnapshot(), []string{
		"open:" + uri + ":1", "change:" + uri + ":2", "request:ConcurrentFresh", "change:" + uri + ":3",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("concurrent wire events = %#v, want %#v", got, want)
	}
}

func TestDirectDidChangeFailsBeforeReplacementClientNotification(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function GenerationOne() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen generation fixture: %v", err)
	}
	factory.clientContainingURI(t, uri).setHealthy(false)
	err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: "function GenerationTwo() {}\n"}})
	if err == nil || !strings.Contains(err.Error(), "belongs to client generation") {
		t.Fatalf("replacement DidChange error = %v, want generation mismatch", err)
	}
	clients := factory.clientsSnapshot()
	if len(clients) != 2 {
		t.Fatalf("replacement client count = %d, want two", len(clients))
	}
	if clients[1].notificationCount() != 0 {
		t.Fatalf("replacement client events = %#v, want no document notification", clients[1].eventsSnapshot())
	}
}

func TestLanguageBootstrapDocumentRefreshesAndFailsOnDelete(t *testing.T) {
	root, target, stale := writeWorkspaceSymbolSyncFixture(t, "app.js", "function BootstrapStale() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	uri := fileURIFromPath(target)
	if _, err := mgr.WorkspaceSymbol(ctx, "BootstrapStale", "javascript"); err != nil {
		t.Fatalf("initial bootstrap WorkspaceSymbol: %v", err)
	}
	fresh := "function BootstrapFresh() {}\n"
	writeGenericTestFile(t, target, fresh)
	results, err := mgr.WorkspaceSymbol(ctx, "BootstrapFresh", "javascript")
	if err != nil {
		t.Fatalf("refreshed bootstrap WorkspaceSymbol: %v", err)
	}
	assertStrictWorkspaceSymbolResult(t, results, "BootstrapFresh", uri)
	if err := os.Remove(target); err != nil {
		t.Fatalf("remove bootstrap target: %v", err)
	}
	if _, err := mgr.WorkspaceSymbol(ctx, "BootstrapFresh", "javascript"); err == nil {
		t.Fatal("deleted bootstrap document query succeeded")
	}
	client := factory.clientContainingURI(t, uri)
	if got := client.requestCount(); got != 2 {
		t.Fatalf("bootstrap requests after delete = %d, want delete failure before third query", got)
	}
	if got, want := client.eventsSnapshot(), []string{
		"open:" + uri + ":0", "request:BootstrapStale", "change:" + uri + ":1", "request:BootstrapFresh",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("bootstrap refresh events = %#v, want %#v (initial=%q)", got, want, stale)
	}
}

func TestLanguageBootstrapAndDidCloseCannotResurrectDocument(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function CloseBootstrap() {}\n")
	openEntered := make(chan struct{})
	openRelease := make(chan struct{})
	factory := &strictWorkspaceSymbolFactory{openEntered: openEntered, openRelease: openRelease}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	uri := fileURIFromPath(target)
	group := newTestGoroutineGroup(t)
	workspaceDone := make(chan error, 1)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "CloseBootstrap", "javascript")
		workspaceDone <- err
	})
	waitWorkspaceSymbolSignal(t, openEntered, "bootstrap DidOpen")
	closeDone := make(chan error, 1)
	group.Go(func() { closeDone <- mgr.DidClose(ctx, uri) })
	assertWorkspaceSymbolOperationBlocked(t, closeDone, "DidClose")
	close(openRelease)
	if err := awaitWorkspaceSymbolError(t, closeDone, "DidClose"); err != nil {
		t.Fatalf("DidClose after bootstrap barrier: %v", err)
	}
	_ = awaitWorkspaceSymbolError(t, workspaceDone, "WorkspaceSymbol")
	client := factory.clientsSnapshot()[0]
	if client.hasDocument(uri) {
		t.Fatalf("bootstrap document %s was resurrected after DidClose; events=%#v", uri, client.eventsSnapshot())
	}
}

func TestManagedDocumentLimitsFailBeforeNotificationAndCleanTextIsNotRetained(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"managed-limits"}`)
	first := filepath.Join(root, "first.js")
	second := filepath.Join(root, "second.js")
	firstText := "function FirstManaged() {}\n"
	secondText := "function SecondManaged() {}\n"
	writeGenericTestFile(t, first, firstText)
	writeGenericTestFile(t, second, secondText)
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	mgr.explicitDocumentLimit = 1
	mgr.dirtyDocumentByteLimit = 16
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, first, "javascript", true)
	firstURI := fileURIFromPath(first)
	if err := mgr.DidOpen(ctx, firstURI, "javascript", 1, firstText); err != nil {
		t.Fatalf("DidOpen first managed document: %v", err)
	}
	mgr.explicitOpenMu.RLock()
	firstState := mgr.explicitDocuments[firstURI]
	mgr.explicitOpenMu.RUnlock()
	if firstState.text != "" || firstState.fullTextKnown {
		t.Fatalf("clean managed state retained full text: %#v", firstState)
	}
	if err := mgr.DidOpen(ctx, fileURIFromPath(second), "javascript", 1, secondText); err == nil || !strings.Contains(err.Error(), "managed document limit") {
		t.Fatalf("second DidOpen error = %v, want managed document limit", err)
	}
	if err := mgr.DidChange(ctx, firstURI, 2, []protocol.TextDocumentContentChangeEvent{{Text: strings.Repeat("x", 32)}}); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("oversized dirty DidChange error = %v, want byte limit", err)
	}
	if got := factory.clientContainingURI(t, firstURI).notificationCount(); got != 1 {
		t.Fatalf("notifications after capacity failures = %d, want initial DidOpen only", got)
	}
}

func TestWorkspaceSymbolCapabilityFailureDoesNotBootstrapDocument(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function UnsupportedWorkspace() {}\n")
	supported := false
	factory := &strictWorkspaceSymbolFactory{workspaceSymbolSupported: &supported}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	if _, err := mgr.WorkspaceSymbol(ctx, "UnsupportedWorkspace", "javascript"); err == nil || !errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("unsupported WorkspaceSymbol error = %v", err)
	}
	clients := factory.clientsSnapshot()
	if len(clients) != 1 {
		t.Fatalf("unsupported capability clients = %d, want one initialized owner", len(clients))
	}
	if clients[0].notificationCount() != 0 {
		t.Fatalf("unsupported capability bootstrap events=%#v, want none", clients[0].eventsSnapshot())
	}
}

func waitWorkspaceSymbolSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func assertWorkspaceSymbolOperationBlocked(t *testing.T, done <-chan error, label string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s returned before URI gate release: %v", label, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func awaitWorkspaceSymbolError(t *testing.T, done <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}

func writeWorkspaceSymbolSyncFixture(t *testing.T, name, text string) (string, string, string) {
	t.Helper()
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"workspace-symbol-sync"}`)
	target := filepath.Join(root, name)
	writeGenericTestFile(t, target, text)
	return root, target, text
}

func workspaceSymbolSyncContext(t *testing.T, root, target, languageID string, resolved bool) context.Context {
	t.Helper()
	ctx := ctxWithCWD(root, "agent-workspace-sync", "thread-workspace-sync")
	if !resolved {
		return ctx
	}
	scope, err := ResolveLSPToolScope(LSPToolScope{
		AgentID:               "agent-workspace-sync",
		ThreadID:              "thread-workspace-sync",
		CWD:                   root,
		WorkspaceRoots:        []string{root},
		Family:                "lsp",
		LanguageID:            languageID,
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
		t.Fatalf("resolve workspace symbol sync scope: %v", err)
	}
	return WithResolvedLSPToolScope(ctx, scope)
}

func assertStrictWorkspaceSymbolResult(t *testing.T, results []protocol.WorkspaceSymbolResult, name, uri string) {
	t.Helper()
	if len(results) != 1 || results[0].WorkspaceSymbol == nil {
		t.Fatalf("workspace symbols = %#v, want one result", results)
	}
	if got := results[0].WorkspaceSymbol.Name; got != name {
		t.Fatalf("workspace symbol name = %q, want %q", got, name)
	}
	payload, err := json.Marshal(results[0].WorkspaceSymbol.Location)
	if err != nil {
		t.Fatalf("encode workspace symbol location: %v", err)
	}
	var location protocol.WorkspaceSymbolLocation
	if err := json.Unmarshal(payload, &location); err != nil {
		t.Fatalf("decode workspace symbol location: %v", err)
	}
	if got := location.URI; got != uri {
		t.Fatalf("workspace symbol URI = %q, want %q", got, uri)
	}
}

type strictWorkspaceSymbolFactory struct {
	mu                       sync.Mutex
	clients                  []*strictWorkspaceSymbolClient
	openEntered              chan struct{}
	openRelease              chan struct{}
	changeEntered            chan struct{}
	changeRelease            chan struct{}
	changeBatchEntered       chan struct{}
	changeBatchRelease       chan struct{}
	changeBatchTarget        int
	requestErrors            []error
	workspaceSymbolSupported *bool
}

func (f *strictWorkspaceSymbolFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := len(f.clients)
	supported := true
	if f.workspaceSymbolSupported != nil {
		supported = *f.workspaceSymbolSupported
	}
	client := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true,
		openEntered: f.openEntered, openRelease: f.openRelease,
		changeEntered: f.changeEntered, changeRelease: f.changeRelease,
		changeBatchEntered: f.changeBatchEntered, changeBatchRelease: f.changeBatchRelease,
		changeBatchTarget:        f.changeBatchTarget,
		workspaceSymbolSupported: supported,
	}
	if index < len(f.requestErrors) {
		client.requestErr = f.requestErrors[index]
	}
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *strictWorkspaceSymbolFactory) clientsSnapshot() []*strictWorkspaceSymbolClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*strictWorkspaceSymbolClient(nil), f.clients...)
}

func (f *strictWorkspaceSymbolFactory) clientContainingURI(t *testing.T, uri string) *strictWorkspaceSymbolClient {
	t.Helper()
	for _, client := range f.clientsSnapshot() {
		if client.hasDocument(uri) {
			return client
		}
	}
	t.Fatalf("no strict client contains %s", uri)
	return nil
}

type strictWorkspaceSymbolDocument struct {
	languageID string
	version    int
	text       string
}

type strictWorkspaceSymbolClient struct {
	mu                       sync.Mutex
	documents                map[string]strictWorkspaceSymbolDocument
	events                   []string
	healthy                  bool
	requestErr               error
	openEntered              chan struct{}
	openRelease              chan struct{}
	openOnce                 sync.Once
	changeEntered            chan struct{}
	changeRelease            chan struct{}
	changeOnce               sync.Once
	changeBatchEntered       chan struct{}
	changeBatchRelease       chan struct{}
	changeBatchOnce          sync.Once
	changeBatchTarget        int
	changeCalls              int
	workspaceSymbolSupported bool
}

func (c *strictWorkspaceSymbolClient) Initialize(context.Context, string) error { return nil }
func (c *strictWorkspaceSymbolClient) Shutdown(context.Context) error           { return nil }
func (c *strictWorkspaceSymbolClient) Notify(context.Context, string, any) error {
	return nil
}

func (c *strictWorkspaceSymbolClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method != protocol.MethodWorkspaceSymbol {
		return nil, fmt.Errorf("unexpected request method %q", method)
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var request protocol.WorkspaceSymbolParams
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, "request:"+request.Query)
	if c.requestErr != nil {
		return nil, c.requestErr
	}
	results := make([]protocol.WorkspaceSymbol, 0, 1)
	for uri, document := range c.documents {
		if !strings.Contains(document.text, request.Query) {
			continue
		}
		results = append(results, protocol.WorkspaceSymbol{
			Name:     request.Query,
			Kind:     int(protocol.SymbolKindFunction),
			Location: protocol.WorkspaceSymbolLocation{URI: uri},
		})
	}
	return json.Marshal(results)
}

func (c *strictWorkspaceSymbolClient) DidOpen(_ context.Context, uri, languageID string, version int, text string) error {
	c.waitOpenBarrier()
	c.mu.Lock()
	defer c.mu.Unlock()
	if document, ok := c.documents[uri]; ok {
		return fmt.Errorf("duplicate DidOpen for %s at version %d; current version %d", uri, version, document.version)
	}
	c.documents[uri] = strictWorkspaceSymbolDocument{languageID: languageID, version: version, text: text}
	c.events = append(c.events, fmt.Sprintf("open:%s:%d", uri, version))
	return nil
}

func (c *strictWorkspaceSymbolClient) DidChange(_ context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.waitChangeBarrier()
	c.mu.Lock()
	document, ok := c.documents[uri]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("DidChange for unopened %s", uri)
	}
	if version <= document.version {
		c.mu.Unlock()
		return fmt.Errorf("non-monotonic DidChange for %s: version %d <= %d", uri, version, document.version)
	}
	if len(changes) != 1 || changes[0].Range != nil || changes[0].RangeLength != nil {
		c.mu.Unlock()
		return fmt.Errorf("DidChange for %s is not one full-document update", uri)
	}
	document.version = version
	document.text = changes[0].Text
	c.documents[uri] = document
	c.events = append(c.events, fmt.Sprintf("change:%s:%d", uri, version))
	c.changeCalls++
	waitBatch := c.changeBatchTarget > 0 && c.changeCalls == c.changeBatchTarget
	c.mu.Unlock()
	if waitBatch {
		c.changeBatchOnce.Do(func() { close(c.changeBatchEntered) })
		<-c.changeBatchRelease
	}
	return nil
}

func (c *strictWorkspaceSymbolClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.documents[uri]; !ok {
		return fmt.Errorf("DidClose for unopened %s", uri)
	}
	delete(c.documents, uri)
	c.events = append(c.events, "close:"+uri)
	return nil
}

func (c *strictWorkspaceSymbolClient) Close() error { return nil }

func (c *strictWorkspaceSymbolClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{WorkspaceSymbolProvider: c.workspaceSymbolSupported}
}

func (c *strictWorkspaceSymbolClient) waitOpenBarrier() {
	if c.openEntered == nil || c.openRelease == nil {
		return
	}
	c.openOnce.Do(func() { close(c.openEntered) })
	<-c.openRelease
}

func (c *strictWorkspaceSymbolClient) waitChangeBarrier() {
	if c.changeEntered == nil || c.changeRelease == nil {
		return
	}
	c.changeOnce.Do(func() { close(c.changeEntered) })
	<-c.changeRelease
}

func (c *strictWorkspaceSymbolClient) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy
}

func (c *strictWorkspaceSymbolClient) setHealthy(healthy bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = healthy
}

func (c *strictWorkspaceSymbolClient) hasDocument(uri string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.documents[uri]
	return ok
}

func (c *strictWorkspaceSymbolClient) eventsSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

func (c *strictWorkspaceSymbolClient) notificationCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, event := range c.events {
		if !strings.HasPrefix(event, "request:") {
			count++
		}
	}
	return count
}

func (c *strictWorkspaceSymbolClient) requestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, event := range c.events {
		if strings.HasPrefix(event, "request:") {
			count++
		}
	}
	return count
}

func (c *strictWorkspaceSymbolClient) changeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, event := range c.events {
		if strings.HasPrefix(event, "change:") {
			count++
		}
	}
	return count
}
