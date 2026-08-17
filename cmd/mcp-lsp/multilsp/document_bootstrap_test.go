package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestDocumentRequestBootstrapsFreshSnapshotForJavaScript(t *testing.T) {
	root := t.TempDir()
	ctx := ctxWithCWD(root, "agent-bootstrap", "thread-bootstrap")
	writeBootstrapTestFile(t, filepath.Join(root, "package.json"), `{"name":"multilsp-test"}`)

	target := filepath.Join(root, "app.js")
	writeBootstrapTestFile(t, target, "function staleName() { return 1; }\n")

	factory := &recordingClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	t.Cleanup(func() { closeBootstrapTestManager(t, manager) })

	if err := manager.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("bootstrap stale document: %v", err)
	}
	client := requireRecordingClient(t, factory)
	if got := client.openCount(); got != 1 {
		t.Fatalf("initial bootstrap should open the JS document once, got %d", got)
	}

	writeBootstrapTestFile(t, target, "function freshName() { return 2; }\n")
	client.expectRequestContent("freshName")

	symbols, err := manager.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("document symbol after external edit: %v", err)
	}
	assertFreshDocumentSymbol(t, symbols)
	if got := client.changeCount(); got != 1 {
		t.Fatalf("document request should push exactly one DidChange after disk edit, got %d", got)
	}
	if !client.anyDocumentContains("freshName") {
		t.Fatalf("client snapshot was not refreshed, documents=%#v", client.documentSnapshot())
	}
}

func TestDocumentRequestPreservesDirtyEditorSnapshotForJavaScript(t *testing.T) {
	root := t.TempDir()
	ctx := ctxWithCWD(root, "agent-dirty-snapshot", "thread-dirty-snapshot")
	writeBootstrapTestFile(t, filepath.Join(root, "package.json"), `{"name":"multilsp-dirty-test"}`)
	target := filepath.Join(root, "app.js")
	writeBootstrapTestFile(t, target, "function diskName() { return 1; }\n")
	uri := fileURIFromPath(target)

	factory := &recordingClientFactory{}
	manager := NewManager(Config{
		WorkspaceRoot:      root,
		ClientFactory:      factory,
		DiagnosticsMaxWait: 1,
	})
	t.Cleanup(func() { closeBootstrapTestManager(t, manager) })

	const editorText = "function editorName() { return 2; }\n"
	if err := manager.DidOpen(ctx, uri, "javascript", 1, editorText); err != nil {
		t.Fatalf("open dirty editor document: %v", err)
	}
	client := requireRecordingClient(t, factory)
	client.expectRequestContent("editorName")

	symbols, err := manager.DocumentSymbol(ctx, target)
	if err != nil {
		t.Fatalf("document symbol for dirty editor snapshot: %v", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "editorName" {
		t.Fatalf("dirty editor symbol result = %#v, want editorName", symbols)
	}
	if got := client.changeCount(); got != 0 {
		t.Fatalf("document request overwrote dirty editor snapshot with %d DidChange notifications", got)
	}
}

func TestDidOpenCommitAndManagerCloseAreAtomicallyOrdered(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function CloseBarrier() {}\n")
	openEntered := make(chan struct{})
	openRelease := make(chan struct{})
	factory := &strictWorkspaceSymbolFactory{openEntered: openEntered, openRelease: openRelease}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	openDone := make(chan error, 1)
	group := newTestGoroutineGroup(t)
	group.Go(func() { openDone <- mgr.DidOpen(ctx, uri, "javascript", 1, text) })
	waitWorkspaceSymbolSignal(t, openEntered, "DidOpen close barrier")
	client := factory.clientsSnapshot()[0]

	mgr.explicitOpenMu.Lock()
	locked := true
	defer func() {
		if locked {
			mgr.explicitOpenMu.Unlock()
		}
	}()
	close(openRelease)
	waitForClientLeaseRelease(t, mgr, client)
	waitForRecipientCommitReadLock(t, mgr)
	closeDone := make(chan error, 1)
	group.Go(func() { closeDone <- mgr.Close() })
	assertWorkspaceSymbolOperationBlocked(t, closeDone, "Manager.Close during atomic DidOpen commit")
	mgr.explicitOpenMu.Unlock()
	locked = false
	if err := awaitWorkspaceSymbolError(t, openDone, "atomic DidOpen commit"); err != nil {
		t.Fatalf("DidOpen won atomic commit ordering: %v", err)
	}
	if err := awaitWorkspaceSymbolError(t, closeDone, "Manager.Close after DidOpen commit"); err != nil {
		t.Fatalf("Manager.Close: %v", err)
	}
	if _, ok := mgr.explicitDocumentForURI(uri); !ok {
		t.Fatal("DidOpen state missing after commit linearized before Manager.Close")
	}
}

func TestWorkspaceSymbolBatchSyncDoesNotCommitAfterRecipientReplacement(t *testing.T) {
	root, first, firstText := writeWorkspaceSymbolSyncFixture(t, "a.js", "function BatchOneOld() {}\n")
	second := filepath.Join(root, "b.js")
	secondText := "function BatchTwoOld() {}\n"
	writeGenericTestFile(t, second, secondText)
	changeBatchEntered := make(chan struct{})
	changeBatchRelease := make(chan struct{})
	factory := &strictWorkspaceSymbolFactory{
		changeBatchEntered: changeBatchEntered, changeBatchRelease: changeBatchRelease, changeBatchTarget: 2,
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, first, "javascript", true)
	firstURI := fileURIFromPath(first)
	secondURI := fileURIFromPath(second)
	if err := mgr.DidOpen(ctx, firstURI, "javascript", 1, firstText); err != nil {
		t.Fatalf("DidOpen first batch document: %v", err)
	}
	if err := mgr.DidOpen(ctx, secondURI, "javascript", 1, secondText); err != nil {
		t.Fatalf("DidOpen second batch document: %v", err)
	}
	beforeFirst, _ := mgr.explicitDocumentForURI(firstURI)
	beforeSecond, _ := mgr.explicitDocumentForURI(secondURI)
	writeGenericTestFile(t, first, "function BatchOneFresh() {}\n")
	writeGenericTestFile(t, second, "function BatchTwoFresh() {}\n")

	workspaceDone := make(chan error, 1)
	group := newTestGoroutineGroup(t)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "Batch", "javascript")
		workspaceDone <- err
	})
	waitWorkspaceSymbolSignal(t, changeBatchEntered, "second batch workspace sync DidChange")
	oldClient := factory.clientsSnapshot()[0]
	replacement, err := mgr.rebuildClientAfterFailure(ctx, oldClient, false)
	if err != nil {
		t.Fatalf("replace batch sync recipient: %v", err)
	}
	close(changeBatchRelease)
	if err := awaitWorkspaceSymbolError(t, workspaceDone, "batch WorkspaceSymbol"); !errors.Is(err, ErrStaleClientLease) {
		t.Fatalf("batch workspace sync replacement error = %v, want ErrStaleClientLease", err)
	}
	afterFirst, _ := mgr.explicitDocumentForURI(firstURI)
	afterSecond, _ := mgr.explicitDocumentForURI(secondURI)
	if afterFirst != beforeFirst || afterSecond != beforeSecond {
		t.Fatalf("stale batch state committed: first=%#v second=%#v", afterFirst, afterSecond)
	}
	if got := replacement.(*strictWorkspaceSymbolClient).notificationCount(); got != 0 {
		t.Fatalf("replacement batch notifications = %d, want zero", got)
	}
}

func TestOpenManagedSnapshotKeepsAuthoritativeReadCleanAcrossLaterDiskWrite(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function SnapshotOld() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	ref, cfg, scope, client, snapshot := prepareAuthoritativeSnapshotFixture(t, mgr, ctx, target)
	writeGenericTestFile(t, target, "function SnapshotFresh() {}\n")
	req := snapshotSyncRequest{scope: scope, version: 1}
	if err := mgr.openManagedSnapshot(ctx, client, cfg, snapshot, &req); err != nil {
		t.Fatalf("open authoritative old snapshot after disk write: %v", err)
	}
	requireAuthoritativeSnapshotState(t, mgr, ref.uri, snapshot.fingerprint)
	results, err := mgr.WorkspaceSymbol(ctx, "SnapshotFresh", "javascript")
	if err != nil {
		t.Fatalf("WorkspaceSymbol refresh after authoritative snapshot: %v", err)
	}
	assertStrictWorkspaceSymbolResult(t, results, "SnapshotFresh", ref.uri)
	if got := factory.clientsSnapshot()[0].changeCount(); got != 1 {
		t.Fatalf("authoritative snapshot refresh changes = %d, want one", got)
	}
}

func TestReadDocumentSnapshotRejectsOrBlocksAtomicPathReplacementAfterOpen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "atomic.js")
	replacement := filepath.Join(root, "atomic.next.js")
	writeBootstrapTestFile(t, target, "function OldAtomic() {}\n")
	writeBootstrapTestFile(t, replacement, "function NewAtomic() {}\n")
	ref := documentRef{uri: fileURIFromPath(target), absPath: target, languageID: "javascript"}
	var renameErr error
	snapshot, err := readDocumentSnapshotWithLimitAfterOpen(ref, defaultCleanDocumentByteLimit, func() {
		renameErr = os.Rename(replacement, target)
	})
	assertAtomicReplacementOutcome(t, renameErr, snapshot, err)
}

func prepareAuthoritativeSnapshotFixture(
	t *testing.T,
	mgr *manager,
	ctx context.Context,
	target string,
) (documentRef, workspaceConfig, ResolvedLSPToolScope, Client, documentSnapshot) {
	t.Helper()
	ref, err := mgr.resolveDocumentRef(ctx, target, "javascript")
	if err != nil {
		t.Fatalf("resolve authoritative snapshot document: %v", err)
	}
	cfg, err := mgr.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		t.Fatalf("resolve authoritative snapshot workspace: %v", err)
	}
	scope, err := mgr.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("resolve authoritative snapshot scope: %v", err)
	}
	client, err := mgr.ensurePublishedClient(ctx, cfg)
	if err != nil {
		t.Fatalf("ensure authoritative snapshot client: %v", err)
	}
	snapshot, err := readDocumentSnapshot(ref)
	if err != nil {
		t.Fatalf("read authoritative old snapshot: %v", err)
	}
	return ref, cfg, scope, client, snapshot
}

func requireAuthoritativeSnapshotState(t *testing.T, mgr *manager, uri, fingerprint string) {
	t.Helper()
	state, ok := mgr.explicitDocumentForURI(uri)
	if !ok || !state.diskClean || state.diskFingerprint != fingerprint || state.fullTextKnown {
		t.Fatalf("authoritative snapshot state = %#v, present=%v", state, ok)
	}
}

func waitForClientLeaseRelease(t *testing.T, mgr *manager, client Client) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.RLock()
		active := 0
		for _, workspace := range mgr.workspaces {
			if workspace != nil && workspace.client == client {
				active = workspace.activeLeases
			}
		}
		mgr.mu.RUnlock()
		if active == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for client lease release")
}

func waitForRecipientCommitReadLock(t *testing.T, mgr *manager) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !mgr.mu.TryLock() {
			return
		}
		mgr.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for exact-recipient commit read lock")
}

func TestBootstrapDocumentThenLanguageWorkspaceSymbolUsesManagedFreshSnapshot(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function LegacyStale() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	uri := fileURIFromPath(target)

	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument legacy path: %v", err)
	}
	if mgr.isExplicitDocumentOpen(uri) {
		t.Fatal("internal bootstrap was classified as a user-opened document")
	}
	writeBootstrapTestFile(t, target, "function LegacyFresh() {}\n")
	results, err := mgr.WorkspaceSymbol(ctx, "LegacyFresh", "javascript")
	if err != nil {
		t.Fatalf("WorkspaceSymbol after legacy bootstrap rewrite: %v", err)
	}
	assertStrictWorkspaceSymbolResult(t, results, "LegacyFresh", uri)
	stale, err := mgr.WorkspaceSymbol(ctx, "LegacyStale", "javascript")
	if err != nil {
		t.Fatalf("stale WorkspaceSymbol query: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("stale WorkspaceSymbol results = %#v, want empty", stale)
	}
	client := factory.clientContainingURI(t, uri)
	if got, want := client.eventsSnapshot(), []string{
		"open:" + uri + ":1",
		"change:" + uri + ":2",
		"request:LegacyFresh",
		"request:LegacyStale",
	}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("legacy managed events = %#v, want %#v", got, want)
	}
	if err := mgr.DidClose(ctx, uri); err != nil {
		t.Fatalf("DidClose internally bootstrapped document: %v", err)
	}
	if client.hasDocument(uri) {
		t.Fatalf("DidClose left internally bootstrapped document open; events=%#v", client.eventsSnapshot())
	}
}

func writeBootstrapTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func closeBootstrapTestManager(t *testing.T, manager Manager) {
	t.Helper()
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func requireRecordingClient(t *testing.T, factory *recordingClientFactory) *recordingClient {
	t.Helper()
	client := factory.currentClient()
	if client == nil {
		t.Fatal("expected bootstrap to create an LSP client")
	}
	return client
}

func assertFreshDocumentSymbol(t *testing.T, symbols []protocol.DocumentSymbol) {
	t.Helper()
	if len(symbols) != 1 || symbols[0].Name != "freshName" {
		t.Fatalf("expected fresh symbol result, got %#v", symbols)
	}
}

type recordingClientFactory struct {
	mu     sync.Mutex
	client *recordingClient
}

func (f *recordingClientFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.client = &recordingClient{documents: map[string]string{}}
	return f.client, nil
}

func (f *recordingClientFactory) currentClient() *recordingClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.client
}

type recordingClient struct {
	mu                sync.Mutex
	documents         map[string]string
	expectedSubstring string
	didOpenCount      int
	didChangeCount    int
}

func (c *recordingClient) Initialize(context.Context, string) error {
	return nil
}

func (c *recordingClient) Shutdown(context.Context) error {
	return nil
}

func (c *recordingClient) Request(_ context.Context, method string, params any) (json.RawMessage, error) {
	if method != protocol.MethodDocumentSymbol {
		return json.RawMessage("null"), nil
	}
	uri, err := documentURIFromParams(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	text := c.documents[uri]
	expected := c.expectedSubstring
	c.mu.Unlock()
	if expected != "" && !strings.Contains(text, expected) {
		return nil, fmt.Errorf("document request used stale snapshot: expected %q in %q", expected, text)
	}
	name := expected
	if name == "" {
		name = "symbol"
	}
	return json.Marshal([]protocol.DocumentSymbol{{
		Name:           name,
		Kind:           protocol.SymbolKindFunction,
		Range:          protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: len(text)}},
		SelectionRange: protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: len(name)}},
	}})
}

func (c *recordingClient) Notify(context.Context, string, any) error {
	return nil
}

func (c *recordingClient) DidOpen(_ context.Context, uri, _ string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didOpenCount++
	c.documents[uri] = text
	return nil
}

func (c *recordingClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didChangeCount++
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}

func (c *recordingClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.documents, uri)
	return nil
}

func (c *recordingClient) Close() error {
	return nil
}

func (c *recordingClient) expectRequestContent(substring string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expectedSubstring = substring
}

func (c *recordingClient) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didOpenCount
}

func (c *recordingClient) changeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.didChangeCount
}

func (c *recordingClient) anyDocumentContains(substring string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, text := range c.documents {
		if strings.Contains(text, substring) {
			return true
		}
	}
	return false
}

func (c *recordingClient) documentSnapshot() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.documents))
	maps.Copy(out, c.documents)
	return out
}

func documentURIFromParams(params any) (string, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	var decoded protocol.DocumentSymbolParams
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", err
	}
	return decoded.TextDocument.URI, nil
}
