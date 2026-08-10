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

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestDidChangeFailsWhenRecipientIsReplacedWhileWireCallIsInFlight(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function RecipientOne() {}\n")
	changeEntered := make(chan struct{})
	changeRelease := make(chan struct{})
	factory := &strictWorkspaceSymbolFactory{changeEntered: changeEntered, changeRelease: changeRelease}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen recipient fixture: %v", err)
	}
	oldClient := factory.clientContainingURI(t, uri)
	changeDone := make(chan error, 1)
	group := newTestGoroutineGroup(t)
	group.Go(func() {
		changeDone <- mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: "function RecipientTwo() {}\n"}})
	})
	waitWorkspaceSymbolSignal(t, changeEntered, "in-flight DidChange")
	replacement, err := mgr.rebuildClientAfterFailure(ctx, oldClient, false)
	if err != nil {
		t.Fatalf("replace client during DidChange: %v", err)
	}
	close(changeRelease)
	if err := <-changeDone; !errors.Is(err, ErrStaleClientLease) {
		t.Fatalf("DidChange after recipient replacement error = %v, want ErrStaleClientLease", err)
	}
	if got := replacement.(*strictWorkspaceSymbolClient).notificationCount(); got != 0 {
		t.Fatalf("replacement notifications = %d, want zero", got)
	}
	state, ok := mgr.explicitDocumentForURI(uri)
	if !ok || state.lspVersion != 1 {
		t.Fatalf("managed state after stale DidChange = %#v, present=%v; want prior version 1", state, ok)
	}
}

func TestWorkspaceSymbolRejectsResultFromReplacedInFlightRecipient(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function OldRecipientSymbol() {}\n")
	requestEntered := make(chan struct{})
	requestRelease := make(chan struct{})
	factory := &blockingWorkspaceSymbolFactory{entered: requestEntered, release: requestRelease}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	resultDone := make(chan error, 1)
	group := newTestGoroutineGroup(t)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "OldRecipientSymbol", "javascript")
		resultDone <- err
	})
	waitWorkspaceSymbolSignal(t, requestEntered, "in-flight workspace/symbol")
	oldClient := factory.firstClient(t)
	if _, err := mgr.rebuildClientAfterFailure(ctx, oldClient, false); err != nil {
		t.Fatalf("replace client during workspace/symbol: %v", err)
	}
	close(requestRelease)
	if err := <-resultDone; !errors.Is(err, ErrStaleClientLease) {
		t.Fatalf("workspace/symbol after recipient replacement error = %v, want ErrStaleClientLease", err)
	}
}

func TestWorkspaceSymbolMethodNotFoundDoesNotHideStaleRecipient(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function MissingOnOldRecipient() {}\n")
	requestEntered := make(chan struct{})
	requestRelease := make(chan struct{})
	factory := &blockingWorkspaceSymbolFactory{
		entered: requestEntered,
		release: requestRelease,
		requestErr: &responseError{
			Code: jsonRPCMethodNotFound, Message: "workspace/symbol missing",
		},
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	resultDone := make(chan error, 1)
	group := newTestGoroutineGroup(t)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "MissingOnOldRecipient", "javascript")
		resultDone <- err
	})
	waitWorkspaceSymbolSignal(t, requestEntered, "method-not-found workspace/symbol")
	oldClient := factory.firstClient(t)
	if _, err := mgr.rebuildClientAfterFailure(ctx, oldClient, false); err != nil {
		t.Fatalf("replace method-not-found recipient: %v", err)
	}
	close(requestRelease)
	err := <-resultDone
	if !errors.Is(err, ErrStaleClientLease) {
		t.Fatalf("workspace/symbol method-not-found replacement error = %v, want ErrStaleClientLease", err)
	}
	if errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("stale workspace/symbol was misclassified as unsupported: %v", err)
	}
}

func TestWorkspaceSymbolRejectsSameScopeMembershipChangeDuringRequest(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function MembershipBase() {}\n")
	added := filepath.Join(root, "z_added.js")
	addedText := "function MembershipAdded() {}\n"
	writeGenericTestFile(t, added, addedText)
	requestEntered := make(chan struct{})
	requestRelease := make(chan struct{})
	factory := &blockingWorkspaceSymbolFactory{entered: requestEntered, release: requestRelease}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	resultDone := make(chan error, 1)
	group := newTestGoroutineGroup(t)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "MembershipBase", "javascript")
		resultDone <- err
	})
	waitWorkspaceSymbolSignal(t, requestEntered, "membership workspace/symbol request")
	if err := mgr.DidOpen(ctx, fileURIFromPath(added), "javascript", 1, addedText); err != nil {
		t.Fatalf("same-scope membership DidOpen: %v", err)
	}
	close(requestRelease)
	if err := <-resultDone; err == nil || !strings.Contains(err.Error(), "managed document changed") {
		t.Fatalf("workspace/symbol membership error = %v, want fail-fast", err)
	}
}

func TestWorkspaceSymbolRejectsOversizedCleanDocumentBeforeNotification(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function SmallClean() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	mgr.cleanDocumentByteLimit = 64
	mgr.cleanRefreshByteLimit = 1024
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen oversized clean fixture: %v", err)
	}
	writeGenericTestFile(t, target, strings.Repeat("x", 128))
	_, err := mgr.WorkspaceSymbol(ctx, "SmallClean", "javascript")
	if err == nil || !strings.Contains(err.Error(), "exceeds read limit 64") {
		t.Fatalf("oversized clean workspace error = %v, want per-document limit", err)
	}
	client := factory.clientContainingURI(t, uri)
	if client.changeCount() != 0 || client.requestCount() != 0 {
		t.Fatalf("oversized clean query sent activity: changes=%d requests=%d", client.changeCount(), client.requestCount())
	}
}

func TestWorkspaceSymbolRejectsAggregateCleanBudgetBeforeNotification(t *testing.T) {
	root, first, firstText := writeWorkspaceSymbolSyncFixture(t, "app.js", strings.Repeat("a", 40))
	second := filepath.Join(root, "peer.js")
	secondText := strings.Repeat("b", 40)
	writeGenericTestFile(t, second, secondText)
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	mgr.cleanDocumentByteLimit = 128
	mgr.cleanRefreshByteLimit = 64
	ctx := workspaceSymbolSyncContext(t, root, first, "javascript", true)
	if err := mgr.DidOpen(ctx, fileURIFromPath(first), "javascript", 1, firstText); err != nil {
		t.Fatalf("DidOpen first aggregate fixture: %v", err)
	}
	if err := mgr.DidOpen(ctx, fileURIFromPath(second), "javascript", 1, secondText); err != nil {
		t.Fatalf("DidOpen second aggregate fixture: %v", err)
	}
	_, err := mgr.WorkspaceSymbol(ctx, "aggregate", "javascript")
	if err == nil || !strings.Contains(err.Error(), "aggregate exceeds read limit 64") {
		t.Fatalf("aggregate clean workspace error = %v, want aggregate limit", err)
	}
	client := factory.clientContainingURI(t, fileURIFromPath(first))
	if client.changeCount() != 0 || client.requestCount() != 0 {
		t.Fatalf("aggregate clean query sent activity: changes=%d requests=%d", client.changeCount(), client.requestCount())
	}
}

func TestWorkspaceSymbolActualReadBudgetRejectsGrowthAfterStatPreflight(t *testing.T) {
	root, first, firstText := writeWorkspaceSymbolSyncFixture(t, "app.js", strings.Repeat("a", 20))
	second := filepath.Join(root, "peer.js")
	secondText := strings.Repeat("b", 20)
	writeGenericTestFile(t, second, secondText)
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	mgr.cleanDocumentByteLimit = 128
	mgr.cleanRefreshByteLimit = 64
	ctx := workspaceSymbolSyncContext(t, root, first, "javascript", true)
	firstURI := fileURIFromPath(first)
	secondURI := fileURIFromPath(second)
	if err := mgr.DidOpen(ctx, firstURI, "javascript", 1, firstText); err != nil {
		t.Fatalf("DidOpen first growth fixture: %v", err)
	}
	if err := mgr.DidOpen(ctx, secondURI, "javascript", 1, secondText); err != nil {
		t.Fatalf("DidOpen second growth fixture: %v", err)
	}
	firstState, _ := mgr.explicitDocumentForURI(firstURI)
	secondState, _ := mgr.explicitDocumentForURI(secondURI)
	states := []explicitDocumentState{firstState, secondState}
	if err := mgr.preflightExplicitDocumentSyncReads(states); err != nil {
		t.Fatalf("initial stat preflight: %v", err)
	}
	writeGenericTestFile(t, first, strings.Repeat("c", 40))
	writeGenericTestFile(t, second, strings.Repeat("d", 40))
	identity := workspaceClientIdentity{key: firstState.configKey, generation: firstState.clientGeneration}
	_, err := mgr.prepareExplicitDocumentSyncUpdatesAfterPreflight(ctx, states, identity)
	if err == nil || !strings.Contains(err.Error(), "actual aggregate exceeds read limit 64") {
		t.Fatalf("actual growth aggregate error = %v, want hard budget rejection", err)
	}
	if got := factory.clientsSnapshot()[0].changeCount(); got != 0 {
		t.Fatalf("actual growth aggregate sent %d changes before rejection", got)
	}
}

func TestLanguageBootstrapRejectsOversizedFileBeforeDidOpen(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "x")
	if err := os.Truncate(target, defaultCleanDocumentByteLimit+1); err != nil {
		t.Fatalf("truncate oversized bootstrap fixture: %v", err)
	}
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	_, err := mgr.WorkspaceSymbol(ctx, "oversized", "javascript")
	if err == nil || !strings.Contains(err.Error(), "exceeds read limit") {
		t.Fatalf("oversized language bootstrap error = %v, want read limit", err)
	}
	client := factory.clientsSnapshot()[0]
	if client.notificationCount() != 0 || client.requestCount() != 0 {
		t.Fatalf("oversized bootstrap sent activity: notifications=%d requests=%d", client.notificationCount(), client.requestCount())
	}
}

func TestWorkspaceSymbolDeadRequestQuarantinesWithoutReplayAndNextQueryRestores(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function DeadRequestSymbol() {}\n")
	factory := &strictWorkspaceSymbolFactory{requestErrors: []error{ErrTransportClosed}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen dead request fixture: %v", err)
	}
	if _, err := mgr.WorkspaceSymbol(ctx, "DeadRequestSymbol", "javascript"); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("first dead WorkspaceSymbol error = %v, want ErrTransportClosed", err)
	}
	clients := factory.clientsSnapshot()
	if len(clients) != 2 {
		t.Fatalf("dead request quarantine clients = %d, want two", len(clients))
	}
	if clients[0].requestCount() != 1 || clients[1].requestCount() != 0 {
		t.Fatalf("dead request quarantine old/new requests = %d/%d, want 1/0", clients[0].requestCount(), clients[1].requestCount())
	}
	results, err := mgr.WorkspaceSymbol(ctx, "DeadRequestSymbol", "javascript")
	if err != nil {
		t.Fatalf("second WorkspaceSymbol after quarantine: %v", err)
	}
	assertStrictWorkspaceSymbolResult(t, results, "DeadRequestSymbol", uri)
	if clients[1].requestCount() != 1 || !clients[1].hasDocument(uri) {
		t.Fatalf("replacement query/restore = requests %d open %v, want 1/true", clients[1].requestCount(), clients[1].hasDocument(uri))
	}
}

func TestUnmanagedDidCloseTombstonesAreBoundedAndClearedByDidOpen(t *testing.T) {
	root, first, firstText := writeWorkspaceSymbolSyncFixture(t, "first.js", "function FirstTombstone() {}\n")
	second := filepath.Join(root, "second.js")
	third := filepath.Join(root, "third.js")
	writeGenericTestFile(t, second, "function SecondTombstone() {}\n")
	writeGenericTestFile(t, third, "function ThirdTombstone() {}\n")
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: &strictWorkspaceSymbolFactory{}}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	mgr.documentOperationLimit = 2
	ctx := workspaceSymbolSyncContext(t, root, first, "javascript", true)
	if err := mgr.DidClose(ctx, fileURIFromPath(first)); err != nil {
		t.Fatalf("first unmanaged DidClose: %v", err)
	}
	if err := mgr.DidClose(ctx, fileURIFromPath(second)); err != nil {
		t.Fatalf("second unmanaged DidClose: %v", err)
	}
	if err := mgr.DidClose(ctx, fileURIFromPath(third)); err == nil || !strings.Contains(err.Error(), "limit 2") {
		t.Fatalf("third unmanaged DidClose error = %v, want bounded tombstone failure", err)
	}
	if err := mgr.DidOpen(ctx, fileURIFromPath(first), "javascript", 1, firstText); err != nil {
		t.Fatalf("DidOpen clears tombstone: %v", err)
	}
	mgr.documentOperationMu.Lock()
	count := len(mgr.documentOperations)
	mgr.documentOperationMu.Unlock()
	if count != 1 {
		t.Fatalf("document operation tombstones after DidOpen = %d, want one", count)
	}
}

func TestDeadClientRetryRestoresFullDirtyManagedDocument(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function DiskVersion() {}\n")
	factory := &retryDocumentStateFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen dirty retry fixture: %v", err)
	}
	dirty := "function DirtyMemoryVersion() {}\n"
	if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: dirty}}); err != nil {
		t.Fatalf("full dirty DidChange: %v", err)
	}
	first := factory.clientAt(t, 0)
	first.requestErr = ErrTransportClosed
	raw, err := mgr.request(ctx, first, protocol.MethodHover, protocol.HoverParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
	if err != nil {
		t.Fatalf("dead client full dirty retry: %v", err)
	}
	if !strings.Contains(string(raw), "DirtyMemoryVersion") {
		t.Fatalf("replacement hover payload = %s, want dirty in-memory text", raw)
	}
	replacement := factory.clientAt(t, 1)
	if got := replacement.documentText(uri); got != dirty {
		t.Fatalf("replacement document text = %q, want %q", got, dirty)
	}
	if got := replacement.openCount; got != 1 {
		t.Fatalf("replacement DidOpen count = %d, want one", got)
	}
}

func TestDeadClientRetryRejectsIncrementalDirtyManagedDocument(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function IncrementalBase() {}\n")
	factory := &retryDocumentStateFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen incremental retry fixture: %v", err)
	}
	rng := protocol.Range{Start: protocol.Position{Line: 0}, End: protocol.Position{Line: 0, Character: 8}}
	if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Range: &rng, Text: "changed"}}); err != nil {
		t.Fatalf("incremental dirty DidChange: %v", err)
	}
	first := factory.clientAt(t, 0)
	first.requestErr = ErrTransportClosed
	_, err := mgr.request(ctx, first, protocol.MethodHover, protocol.HoverParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot restore dirty incremental document") {
		t.Fatalf("incremental dirty retry error = %v, want strict restore failure", err)
	}
	replacement := factory.clientAt(t, 1)
	if replacement.openCount != 0 || replacement.requestCount != 0 {
		t.Fatalf("replacement activity after strict restore failure = opens %d requests %d", replacement.openCount, replacement.requestCount)
	}
}

func TestEnsureClientRestoresManagedDocumentToReplacementGeneration(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function EnsureReplacement() {}\n")
	factory := &retryDocumentStateFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen ensure replacement fixture: %v", err)
	}
	factory.clientAt(t, 0).setHealthy(false)
	replacement, err := mgr.EnsureClient(ctx, target, "javascript")
	if err != nil {
		t.Fatalf("EnsureClient replacement restore: %v", err)
	}
	current := replacement.(*retryDocumentStateClient)
	if current != factory.clientAt(t, 1) || current.openCount != 1 {
		t.Fatalf("replacement client/open count = %p/%d, want second client with one DidOpen", current, current.openCount)
	}
	if got := current.documentText(uri); got != text {
		t.Fatalf("replacement managed text = %q, want %q", got, text)
	}
}

func TestDocumentSymbolRestoresReadyBootstrapAfterUnhealthyReplacement(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function ReadyReplacement() {}\n")
	factory := &retryDocumentStateFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("initial ready BootstrapDocument: %v", err)
	}
	factory.clientAt(t, 0).setHealthy(false)
	symbols, err := mgr.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("DocumentSymbol after unhealthy ready bootstrap: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "ready" {
		t.Fatalf("replacement document symbols = %#v, want ready symbol", symbols)
	}
	replacement := factory.clientAt(t, 1)
	if replacement.openCount != 1 || replacement.documentText(uri) != text {
		t.Fatalf("replacement target was not restored before request: opens=%d text=%q", replacement.openCount, replacement.documentText(uri))
	}
}

func TestBootstrapDocumentOpenOnlyRestoresReadyUnhealthyReplacement(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function OpenOnlyReplacement() {}\n")
	factory := &retryDocumentStateFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("initial open-only ready bootstrap: %v", err)
	}
	factory.clientAt(t, 0).setHealthy(false)
	if err := mgr.BootstrapDocumentOpenOnly(ctx, target); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly after unhealthy replacement: %v", err)
	}
	replacement := factory.clientAt(t, 1)
	if replacement.openCount != 1 || replacement.documentText(uri) != text {
		t.Fatalf("open-only replacement target = opens %d text %q, want one exact DidOpen", replacement.openCount, replacement.documentText(uri))
	}
}

func TestManagedWireClosedStateRequiresDidOpenAndCloseDoesNotRepeatWire(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function WireLifecycle() {}\n")
	factory := &retryDocumentStateFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("initial wire lifecycle bootstrap: %v", err)
	}
	client := factory.clientAt(t, 0)
	requireWireClosedAfterFailedReopen(t, mgr, client, ctx, target, uri)
	requireWireClosedChangeRejected(t, mgr, client, ctx, uri, text)
	requireDirectOpenRestoresWire(t, mgr, client, ctx, uri, text)
	requireWireClosedDidCloseDoesNotNotify(t, mgr, client, ctx, target, uri)
}

func requireWireClosedAfterFailedReopen(
	t *testing.T,
	mgr *manager,
	client *retryDocumentStateClient,
	ctx context.Context,
	target string,
	uri string,
) {
	t.Helper()
	client.setFailOpenAttempt(2)
	if err := mgr.ReopenDocumentForDiagnostics(ctx, target); err == nil {
		t.Fatal("partial close/open reopen unexpectedly succeeded")
	}
	state, ok := mgr.explicitDocumentForURI(uri)
	if !ok || state.wireOpen {
		t.Fatalf("state after partial reopen = %#v, present=%v; want wire closed", state, ok)
	}
}

func requireWireClosedChangeRejected(
	t *testing.T,
	mgr *manager,
	client *retryDocumentStateClient,
	ctx context.Context,
	uri string,
	text string,
) {
	t.Helper()
	if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: text}}); err == nil ||
		!strings.Contains(err.Error(), "DidOpen is required") {
		t.Fatalf("DidChange on wire-closed state error = %v, want DidOpen required", err)
	}
	if client.changeCount != 0 {
		t.Fatalf("DidChange notifications after wire close = %d, want zero", client.changeCount)
	}
}

func requireDirectOpenRestoresWire(
	t *testing.T,
	mgr *manager,
	client *retryDocumentStateClient,
	ctx context.Context,
	uri string,
	text string,
) {
	t.Helper()
	client.setFailOpenAttempt(0)
	if err := mgr.DidOpen(ctx, uri, "javascript", 2, text); err != nil {
		t.Fatalf("direct DidOpen recovery: %v", err)
	}
	if client.openAttempts != 3 || client.changeCount != 0 {
		t.Fatalf("direct recovery activity = open attempts %d changes %d, want 3/0", client.openAttempts, client.changeCount)
	}
}

func requireWireClosedDidCloseDoesNotNotify(
	t *testing.T,
	mgr *manager,
	client *retryDocumentStateClient,
	ctx context.Context,
	target string,
	uri string,
) {
	t.Helper()
	client.setFailOpenAttempt(4)
	if err := mgr.ReopenDocumentForDiagnostics(ctx, target); err == nil {
		t.Fatal("second partial close/open reopen unexpectedly succeeded")
	}
	closeCount := client.closeCount
	if err := mgr.DidClose(ctx, uri); err != nil {
		t.Fatalf("DidClose wire-closed managed state: %v", err)
	}
	if client.closeCount != closeCount {
		t.Fatalf("DidClose repeated wire close: before=%d after=%d", closeCount, client.closeCount)
	}
	if _, ok := mgr.explicitDocumentForURI(uri); ok {
		t.Fatal("DidClose retained wire-closed managed state")
	}
}

type blockingWorkspaceSymbolFactory struct {
	mu         sync.Mutex
	clients    []Client
	entered    chan<- struct{}
	release    <-chan struct{}
	requestErr error
}

func (f *blockingWorkspaceSymbolFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true,
		workspaceSymbolSupported: true, requestErr: f.requestErr,
	}
	var client Client = base
	if len(f.clients) == 0 {
		client = &blockingWorkspaceSymbolClient{strictWorkspaceSymbolClient: base, entered: f.entered, release: f.release}
	}
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *blockingWorkspaceSymbolFactory) firstClient(t *testing.T) Client {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.clients) == 0 {
		t.Fatal("workspace symbol client was not created")
	}
	return f.clients[0]
}

type blockingWorkspaceSymbolClient struct {
	*strictWorkspaceSymbolClient
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *blockingWorkspaceSymbolClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == protocol.MethodWorkspaceSymbol {
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.strictWorkspaceSymbolClient.Request(ctx, method, params)
}

type retryDocumentStateFactory struct {
	mu      sync.Mutex
	clients []*retryDocumentStateClient
}

func (f *retryDocumentStateFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &retryDocumentStateClient{documents: make(map[string]string), versions: make(map[string]int), healthy: true}
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *retryDocumentStateFactory) clientAt(t *testing.T, index int) *retryDocumentStateClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.clients) {
		t.Fatalf("client index %d missing from %d clients", index, len(f.clients))
	}
	return f.clients[index]
}

type retryDocumentStateClient struct {
	mu           sync.Mutex
	documents    map[string]string
	versions     map[string]int
	requestErr   error
	openCount    int
	requestCount int
	healthy      bool
	openAttempts int
	failOpenAt   int
	changeCount  int
	closeCount   int
}

func (c *retryDocumentStateClient) Initialize(context.Context, string) error  { return nil }
func (c *retryDocumentStateClient) Shutdown(context.Context) error            { return nil }
func (c *retryDocumentStateClient) Notify(context.Context, string, any) error { return nil }
func (c *retryDocumentStateClient) Close() error                              { return nil }
func (c *retryDocumentStateClient) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.healthy
}

func (c *retryDocumentStateClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestCount++
	if c.requestErr != nil {
		return nil, c.requestErr
	}
	if method != protocol.MethodHover {
		if method != protocol.MethodDocumentSymbol {
			return json.RawMessage("null"), nil
		}
		uri, err := documentURIFromParams(params)
		if err != nil {
			return nil, err
		}
		if _, ok := c.documents[uri]; !ok {
			return nil, fmt.Errorf("document symbol requested unopened %s", uri)
		}
		return json.Marshal([]protocol.DocumentSymbol{{
			Name: "ready", Kind: protocol.SymbolKindFunction,
			Range: protocol.Range{}, SelectionRange: protocol.Range{},
		}})
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var hover protocol.HoverParams
	if err := json.Unmarshal(payload, &hover); err != nil {
		return nil, err
	}
	return json.Marshal(protocol.HoverResult{Contents: protocol.MarkupContent{
		Kind: "plaintext", Value: c.documents[hover.TextDocument.URI],
	}})
}

func (c *retryDocumentStateClient) DidOpen(_ context.Context, uri, _ string, version int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openAttempts++
	if c.failOpenAt > 0 && c.openAttempts == c.failOpenAt {
		return fmt.Errorf("injected DidOpen failure at attempt %d", c.openAttempts)
	}
	if _, ok := c.documents[uri]; ok {
		return fmt.Errorf("duplicate DidOpen for %s", uri)
	}
	c.documents[uri] = text
	c.versions[uri] = version
	c.openCount++
	return nil
}

func (c *retryDocumentStateClient) DidChange(_ context.Context, uri string, version int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.documents[uri]; !ok {
		return fmt.Errorf("DidChange for unopened %s", uri)
	}
	if version <= c.versions[uri] {
		return fmt.Errorf("non-monotonic DidChange %d <= %d", version, c.versions[uri])
	}
	c.versions[uri] = version
	if len(changes) == 1 && changes[0].Range == nil {
		c.documents[uri] = changes[0].Text
	}
	c.changeCount++
	return nil
}

func (c *retryDocumentStateClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.documents, uri)
	delete(c.versions, uri)
	c.closeCount++
	return nil
}

func (c *retryDocumentStateClient) documentText(uri string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.documents[uri]
}

func (c *retryDocumentStateClient) setHealthy(healthy bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = healthy
}

func (c *retryDocumentStateClient) setFailOpenAttempt(attempt int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failOpenAt = attempt
}
