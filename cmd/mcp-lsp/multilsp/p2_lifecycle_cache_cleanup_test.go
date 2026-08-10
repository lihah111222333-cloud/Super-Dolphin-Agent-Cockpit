package multilsp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestReleaseScopeClearsDiagnosticsBootstrapAndCache(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-clean", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope: %v", err)
	}
	uri := fileURIFromPath(filepath.Join(root, "main.go"))
	coordinator := mustBootstrapCoordinator(t, scoped)
	key := resolved.cacheKey("go", uri)
	coordinator.cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()})
	coordinator.states.complete(resolved.bootstrapKey(), uri, "fp", 1)
	scoped.diagnostics[diagnosticStoreKeyFor(resolved, uri).String()] = diagnosticSnapshot{
		scopeKey:     resolved.ScopeKey,
		workspaceKey: resolved.WorkspaceKey,
		language:     "go",
		uri:          uri,
		generation:   scoped.CurrentDiagnosticGeneration(),
		state:        diagnosticStateReady,
		params:       protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "stale"}}},
	}

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-clean", ThreadID: "thread-1", Drain: true})
	if err != nil {
		t.Fatalf("ReleaseScope(clean): %v", err)
	}
	if result.ClosedManagers != 1 {
		t.Fatalf("ClosedManagers = %d, want 1", result.ClosedManagers)
	}
	scoped.coordinatorMu.Lock()
	hasCoordinator := scoped.coordinator != nil
	scoped.coordinatorMu.Unlock()
	if hasCoordinator {
		t.Fatalf("bootstrap/cache coordinator survived ReleaseScope close")
	}
	if !managerIsClosed(scoped) {
		t.Fatalf("scoped manager was not closed")
	}
}

func TestReleaseScopeRejectsEmptyIdentityWithoutExplicitScopeKind(t *testing.T) {
	mgr := newManagerPoolTestManager(t, canonicalScopePath(t.TempDir(), ""))
	if _, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{}); err == nil {
		t.Fatalf("ReleaseScope(empty) error = nil, want explicit scope kind/identity rejection")
	}
}

func TestDidCloseDoesNotRecreateBootstrapCoordinatorAfterManagerClose(t *testing.T) {
	root, target, initial := writeWorkspaceSymbolSyncFixture(t, "close-race.js", "function CloseRace() {}\n")
	base := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true, workspaceSymbolSupported: true,
	}
	client := &didCloseBarrierClient{
		strictWorkspaceSymbolClient: base, entered: make(chan struct{}), release: make(chan struct{}),
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root, DisableInitialWorkspaceBootstrap: true,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) { return client, nil }),
	}).(*manager)
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	if err := mgr.DidOpen(ctx, target, "javascript", 1, initial); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	_ = mustBootstrapCoordinator(t, mgr)
	didCloseDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.didCloseCoordinatorRace.closeDocument", func(context.Context) {
		didCloseDone <- mgr.DidClose(ctx, target)
	})
	waitForProvisionalSignal(t, client.entered, "DidClose wire call")
	mgr.explicitOpenMu.Lock()
	close(client.release)
	waitForClientLeaseRelease(t, mgr, client)
	closeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.didCloseCoordinatorRace.closeManager", func(context.Context) {
		closeDone <- mgr.Close()
	})
	if err := <-closeDone; err != nil {
		mgr.explicitOpenMu.Unlock()
		t.Fatalf("Manager.Close: %v", err)
	}
	mgr.explicitOpenMu.Unlock()
	if err := <-didCloseDone; err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	mgr.coordinatorMu.Lock()
	defer mgr.coordinatorMu.Unlock()
	if mgr.coordinator != nil {
		t.Fatal("DidClose recreated bootstrap coordinator after Manager.Close")
	}
}

func TestManagerCloseAttemptTimeoutRetainsDetachedCleanupForRetry(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"timeout-owner"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	didOpenErr := errors.New("bootstrap didOpen failed before timeout")
	closeErr := errors.New("detached close failed after timeout")
	client := &detachedBootstrapAbortClient{
		didOpenErr: didOpenErr, closeErr: closeErr, closeStart: make(chan struct{}), closeAllow: make(chan struct{}),
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) { return client, nil }),
	}).(*manager)
	mgr.bootstrapAttemptWaitTimeout = 10 * time.Millisecond
	ctx := ctxWithCWD(root, "agent-timeout-owner", "thread-timeout-owner")
	ensureDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.timeoutOwner.ensure", func(context.Context) {
		_, err := mgr.EnsureClient(ctx, "", "javascript")
		ensureDone <- err
	})
	waitForDetachedBootstrapClose(t, client.closeStart, ensureDone)
	done, firstErr := mgr.closeWithoutPoolStatus()
	if done || !errors.Is(firstErr, context.DeadlineExceeded) {
		t.Fatalf("first Close status = done:%v err:%v, want incomplete deadline", done, firstErr)
	}
	if mgr.closeInitialized || mgr.closeComplete {
		t.Fatalf("timed-out Close initialized=%v complete=%v", mgr.closeInitialized, mgr.closeComplete)
	}
	close(client.closeAllow)
	ensureErr := waitForLifecycleError(t, ensureDone, "timed-out bootstrap owner")
	if !errors.Is(ensureErr, didOpenErr) || !errors.Is(ensureErr, closeErr) {
		t.Fatalf("EnsureClient error = %v, want didOpen and Close failures", ensureErr)
	}
	done, retryErr := mgr.closeWithoutPoolStatus()
	if !done || retryErr != nil {
		t.Fatalf("retry Close status = done:%v err:%v", done, retryErr)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closeCalls != 2 {
		t.Fatalf("detached client Close calls = %d, want failure plus retry", client.closeCalls)
	}
}

func TestManagerCloseInitializeTimeoutRemainsRetryable(t *testing.T) {
	closeErr := errors.New("initialize owner close failed once")
	initializeStarted := make(chan struct{})
	initializeRelease := make(chan struct{})
	client := &provisionalCleanupClient{
		initializeStarted: initializeStarted,
		initializeRelease: initializeRelease,
		closeErrors:       []error{closeErr, nil},
	}
	factory := &provisionalCleanupFactory{clients: []Client{client}}
	mgr := &manager{
		instanceID:                  "test-initialize-timeout-manager",
		factory:                     ClientFactoryFunc(factory.newClient),
		workspaces:                  make(map[string]*workspaceClient),
		bootstrapAttemptWaitTimeout: 10 * time.Millisecond,
	}
	cfg := workspaceConfig{
		key: "initialize-timeout:go", rootPath: t.TempDir(), rootURI: "file:///initialize-timeout", languageID: "go",
	}
	ensureDone := make(chan provisionalEnsureResult, 1)
	safego.Go(context.Background(), nil, "multilsp.initializeTimeout.ensure", func(context.Context) {
		got, err := mgr.ensureClient(context.Background(), cfg)
		ensureDone <- provisionalEnsureResult{client: got, err: err}
	})
	waitForProvisionalSignal(t, initializeStarted, "client initialize start")
	done, firstErr := mgr.closeWithoutPoolStatus()
	assertInitializeCloseTimedOut(t, mgr, client, done, firstErr)
	close(initializeRelease)
	ensureResult := waitForProvisionalEnsureResult(t, ensureDone)
	assertInitializeOwnerCleanupFailed(t, ensureResult, closeErr)
	done, retryErr := mgr.closeWithoutPoolStatus()
	assertInitializeCloseRetry(t, client, done, retryErr)
}

func assertInitializeCloseTimedOut(
	t *testing.T,
	mgr *manager,
	client *provisionalCleanupClient,
	done bool,
	err error,
) {
	t.Helper()
	if done || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close status = done:%v err:%v, want incomplete deadline", done, err)
	}
	if mgr.closeInitialized || mgr.closeComplete || client.closeCallCount() != 0 {
		t.Fatalf("timed-out Close initialized=%v complete=%v clientClose=%d", mgr.closeInitialized, mgr.closeComplete, client.closeCallCount())
	}
}

func assertInitializeOwnerCleanupFailed(t *testing.T, result provisionalEnsureResult, closeErr error) {
	t.Helper()
	if result.client != nil || !errors.Is(result.err, ErrManagerClosed) || !errors.Is(result.err, closeErr) {
		t.Fatalf("racing ensure result = client:%T err:%v, want manager closed and cleanup failure", result.client, result.err)
	}
}

func assertInitializeCloseRetry(t *testing.T, client *provisionalCleanupClient, done bool, err error) {
	t.Helper()
	if !done || err != nil {
		t.Fatalf("retry Close status = done:%v err:%v", done, err)
	}
	if got := client.closeCallCount(); got != 2 {
		t.Fatalf("provisional client Close calls = %d, want failed owner cleanup plus retry", got)
	}
}

func TestManagedDidChangeCannotRecreateCoordinatorAfterManagerClose(t *testing.T) {
	root, target, initial := writeWorkspaceSymbolSyncFixture(t, "change-close-race.js", "function Initial() {}\n")
	client := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true, workspaceSymbolSupported: true,
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root, DisableInitialWorkspaceBootstrap: true,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) { return client, nil }),
	}).(*manager)
	publishEntered := make(chan struct{})
	publishRelease := make(chan struct{})
	mgr.bootstrapCoordinatorBeforePublish = func() {
		close(publishEntered)
		<-publishRelease
	}
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	if err := mgr.DidOpen(ctx, target, "javascript", 1, initial); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	changeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.didChangeCoordinatorRace.change", func(context.Context) {
		change := []protocol.TextDocumentContentChangeEvent{{Text: "function Changed() {}\n"}}
		changeDone <- mgr.DidChange(ctx, target, 2, change)
	})
	waitForProvisionalSignal(t, publishEntered, "managed DidChange coordinator publish")
	if err := mgr.closeWithoutPool(); err != nil {
		t.Fatalf("Manager.Close: %v", err)
	}
	close(publishRelease)
	if err := waitForLifecycleError(t, changeDone, "full DidChange after Close"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("DidChange error = %v, want ErrManagerClosed", err)
	}
	mgr.coordinatorMu.Lock()
	defer mgr.coordinatorMu.Unlock()
	if mgr.coordinator != nil {
		t.Fatal("managed DidChange recreated coordinator after Manager.Close")
	}
}

func TestManagedDidChangeCannotCommitToDetachedCoordinatorAfterManagerClose(t *testing.T) {
	root, target, initial := writeWorkspaceSymbolSyncFixture(t, "change-detached-race.js", "function Initial() {}\n")
	client := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true, workspaceSymbolSupported: true,
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root, DisableInitialWorkspaceBootstrap: true,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) { return client, nil }),
	}).(*manager)
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	if err := mgr.DidOpen(ctx, target, "javascript", 1, initial); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	coordinator := mustBootstrapCoordinator(t, mgr)
	cacheEntered := make(chan struct{})
	cacheRelease := make(chan struct{})
	var cacheOnce sync.Once
	coordinator.cache.now = func() time.Time {
		cacheOnce.Do(func() { close(cacheEntered) })
		<-cacheRelease
		return time.Unix(1700000000, 0)
	}
	changeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.detachedCoordinator.change", func(context.Context) {
		change := []protocol.TextDocumentContentChangeEvent{{Text: "function Changed() {}\n"}}
		changeDone <- mgr.DidChange(ctx, target, 2, change)
	})
	waitForProvisionalSignal(t, cacheEntered, "managed DidChange cache mutation")
	closeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.detachedCoordinator.close", func(context.Context) {
		closeDone <- mgr.closeWithoutPool()
	})
	waitForCoordinatorClosing(t, coordinator)
	close(cacheRelease)
	if err := waitForLifecycleError(t, changeDone, "detached coordinator DidChange"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("DidChange error = %v, want ErrManagerClosed", err)
	}
	if err := waitForLifecycleError(t, closeDone, "manager close after coordinator mutation"); err != nil {
		t.Fatalf("Manager.Close: %v", err)
	}
	assertCoordinatorClosedWithoutLateState(t, mgr, coordinator)
}

func waitForCoordinatorClosing(t *testing.T, coordinator *bootstrapCoordinator) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if coordinator.closed.Load() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("bootstrap coordinator did not enter closing state")
}

func assertCoordinatorClosedWithoutLateState(t *testing.T, mgr *manager, coordinator *bootstrapCoordinator) {
	t.Helper()
	mgr.coordinatorMu.Lock()
	active := mgr.coordinator
	mgr.coordinatorMu.Unlock()
	if active != nil {
		t.Fatal("closed manager retained bootstrap coordinator")
	}
	coordinator.cache.mu.RLock()
	cacheDocuments, cacheIndex := len(coordinator.cache.memory), len(coordinator.cache.index)
	coordinator.cache.mu.RUnlock()
	if cacheDocuments != 0 || cacheIndex != 0 {
		t.Fatalf("detached cache mutated after close: documents=%d index=%d", cacheDocuments, cacheIndex)
	}
	coordinator.states.mu.Lock()
	stateEntries := len(coordinator.states.entries)
	coordinator.states.mu.Unlock()
	if stateEntries != 0 {
		t.Fatalf("detached bootstrap state mutated after close: entries=%d", stateEntries)
	}
}

func TestDocumentOperationGateBoundsSameURIWaitersAndRestoresCapacity(t *testing.T) {
	mgr := &manager{documentOperationWaiterLimit: 2}
	uri := "file:///workspace/bounded-waiters.go"
	held, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationObserveSync)
	if err != nil {
		t.Fatalf("acquire held operation: %v", err)
	}
	waitCtx, cancelWait := context.WithCancel(context.Background())
	waiterDone := beginDocumentOperationAsync(mgr, waitCtx, uri)
	waitForDocumentOperationRefs(t, mgr, uri, 2)
	if _, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationDidChange); err == nil || !strings.Contains(err.Error(), "waiter limit 2") {
		held.release()
		cancelWait()
		t.Fatalf("over-cap operation error = %v, want waiter limit rejection", err)
	}
	cancelWait()
	if result := waitForDocumentOperationResult(t, waiterDone); !errors.Is(result.err, context.Canceled) {
		held.release()
		t.Fatalf("canceled waiter error = %v, want context canceled", result.err)
	}
	nextDone := beginDocumentOperationAsync(mgr, context.Background(), uri)
	waitForDocumentOperationRefs(t, mgr, uri, 2)
	held.release()
	next := waitForDocumentOperationResult(t, nextDone)
	if next.err != nil || next.token == nil {
		t.Fatalf("operation after capacity release = token:%v err:%v", next.token, next.err)
	}
	next.token.release()
}

func TestDocumentOperationGateReleaseRechecksClosedStateAtomically(t *testing.T) {
	mgr := &manager{
		documentOperationLimit:       maxDocumentOperationGates,
		documentOperationWaiterLimit: maxDocumentOperationWaiters,
	}
	uri := "file:///workspace/close-open-release.go"
	closeToken, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationDidClose)
	if err != nil {
		t.Fatalf("begin DidClose: %v", err)
	}
	closeToken.commitMutation()
	openDone := beginDocumentOperationKindAsync(mgr, context.Background(), uri, documentOperationDidOpen)
	waitForDocumentOperationRefs(t, mgr, uri, 2)

	closeReleaseEntered := make(chan struct{})
	resumeCloseRelease := make(chan struct{})
	var blockCloseOnce sync.Once
	mgr.documentOperationReleaseHook = func(kind documentOperationKind) {
		if kind == documentOperationDidClose {
			blockCloseOnce.Do(func() {
				close(closeReleaseEntered)
				<-resumeCloseRelease
			})
		}
	}
	closeReleaseDone := releaseDocumentOperationAsync(closeToken)
	waitForProvisionalSignal(t, closeReleaseEntered, "DidClose reference release")
	openResult := waitForDocumentOperationResult(t, openDone)
	if openResult.err != nil || openResult.token == nil {
		t.Fatalf("queued DidOpen = token:%v err:%v", openResult.token, openResult.err)
	}
	openResult.token.commitMutation()
	openResult.token.release()
	close(resumeCloseRelease)
	if err := waitForLifecycleError(t, closeReleaseDone, "DidClose reference release"); err != nil {
		t.Fatalf("DidClose release: %v", err)
	}
	mgr.documentOperationReleaseHook = nil
	assertDocumentOperationRegistrySize(t, mgr, 0)
	assertDocumentOperationGateCapacityRecycled(t, mgr)
}

func TestManagedOpenCloseHistoryReleasesDocumentOperationGateCapacity(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := NewManager(Config{
		WorkspaceRoot:                    root,
		ClientFactory:                    ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) { return &noopClient{}, nil }),
		DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID: "managed-close-capacity", ThreadID: "thread-1", CWD: root, WorkspaceRoots: []string{root},
	})
	for index := 0; index <= maxDocumentOperationGates; index++ {
		uri := fileURIFromPath(filepath.Join(root, fmt.Sprintf("closed-%d.js", index)))
		if err := mgr.DidOpen(ctx, uri, "javascript", 1, "function ClosedHistory() {}\n"); err != nil {
			t.Fatalf("DidOpen managed history %d: %v", index, err)
		}
		if err := mgr.DidClose(ctx, uri); err != nil {
			t.Fatalf("DidClose managed history %d: %v", index, err)
		}
	}
	assertDocumentOperationRegistrySize(t, mgr, 0)
}

func assertDocumentOperationGateCapacityRecycled(t *testing.T, mgr *manager) {
	t.Helper()
	for index := 0; index <= maxDocumentOperationGates; index++ {
		uri := fmt.Sprintf("file:///workspace/recycled-%d.go", index)
		closed, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationDidClose)
		if err != nil {
			t.Fatalf("begin recycled DidClose %d: %v", index, err)
		}
		closed.commitMutation()
		closed.release()
		opened, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationDidOpen)
		if err != nil {
			t.Fatalf("begin recycled DidOpen %d: %v", index, err)
		}
		opened.commitMutation()
		opened.release()
	}
	assertDocumentOperationRegistrySize(t, mgr, 0)
}

func assertDocumentOperationRegistrySize(t *testing.T, mgr *manager, want int) {
	t.Helper()
	mgr.documentOperationMu.Lock()
	defer mgr.documentOperationMu.Unlock()
	if got := len(mgr.documentOperations); got != want {
		t.Fatalf("document operation registry size = %d, want %d", got, want)
	}
}

type documentOperationBeginResult struct {
	token *documentOperationToken
	err   error
}

func beginDocumentOperationAsync(mgr *manager, ctx context.Context, uri string) <-chan documentOperationBeginResult {
	return beginDocumentOperationKindAsync(mgr, ctx, uri, documentOperationDidChange)
}

func beginDocumentOperationKindAsync(mgr *manager, ctx context.Context, uri string, kind documentOperationKind) <-chan documentOperationBeginResult {
	done := make(chan documentOperationBeginResult, 1)
	safego.Go(context.Background(), nil, "multilsp.boundedDocumentOperation.begin", func(context.Context) {
		token, err := mgr.beginDocumentOperation(ctx, uri, kind)
		done <- documentOperationBeginResult{token: token, err: err}
	})
	return done
}

func releaseDocumentOperationAsync(token *documentOperationToken) <-chan error {
	done := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.documentOperation.release", func(context.Context) {
		token.release()
		done <- nil
	})
	return done
}

func waitForDocumentOperationRefs(t *testing.T, mgr *manager, uri string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.documentOperationMu.Lock()
		gate := mgr.documentOperations[uri]
		refs := 0
		if gate != nil {
			refs = gate.refs
		}
		mgr.documentOperationMu.Unlock()
		if refs == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("document operation refs for %s did not reach %d", uri, want)
}

func waitForDocumentOperationResult(t *testing.T, done <-chan documentOperationBeginResult) documentOperationBeginResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for document operation")
		return documentOperationBeginResult{}
	}
}

type didCloseBarrierClient struct {
	*strictWorkspaceSymbolClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *didCloseBarrierClient) DidClose(ctx context.Context, uri string) error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.strictWorkspaceSymbolClient.DidClose(ctx, uri)
}

func waitForLifecycleError(t *testing.T, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return nil
	}
}
