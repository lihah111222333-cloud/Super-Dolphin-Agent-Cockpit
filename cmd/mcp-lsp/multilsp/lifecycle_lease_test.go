package multilsp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func ageWorkspaceForLifecycleTest(t *testing.T, mgr *manager, client Client) {
	t.Helper()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			workspace.generation = 1
			workspace.state = workspaceStateIdleCountdown
			workspace.idleSince = time.Now().Add(-2 * idleTimeoutForTest())
			workspace.lastActivity = workspace.idleSince
			return
		}
	}
	t.Fatalf("workspace for %T was not found", client)
}

// RED: lease release must publish the end of the active interval; an acquire-only
// timestamp cannot prove that the full idle window elapsed after the last release.
func TestLeaseReleaseStartsFullIdleWindow(t *testing.T) {
	mgr := &manager{
		workspaces: make(map[string]*workspaceClient),
	}
	client := noopClient{}
	old := time.Now().Add(-time.Hour)
	mgr.workspaces["workspace"] = &workspaceClient{key: "workspace", client: client, generation: 1, state: workspaceStateActive, lastActivity: old}
	leased, bound, err := mgr.leaseBoundClient(client)
	if err != nil || !bound {
		t.Fatalf("leaseBoundClient() = bound=%v err=%v, want bound without error", bound, err)
	}
	releasedAt := time.Now()
	leased.Release()

	got := mgr.workspaces["workspace"].lastActivity
	if got.Before(releasedAt) {
		t.Fatalf("lastActivity after release = %s, want at or after %s", got, releasedAt)
	}
}

func TestBootstrappingCannotBeLeased(t *testing.T) {
	mgr := &manager{workspaces: make(map[string]*workspaceClient)}
	client := noopClient{}
	mgr.workspaces["workspace"] = &workspaceClient{key: "workspace", client: client, generation: 1, state: workspaceStateBootstrapping}
	if _, bound, err := mgr.leaseBoundClient(client); !errors.Is(err, ErrClientNotReady) || bound {
		t.Fatalf("bootstrapping lease = bound=%v err=%v, want false/ErrClientNotReady", bound, err)
	}
}

func TestReleaseScopeZeroLeaseBeforeIdleTimeoutDoesNotClose(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-idle-gate", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	client := &p2LifecycleClient{}
	scoped.mu.Lock()
	scoped.workspaces["idle-gate"] = &workspaceClient{
		key: "idle-gate", client: client, generation: 1,
		state: workspaceStateIdleCountdown, idleSince: time.Now(), lastActivity: time.Now(),
	}
	scoped.mu.Unlock()
	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-idle-gate", ThreadID: "thread-1", Drain: true,
	})
	if err != nil {
		t.Fatalf("ReleaseScope() error = %v", err)
	}
	if result.ClosedManagers != 0 || result.Drained || managerIsClosed(scoped) {
		t.Fatalf("ReleaseScope() = %#v, manager closed before full idle window", result)
	}
}

func TestRecyclerRechecksLeaseAcquireBeforeDetach(t *testing.T) {
	now := time.Now()
	client := noopClient{}
	mgr := &manager{
		workspaces: make(map[string]*workspaceClient),
		clock:      func() time.Time { return now },
	}
	mgr.workspaces["race"] = &workspaceClient{
		key: "race", client: client, generation: 1, state: workspaceStateIdleCountdown,
		idleSince: now.Add(-2 * idleTimeoutForTest()), lastActivity: now.Add(-2 * idleTimeoutForTest()),
	}
	r := &poolRecycler{now: func() time.Time { return now }}
	candidate, ok := r.managerIdleCandidate(mgr, *mgr.workspaces["race"], now, idleTimeoutForTest())
	if !ok {
		t.Fatal("managerIdleCandidate() rejected an eligible workspace")
	}
	if _, bound, err := mgr.leaseBoundClient(client); err != nil || !bound {
		t.Fatalf("leaseBoundClient after scanner snapshot = bound=%v err=%v", bound, err)
	}
	if detached := detachWorkspaceClientGeneration(mgr, candidate.key, candidate.client, candidate.generation); detached != nil {
		t.Fatal("detach succeeded after a lease acquired during scanner race")
	}
}

func TestStaleGenerationReleaseCannotMutateReplacement(t *testing.T) {
	mgr := &manager{workspaces: make(map[string]*workspaceClient)}
	oldClient := noopClient{}
	newClient := noopClient{}
	mgr.workspaces["workspace"] = &workspaceClient{
		key: "workspace", client: oldClient, generation: 1, state: workspaceStateActive,
	}
	leased, bound, err := mgr.leaseBoundClient(oldClient)
	if err != nil || !bound {
		t.Fatalf("old lease = bound=%v err=%v", bound, err)
	}
	mgr.mu.Lock()
	mgr.workspaces["workspace"] = &workspaceClient{
		key: "workspace", client: newClient, generation: 2, state: workspaceStateActive,
	}
	mgr.mu.Unlock()
	if err := leased.Release(); err == nil {
		t.Fatal("stale release returned nil error")
	}
	replacement := mgr.workspaces["workspace"]
	if replacement.activeLeases != 0 || replacement.state != workspaceStateActive {
		t.Fatalf("replacement mutated by stale release: %+v", replacement)
	}
}

func TestBootstrappingNeverIdleEligible(t *testing.T) {
	now := time.Now()
	workspace := &workspaceClient{
		client:     noopClient{},
		generation: 1,
		state:      workspaceStateBootstrapping,
		idleSince:  now.Add(-2 * idleTimeoutForTest()),
	}
	if idleEligible(workspace, now, idleTimeoutForTest()) {
		t.Fatal("bootstrapping workspace became idle eligible")
	}
}

func TestUnhealthyOwnedWorkspaceStillReachesIdleEligible(t *testing.T) {
	now := time.Now()
	workspace := &workspaceClient{
		client:     &p2LifecycleClient{healthy: false},
		generation: 1,
		state:      workspaceStateIdleCountdown,
		idleSince:  now.Add(-2 * idleTimeoutForTest()),
	}
	if !idleEligible(workspace, now, idleTimeoutForTest()) {
		t.Fatal("unhealthy owned workspace was incorrectly blocked from idle eligibility")
	}
}

func TestEnsureClientPublishesOnlyAfterBootstrapAndSerializesConcurrentEnsure(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"ready-barrier"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	client := &barrierBootstrapClient{
		openStarted: make(chan struct{}),
		openRelease: make(chan struct{}),
	}
	mgr := NewManager(Config{
		WorkspaceRoot:    root,
		ClientFactory:    barrierBootstrapFactory{client: client},
		LanguageAdapters: NewDefaultLanguageAdapterRegistry(),
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	group := newTestGoroutineGroup(t)
	firstDone := startEnsureClientForLifecycleTest(group, mgr, ctx)
	waitForBootstrapStart(t, client)
	secondDone := startEnsureClientForLifecycleTest(group, mgr, ctx)
	assertEnsureClientBlocked(t, secondDone)
	close(client.openRelease)
	first := awaitEnsureClientForLifecycleTest(t, firstDone, "first")
	second := awaitEnsureClientForLifecycleTest(t, secondDone, "second")
	assertEnsureClientSuccess(t, first, second, client)
	assertPublishedWorkspace(t, mgr, client)
}

func TestBootstrapFailureClosesOwnerAndRetainsCleanupPendingOnCloseFailure(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"bootstrap-owner"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	didOpenErr := errors.New("bootstrap did open failed")
	closeErr := errors.New("bootstrap close failed")
	client := &bootstrapFailureOwnerClient{didOpenErr: didOpenErr, closeErr: closeErr}
	mgr := NewManager(Config{
		WorkspaceRoot:    root,
		ClientFactory:    ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) { return client, nil }),
		LanguageAdapters: NewDefaultLanguageAdapterRegistry(),
	}).(*manager)

	_, err := mgr.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), "", "javascript")
	assertBootstrapFailureError(t, err, didOpenErr, closeErr)
	assertBootstrapCleanupOwner(t, mgr, client)
	if err := mgr.Close(); err != nil {
		t.Fatalf("manager Close retry: %v", err)
	}
	assertCloseCallCount(t, client, 2, "after manager retry")
}

func TestManagerCloseWaitsForOutOfEnsureBootstrapAttempt(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"close-bootstrap"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const closeBootstrap = true\n")
	client := &barrierBootstrapClient{
		openStarted: make(chan struct{}), openRelease: make(chan struct{}), closeCalled: make(chan struct{}),
	}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: barrierBootstrapFactory{client: client}}).(*manager)
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	group := newTestGoroutineGroup(t)
	ensureDone := startEnsureClientForLifecycleTest(group, mgr, ctx)
	waitForBootstrapStart(t, client)
	closeDone := make(chan error, 1)
	group.Go(func() { closeDone <- mgr.Close() })
	select {
	case <-client.closeCalled:
		close(client.openRelease)
		<-ensureDone
		<-closeDone
		t.Fatal("manager Close reached client Close before the bootstrap DidOpen completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(client.openRelease)
	if result := <-ensureDone; !errors.Is(result.err, ErrManagerClosed) {
		t.Fatalf("EnsureClient racing Close = %v, want ErrManagerClosed", result.err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("manager Close after bootstrap release: %v", err)
	}
}

func TestDocumentMutationFailsFastInsteadOfWaitingForBootstrapWithURIGate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, *manager, string, string) error
	}{
		{name: "did_open", mutate: func(ctx context.Context, mgr *manager, uri, text string) error {
			return mgr.DidOpen(ctx, uri, "javascript", 1, text)
		}},
		{name: "did_change", mutate: func(ctx context.Context, mgr *manager, uri, text string) error {
			return mgr.DidChange(ctx, uri, 1, []protocol.TextDocumentContentChangeEvent{{Text: text}})
		}},
		{name: "did_close", mutate: func(ctx context.Context, mgr *manager, uri, _ string) error {
			return mgr.DidClose(ctx, uri)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runBootstrapDocumentMutationGateCase(t, test.mutate)
		})
	}
}

func runBootstrapDocumentMutationGateCase(
	t *testing.T,
	mutate func(context.Context, *manager, string, string) error,
) {
	t.Helper()
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function BootstrapGateCycle() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	cfg, err := mgr.resolveLanguageWorkspace(ctx, "javascript")
	if err != nil {
		t.Fatalf("resolve language workspace: %v", err)
	}
	candidate, err := mgr.prepareLanguageClientBootstrap(ctx, cfg)
	if err != nil || !candidate.owner {
		t.Fatalf("prepare bootstrap candidate = owner=%v err=%v, want owner", candidate.owner, err)
	}

	mutationCtx, cancelMutation := context.WithCancel(ctx)
	defer cancelMutation()
	group := newTestGoroutineGroup(t)
	mutationDone := make(chan error, 1)
	mgr.ensureMu.Lock()
	group.Go(func() { mutationDone <- mutate(mutationCtx, mgr, fileURIFromPath(target), text) })
	mutationErr, mutationReturned := observeDocumentMutationBeforeBootstrap(t, mgr, fileURIFromPath(target), mutationDone)
	bootstrapDone := make(chan error, 1)
	group.Go(func() {
		_, completeErr := mgr.completeLanguageClientBootstrap(ctx, cfg, candidate)
		bootstrapDone <- completeErr
	})
	mgr.ensureMu.Unlock()

	mutationErr = awaitDocumentMutationFailFast(t, mutationDone, bootstrapDone, cancelMutation, mutationErr, mutationReturned)
	if !errors.Is(mutationErr, ErrClientNotReady) {
		t.Fatalf("document mutation during language bootstrap = %v, want ErrClientNotReady", mutationErr)
	}
	if err := <-bootstrapDone; err != nil {
		t.Fatalf("complete language bootstrap after mutation fail-fast: %v", err)
	}
	if !factory.clientContainingURI(t, fileURIFromPath(target)).hasDocument(fileURIFromPath(target)) {
		t.Fatal("failed mutation suppressed the owner bootstrap DidOpen")
	}
}

func observeDocumentMutationBeforeBootstrap(
	t *testing.T,
	mgr *manager,
	uri string,
	done <-chan error,
) (error, bool) {
	t.Helper()
	select {
	case err := <-done:
		return err, true
	case <-time.After(10 * time.Millisecond):
		waitForDocumentOperationGate(t, mgr, uri)
		return nil, false
	}
}

func awaitDocumentMutationFailFast(
	t *testing.T,
	done <-chan error,
	bootstrapDone <-chan error,
	cancel context.CancelFunc,
	observed error,
	returned bool,
) error {
	t.Helper()
	if returned {
		return observed
	}
	select {
	case err := <-done:
		return err
	case <-time.After(100 * time.Millisecond):
		cancel()
		<-done
		<-bootstrapDone
		t.Fatal("document mutation waited for bootstrap attempt while holding the bootstrap target URI gate")
		return nil
	}
}

func waitForDocumentOperationGate(t *testing.T, mgr *manager, uri string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mgr.documentOperationMu.Lock()
		gate := mgr.documentOperations[uri]
		mgr.documentOperationMu.Unlock()
		if gate != nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("document operation gate for %s was not acquired", uri)
}

func TestDocumentOperationGateCancellationSkipsTicketWithoutBlockingNext(t *testing.T) {
	mgr := &manager{}
	uri := "file:///workspace/cancel.go"
	held, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationObserveSync)
	if err != nil {
		t.Fatalf("acquire held token: %v", err)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelWait()
	if _, err := mgr.beginDocumentOperation(waitCtx, uri, documentOperationDidChange); !errors.Is(err, context.DeadlineExceeded) {
		held.release()
		t.Fatalf("canceled queued operation = %v, want context deadline", err)
	}
	held.release()
	nextCtx, cancelNext := context.WithTimeout(context.Background(), time.Second)
	defer cancelNext()
	next, err := mgr.beginDocumentOperation(nextCtx, uri, documentOperationDidChange)
	if err != nil {
		t.Fatalf("acquire after canceled ticket: %v", err)
	}
	next.release()
}

func TestCanceledQueuedDidCloseDoesNotSuppressServingBootstrap(t *testing.T) {
	mgr := &manager{}
	uri := "file:///workspace/bootstrap.js"
	bootstrap, err := mgr.beginDocumentOperation(context.Background(), uri, documentOperationObserveBootstrap)
	if err != nil {
		t.Fatalf("acquire bootstrap token: %v", err)
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelClose()
	if _, err := mgr.beginDocumentOperation(closeCtx, uri, documentOperationDidClose); !errors.Is(err, context.DeadlineExceeded) {
		bootstrap.release()
		t.Fatalf("canceled queued DidClose = %v, want context deadline", err)
	}
	if !bootstrap.bootstrapSendAllowed() {
		bootstrap.release()
		t.Fatal("canceled queued DidClose suppressed the serving bootstrap DidOpen")
	}
	bootstrap.release()
}

func TestDidCloseRemovesManagedStateWhenContextCancelsAfterWire(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function CancelAfterClose() {}\n")
	base := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true, workspaceSymbolSupported: true,
	}
	client := &cancelAfterCloseClient{strictWorkspaceSymbolClient: base}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
		return client, nil
	})}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen before canceled close: %v", err)
	}
	closeCtx, cancelClose := context.WithCancel(ctx)
	client.afterClose = cancelClose
	if err := mgr.DidClose(closeCtx, uri); err != nil {
		t.Fatalf("DidClose after successful wire cancellation: %v", err)
	}
	if mgr.isExplicitDocumentOpen(uri) {
		t.Fatal("DidClose left managed explicit state after successful wire close")
	}
}

func TestDidCloseDoesNotNotifyReplacementThatNeverOpenedDocument(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function ReplacementClose() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen before replacement: %v", err)
	}
	_, cfg, err := mgr.bootstrapTarget(ctx, uri)
	if err != nil {
		t.Fatalf("resolve replacement config: %v", err)
	}
	replacement := &strictWorkspaceSymbolClient{
		documents: make(map[string]strictWorkspaceSymbolDocument), healthy: true, workspaceSymbolSupported: true,
	}
	mgr.mu.Lock()
	workspace := mgr.workspaces[cfg.key]
	workspace.client = replacement
	workspace.generation++
	workspace.state = workspaceStateActive
	mgr.mu.Unlock()
	if err := mgr.DidClose(ctx, uri); err != nil {
		t.Fatalf("DidClose after replacement: %v", err)
	}
	if replacement.notificationCount() != 0 || mgr.isExplicitDocumentOpen(uri) {
		t.Fatal("DidClose notified unopened replacement or retained stale managed state")
	}
}

func TestWorkspaceSymbolCapabilityErrorDoesNotHidePublishCleanupFailure(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function UnsupportedFatal() {}\n")
	closeErr := errors.New("capability abort close failed")
	client := &capabilityPublishFailureClient{closeErr: closeErr}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
		return client, nil
	})}).(*manager)
	client.onCapabilities = func() {
		mgr.mu.Lock()
		mgr.closed = true
		mgr.retiring = true
		mgr.mu.Unlock()
	}
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", false)
	_, err := mgr.WorkspaceSymbol(ctx, "UnsupportedFatal", "javascript")
	if !errors.Is(err, ErrManagerClosed) || !errors.Is(err, closeErr) {
		t.Fatalf("WorkspaceSymbol capability/publish error = %v, want manager closed and cleanup error", err)
	}
	if errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("fatal publish cleanup error remained classifiable as unsupported capability: %v", err)
	}
}

func TestDiskBootstrapPathsRefuseDirtyManagedDocument(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *manager, string) error
	}{
		{name: "bootstrap", run: func(ctx context.Context, mgr *manager, uri string) error {
			return mgr.BootstrapDocument(ctx, uri)
		}},
		{name: "diagnostic_reopen", run: func(ctx context.Context, mgr *manager, uri string) error {
			return mgr.ReopenDocumentForDiagnostics(ctx, uri)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, target, text := writeWorkspaceSymbolSyncFixture(t, "app.js", "function DiskText() {}\n")
			factory := &strictWorkspaceSymbolFactory{}
			mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
			t.Cleanup(func() { closeBootstrapTestManager(t, mgr) })
			ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
			uri := fileURIFromPath(target)
			if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
				t.Fatalf("DidOpen before dirty bootstrap: %v", err)
			}
			dirty := "function DirtyBuffer() {}\n"
			if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{{Text: dirty}}); err != nil {
				t.Fatalf("DidChange dirty buffer: %v", err)
			}
			if err := test.run(ctx, mgr, uri); err == nil || !strings.Contains(err.Error(), "dirty managed document") {
				t.Fatalf("disk bootstrap for dirty buffer = %v, want fail-fast", err)
			}
			if got := factory.clientContainingURI(t, uri).notificationCount(); got != 2 {
				t.Fatalf("dirty buffer notifications after disk bootstrap = %d, want open+change only", got)
			}
		})
	}
}

func TestResolvedUnsupportedCapabilityDoesNotHideConcurrentManagerClose(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "app.js", "function CloseDuringCapabilities() {}\n")
	client := &blockingUnsupportedCapabilityClient{entered: make(chan struct{}), release: make(chan struct{})}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
		return client, nil
	})}).(*manager)
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	group := newTestGoroutineGroup(t)
	result := make(chan error, 1)
	group.Go(func() {
		_, err := mgr.WorkspaceSymbol(ctx, "CloseDuringCapabilities", "javascript")
		result <- err
	})
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("WorkspaceSymbol did not reach capability check")
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("manager Close during capability check: %v", err)
	}
	close(client.release)
	err := <-result
	if !errors.Is(err, ErrManagerClosed) || errors.Is(err, lspmanager.ErrUnsupportedCapability) {
		t.Fatalf("WorkspaceSymbol after concurrent Close = %v, want fatal ErrManagerClosed only", err)
	}
}

type lifecycleEnsureResult struct {
	client Client
	err    error
}

func startEnsureClientForLifecycleTest(group *testGoroutineGroup, mgr *manager, ctx context.Context) <-chan lifecycleEnsureResult {
	done := make(chan lifecycleEnsureResult, 1)
	group.Go(func() {
		client, err := mgr.EnsureClient(ctx, "", "javascript")
		done <- lifecycleEnsureResult{client: client, err: err}
	})
	return done
}

func waitForBootstrapStart(t *testing.T, client *barrierBootstrapClient) {
	t.Helper()
	select {
	case <-client.openStarted:
	case <-time.After(time.Second):
		t.Fatal("first EnsureClient did not reach bootstrap DidOpen")
	}
}

func assertEnsureClientBlocked(t *testing.T, done <-chan lifecycleEnsureResult) {
	t.Helper()
	select {
	case result := <-done:
		t.Fatalf("second EnsureClient returned before first publish: client=%T err=%v", result.client, result.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func awaitEnsureClientForLifecycleTest(t *testing.T, done <-chan lifecycleEnsureResult, label string) lifecycleEnsureResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatalf("%s EnsureClient did not finish after bootstrap release", label)
		return lifecycleEnsureResult{}
	}
}

func assertEnsureClientSuccess(t *testing.T, first, second lifecycleEnsureResult, want Client) {
	t.Helper()
	if first.err != nil || second.err != nil {
		t.Fatalf("EnsureClient results = first(%T, %v), second(%T, %v), want both successful", first.client, first.err, second.client, second.err)
	}
	if first.client != want || second.client != want {
		t.Fatalf("EnsureClient clients = first(%p), second(%p), want published client %p", first.client, second.client, want)
	}
}

func assertPublishedWorkspace(t *testing.T, mgr *manager, client Client) {
	t.Helper()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			if workspace.state == workspaceStateBootstrapping {
				t.Fatalf("published workspace = %#v, want ready state", workspace)
			}
			return
		}
	}
	t.Fatalf("published workspace for %T was not found", client)
}

func assertBootstrapFailureError(t *testing.T, got, didOpenErr, closeErr error) {
	t.Helper()
	if !errors.Is(got, didOpenErr) || !errors.Is(got, closeErr) {
		t.Fatalf("EnsureClient() error = %v, want DidOpen and Close errors", got)
	}
}

func assertBootstrapCleanupOwner(t *testing.T, mgr *manager, client *bootstrapFailureOwnerClient) {
	t.Helper()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			if workspace.generation == 0 || workspace.state != workspaceStateCleanupPending || workspace.activeLeases != 0 {
				t.Fatalf("bootstrap cleanup owner = %#v, want generation-owned CleanupPending with no leases", workspace)
			}
			if got := client.closeCallCount(); got != 1 {
				t.Fatalf("bootstrap owner Close calls = %d, want one deterministic cleanup attempt", got)
			}
			return
		}
	}
	t.Fatalf("bootstrap cleanup owner for %T was not found", client)
}

func assertCloseCallCount(t *testing.T, client *bootstrapFailureOwnerClient, want int, suffix string) {
	t.Helper()
	if got := client.closeCallCount(); got != want {
		t.Fatalf("bootstrap owner Close calls %s = %d, want %d", suffix, got, want)
	}
}

type barrierBootstrapFactory struct {
	client Client
}

func (f barrierBootstrapFactory) NewClient(string, protocol.NotificationHandler) (Client, error) {
	return f.client, nil
}

type barrierBootstrapClient struct {
	noopClient
	openStarted chan struct{}
	openRelease chan struct{}
	closeCalled chan struct{}
	once        sync.Once
	closeOnce   sync.Once
}

func (c *barrierBootstrapClient) DidOpen(context.Context, string, string, int, string) error {
	c.once.Do(func() { close(c.openStarted) })
	<-c.openRelease
	return nil
}

func (c *barrierBootstrapClient) Close() error {
	if c.closeCalled != nil {
		c.closeOnce.Do(func() { close(c.closeCalled) })
	}
	return nil
}

type bootstrapFailureOwnerClient struct {
	noopClient
	mu         sync.Mutex
	didOpenErr error
	closeErr   error
	closeCalls int
}

type cancelAfterCloseClient struct {
	*strictWorkspaceSymbolClient
	afterClose context.CancelFunc
}

type capabilityPublishFailureClient struct {
	noopClient
	onCapabilities func()
	closeErr       error
	closeCalls     int
	once           sync.Once
}

type blockingUnsupportedCapabilityClient struct {
	noopClient
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingUnsupportedCapabilityClient) ServerCapabilities() protocol.ServerCapabilities {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return protocol.ServerCapabilities{WorkspaceSymbolProvider: false}
}

func (c *capabilityPublishFailureClient) ServerCapabilities() protocol.ServerCapabilities {
	c.once.Do(c.onCapabilities)
	return protocol.ServerCapabilities{WorkspaceSymbolProvider: false}
}

func (c *capabilityPublishFailureClient) Close() error {
	c.closeCalls++
	if c.closeCalls == 1 {
		return c.closeErr
	}
	return nil
}

func (c *cancelAfterCloseClient) DidClose(ctx context.Context, uri string) error {
	if err := c.strictWorkspaceSymbolClient.DidClose(ctx, uri); err != nil {
		return err
	}
	if c.afterClose != nil {
		c.afterClose()
	}
	return nil
}

func (c *bootstrapFailureOwnerClient) DidOpen(context.Context, string, string, int, string) error {
	return c.didOpenErr
}

func (c *bootstrapFailureOwnerClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCalls++
	if c.closeCalls == 1 {
		return c.closeErr
	}
	return nil
}

func (c *bootstrapFailureOwnerClient) closeCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

var _ Client = (*barrierBootstrapClient)(nil)
var _ Client = (*bootstrapFailureOwnerClient)(nil)
