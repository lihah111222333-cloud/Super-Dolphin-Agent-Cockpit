package multilsp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

func TestCreateAndRegisterClientShutsDownDiscardedClientOutsideLockWithTimeout(t *testing.T) {
	t.Parallel()

	existing := noopClient{}
	probe := make(chan bool, 1)
	deadline := make(chan bool, 1)
	m := &manager{
		instanceID: "test-lifecycle-shutdown-manager",
		workspaces: map[string]*workspaceClient{
			"repo:go": {key: "repo:go", client: existing, generation: 1, state: workspaceStateActive},
		},
	}
	discarded := &lockProbeShutdownClient{
		manager:       m,
		workspaceKey:  "repo:go",
		lockAvailable: probe,
		hasDeadline:   deadline,
	}
	m.factory = ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
		return discarded, nil
	})

	got, err := m.createAndRegisterClient(context.Background(), workspaceConfig{
		key:        "repo:go",
		rootPath:   t.TempDir(),
		rootURI:    "file:///repo",
		languageID: "go",
	})
	if err != nil {
		t.Fatalf("createAndRegisterClient() error = %v", err)
	}
	if got != existing {
		t.Fatalf("createAndRegisterClient() returned %#v, want existing client", got)
	}
	if !<-probe {
		t.Fatal("discarded client Shutdown ran while manager lock was still held")
	}
	if !<-deadline {
		t.Fatal("discarded client Shutdown did not receive manager shutdown deadline")
	}
	if discarded.closed != 1 {
		t.Fatalf("discarded Close calls = %d, want 1", discarded.closed)
	}
}

func TestCollectAndClearClientShutdownsDeduplicatesSharedTransportOwner(t *testing.T) {
	client := &lockProbeShutdownClient{}
	m := &manager{instanceID: "shared-owner-test", workspaces: map[string]*workspaceClient{
		"repo:go":    {key: "repo:go", client: client, generation: 1, state: workspaceStateActive},
		"repo:gosum": {key: "repo:gosum", client: client, generation: 1, state: workspaceStateActive},
	}}

	states, err := m.collectAndClearClientShutdowns()
	if err != nil {
		t.Fatalf("collectAndClearClientShutdowns() error = %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("shutdown owner count = %d, want 1", len(states))
	}
}

type lockProbeShutdownClient struct {
	noopClient
	manager       *manager
	workspaceKey  string
	lockAvailable chan<- bool
	hasDeadline   chan<- bool
	closed        int
}

func TestManagerCloseWaitsForDetachedBootstrapAbortCleanup(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"abort-barrier"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	didOpenErr := errors.New("bootstrap didOpen failed")
	closeErr := errors.New("bootstrap abort close failed")
	client := &detachedBootstrapAbortClient{
		didOpenErr: didOpenErr,
		closeErr:   closeErr,
		closeStart: make(chan struct{}),
		closeAllow: make(chan struct{}),
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	ctx := ctxWithCWD(root, "agent-abort-barrier", "thread-abort-barrier")
	ensureDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.detachedBootstrapAbort.ensure", func(context.Context) {
		_, err := mgr.EnsureClient(ctx, "", "javascript")
		ensureDone <- err
	})
	waitForDetachedBootstrapClose(t, client.closeStart, ensureDone)
	closeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.detachedBootstrapAbort.close", func(context.Context) {
		closeDone <- mgr.Close()
	})
	assertManagerCloseBlocked(t, closeDone)
	close(client.closeAllow)
	assertDetachedBootstrapAbortResults(t, <-ensureDone, <-closeDone, didOpenErr, closeErr)
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if len(mgr.workspaces) != 0 || len(mgr.bootstrapAttempts) != 0 {
		t.Fatalf("closed manager retained workspace=%d attempts=%d", len(mgr.workspaces), len(mgr.bootstrapAttempts))
	}
}

func TestManagerCloseCancelsLanguageBootstrapBeforeWaitingForClientClose(t *testing.T) {
	root := t.TempDir()
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"close-cancels-bootstrap"}`)
	writeGenericTestFile(t, filepath.Join(root, "app.js"), "const value = 1\n")
	client := &closeCanceledBootstrapClient{
		openStarted: make(chan struct{}), contextCanceled: make(chan struct{}), closeCalled: make(chan struct{}),
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	ctx := ctxWithCWD(root, "agent-close-cancel", "thread-close-cancel")
	ensureDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.closeCancelsBootstrap.ensure", func(context.Context) {
		_, err := mgr.EnsureClient(ctx, "", "javascript")
		ensureDone <- err
	})
	waitForProvisionalSignal(t, client.openStarted, "language bootstrap DidOpen")
	closeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.closeCancelsBootstrap.close", func(context.Context) {
		closeDone <- mgr.Close()
	})
	waitForProvisionalSignal(t, client.contextCanceled, "bootstrap lifecycle cancellation")
	if err := <-ensureDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureClient error = %v, want context canceled", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Manager.Close: %v", err)
	}
	waitForProvisionalSignal(t, client.closeCalled, "bootstrap client Close")
}

func TestIncrementalDidChangeRejectsVersionConsumedByWorkspaceSync(t *testing.T) {
	root, target, initial := writeWorkspaceSymbolSyncFixture(t, "incremental.js", "function InitialSymbol() {}\n")
	factory := &strictWorkspaceSymbolFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot: root, ClientFactory: factory, DisableInitialWorkspaceBootstrap: true,
	}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	if err := mgr.DidOpen(ctx, target, "javascript", 1, initial); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	writeGenericTestFile(t, target, "function FreshSymbol() {}\n")
	if _, err := mgr.WorkspaceSymbol(ctx, "FreshSymbol", "javascript"); err != nil {
		t.Fatalf("WorkspaceSymbol: %v", err)
	}
	client := factory.clientContainingURI(t, fileURIFromPath(target))
	before := client.notificationCount()
	rng := protocol.Range{End: protocol.Position{Character: 1}}
	err := mgr.DidChange(ctx, target, 2, []protocol.TextDocumentContentChangeEvent{{Range: &rng, Text: "x"}})
	if err == nil || !strings.Contains(err.Error(), "incremental change version 2") {
		t.Fatalf("stale incremental DidChange error = %v", err)
	}
	if got := client.notificationCount(); got != before {
		t.Fatalf("stale incremental DidChange notifications = %d, want unchanged %d", got, before)
	}
}

func TestColdFileWorkspaceSymbolPublishesOnlyAfterTargetBootstrap(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "target.js", "function TargetSymbol() {}\n")
	openErr := errors.New("target bootstrap failed")
	client := &targetBootstrapFailureClient{
		openStarted: make(chan struct{}), openRelease: make(chan struct{}), openErr: openErr,
	}
	mgr := NewManager(Config{
		WorkspaceRoot: root,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	workspaceDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.targetBootstrap.workspaceSymbol", func(context.Context) {
		_, err := mgr.WorkspaceSymbol(ctx, "TargetSymbol", "javascript")
		workspaceDone <- err
	})
	select {
	case <-client.openStarted:
	case <-time.After(time.Second):
		t.Fatal("workspace symbol did not reach exact target bootstrap")
	}
	ensureDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.targetBootstrap.ensure", func(context.Context) {
		_, err := mgr.EnsureClient(ctx, target, "javascript")
		ensureDone <- err
	})
	assertErrorResultBlocked(t, ensureDone, "EnsureClient returned before exact target bootstrap")
	close(client.openRelease)
	assertTargetBootstrapFailure(t, <-workspaceDone, <-ensureDone, openErr, client)
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	if len(mgr.workspaces) != 0 || len(mgr.bootstrapAttempts) != 0 {
		t.Fatalf("failed target bootstrap retained workspace=%d attempts=%d", len(mgr.workspaces), len(mgr.bootstrapAttempts))
	}
}

func TestDidCloseBeforeFileWorkspaceSymbolBootstrapPreventsReopen(t *testing.T) {
	root, target, _ := writeWorkspaceSymbolSyncFixture(t, "closed-before-bootstrap.js", "function ClosedSymbol() {}\n")
	client := &blockingSupportedCapabilityClient{entered: make(chan struct{}), release: make(chan struct{})}
	mgr := NewManager(Config{
		WorkspaceRoot: root, DisableInitialWorkspaceBootstrap: true,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	if _, err := mgr.EnsureClient(ctx, target, "javascript"); err != nil {
		t.Fatalf("prepare active client: %v", err)
	}
	workspaceDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.closeBeforeTargetBootstrap.workspaceSymbol", func(context.Context) {
		_, err := mgr.WorkspaceSymbol(ctx, "ClosedSymbol", "javascript")
		workspaceDone <- err
	})
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("workspace symbol did not reach capability barrier")
	}
	if err := mgr.DidClose(ctx, target); err != nil {
		t.Fatalf("DidClose before target bootstrap: %v", err)
	}
	close(client.release)
	if err := <-workspaceDone; err != nil {
		t.Fatalf("WorkspaceSymbol after prior DidClose: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.didOpenCalls != 0 {
		t.Fatalf("target resurrected after DidClose: DidOpen calls=%d", client.didOpenCalls)
	}
}

func TestManagedDidCloseDuringFileWorkspaceSymbolCapabilityCheckPreventsReopen(t *testing.T) {
	root, target, text := writeWorkspaceSymbolSyncFixture(t, "managed-close-before-bootstrap.js", "function ManagedClosedSymbol() {}\n")
	client := &blockingSupportedCapabilityClient{entered: make(chan struct{}), release: make(chan struct{})}
	mgr := NewManager(Config{
		WorkspaceRoot: root, DisableInitialWorkspaceBootstrap: true,
		ClientFactory: ClientFactoryFunc(func(string, protocol.NotificationHandler) (Client, error) {
			return client, nil
		}),
	}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := workspaceSymbolSyncContext(t, root, target, "javascript", true)
	uri := fileURIFromPath(target)
	if err := mgr.DidOpen(ctx, uri, "javascript", 1, text); err != nil {
		t.Fatalf("DidOpen managed target: %v", err)
	}

	workspaceDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.managedCloseBeforeTargetBootstrap.workspaceSymbol", func(context.Context) {
		_, err := mgr.WorkspaceSymbol(ctx, "ManagedClosedSymbol", "javascript")
		workspaceDone <- err
	})
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("workspace symbol did not reach capability barrier")
	}
	if err := mgr.DidClose(ctx, uri); err != nil {
		t.Fatalf("DidClose managed target before bootstrap: %v", err)
	}
	close(client.release)
	if err := <-workspaceDone; err != nil {
		t.Fatalf("WorkspaceSymbol after managed DidClose: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.didOpenCalls != 1 {
		t.Fatalf("managed target reopened after DidClose: DidOpen calls=%d, want initial open only", client.didOpenCalls)
	}
}

func assertErrorResultBlocked(t *testing.T, done <-chan error, message string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("%s: %v", message, err)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertTargetBootstrapFailure(t *testing.T, workspaceErr, ensureErr, want error, client *targetBootstrapFailureClient) {
	t.Helper()
	if !errors.Is(workspaceErr, want) || !errors.Is(ensureErr, want) {
		t.Fatalf("target bootstrap errors = workspace(%v) ensure(%v), want %v", workspaceErr, ensureErr, want)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closeCalls != 1 {
		t.Fatalf("target bootstrap abort Close calls = %d, want 1", client.closeCalls)
	}
}

type targetBootstrapFailureClient struct {
	noopClient
	mu          sync.Mutex
	openStarted chan struct{}
	openRelease chan struct{}
	openErr     error
	openOnce    sync.Once
	closeCalls  int
}

type blockingSupportedCapabilityClient struct {
	noopClient
	mu           sync.Mutex
	entered      chan struct{}
	release      chan struct{}
	once         sync.Once
	didOpenCalls int
}

func (c *blockingSupportedCapabilityClient) ServerCapabilities() protocol.ServerCapabilities {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return protocol.ServerCapabilities{WorkspaceSymbolProvider: true}
}

func (c *blockingSupportedCapabilityClient) DidOpen(context.Context, string, string, int, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.didOpenCalls++
	return nil
}

func (c *targetBootstrapFailureClient) ServerCapabilities() protocol.ServerCapabilities {
	return protocol.ServerCapabilities{WorkspaceSymbolProvider: true}
}

func (c *targetBootstrapFailureClient) DidOpen(context.Context, string, string, int, string) error {
	c.openOnce.Do(func() { close(c.openStarted) })
	<-c.openRelease
	return c.openErr
}

func (c *targetBootstrapFailureClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCalls++
	return nil
}

func waitForDetachedBootstrapClose(t *testing.T, closeStart <-chan struct{}, ensureDone <-chan error) {
	t.Helper()
	select {
	case <-closeStart:
	case err := <-ensureDone:
		t.Fatalf("EnsureClient returned before detached Close: %v", err)
	case <-time.After(time.Second):
		t.Fatal("bootstrap abort did not reach detached Close")
	}
}

func assertManagerCloseBlocked(t *testing.T, closeDone <-chan error) {
	t.Helper()
	select {
	case err := <-closeDone:
		t.Fatalf("Manager.Close returned before detached bootstrap cleanup finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertDetachedBootstrapAbortResults(t *testing.T, ensureErr, closeResult, didOpenErr, closeErr error) {
	t.Helper()
	if !errors.Is(ensureErr, didOpenErr) || !errors.Is(ensureErr, closeErr) {
		t.Fatalf("EnsureClient error = %v, want bootstrap and abort Close errors", ensureErr)
	}
	if closeResult != nil {
		t.Fatalf("Manager.Close after cleanup retry: %v", closeResult)
	}
}

type detachedBootstrapAbortClient struct {
	noopClient
	mu         sync.Mutex
	didOpenErr error
	closeErr   error
	closeStart chan struct{}
	closeAllow chan struct{}
	closeCalls int
	closeOnce  sync.Once
}

type closeCanceledBootstrapClient struct {
	noopClient
	openStarted     chan struct{}
	contextCanceled chan struct{}
	closeCalled     chan struct{}
	openOnce        sync.Once
	cancelOnce      sync.Once
	closeOnce       sync.Once
}

func (c *closeCanceledBootstrapClient) DidOpen(ctx context.Context, _ string, _ string, _ int, _ string) error {
	c.openOnce.Do(func() { close(c.openStarted) })
	select {
	case <-ctx.Done():
		c.cancelOnce.Do(func() { close(c.contextCanceled) })
		return ctx.Err()
	case <-c.closeCalled:
		return errors.New("client closed before bootstrap context cancellation")
	}
}

func (c *closeCanceledBootstrapClient) Close() error {
	c.closeOnce.Do(func() { close(c.closeCalled) })
	return nil
}

func (c *detachedBootstrapAbortClient) DidOpen(context.Context, string, string, int, string) error {
	return c.didOpenErr
}

func (c *detachedBootstrapAbortClient) Close() error {
	c.mu.Lock()
	c.closeCalls++
	call := c.closeCalls
	c.mu.Unlock()
	if call != 1 {
		return nil
	}
	c.closeOnce.Do(func() { close(c.closeStart) })
	<-c.closeAllow
	return c.closeErr
}

func (c *lockProbeShutdownClient) Shutdown(ctx context.Context) error {
	lockDone := make(chan bool, 1)
	safego.Go(ctx, nil, "multilsp.lockProbeShutdownClient.lookup", func(context.Context) {
		client, err := c.manager.lookupExistingClient(c.workspaceKey)
		lockDone <- err == nil && client != nil
	})
	select {
	case lockAvailable := <-lockDone:
		c.lockAvailable <- lockAvailable
	case <-time.After(200 * time.Millisecond):
		c.lockAvailable <- false
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= managerShutdownTimeout && time.Until(deadline) > 0 {
		c.hasDeadline <- true
	} else {
		c.hasDeadline <- false
	}
	return nil
}

func (c *lockProbeShutdownClient) Close() error {
	c.closed++
	return nil
}

func TestEnsureClientRetainsFailedInitializeCleanupUntilRetry(t *testing.T) {
	initErr := errors.New("initialize failed")
	closeErr := errors.New("close failed once")
	failed := &provisionalCleanupClient{
		initializeErr: initErr,
		closeErrors:   []error{closeErr, nil},
	}
	replacement := &provisionalCleanupClient{}
	factory := &provisionalCleanupFactory{clients: []Client{failed, replacement}}
	mgr := &manager{
		instanceID: "test-lifecycle-initialize-manager",
		factory:    ClientFactoryFunc(factory.newClient),
		workspaces: make(map[string]*workspaceClient),
	}
	cfg := workspaceConfig{
		key:        "initialize-cleanup:go",
		rootPath:   t.TempDir(),
		rootURI:    "file:///initialize-cleanup",
		languageID: "go",
	}

	got, err := mgr.ensureClient(context.Background(), cfg)
	assertInitializeCleanupFailure(t, got, err, factory, initErr, closeErr)

	got, err = mgr.ensureClient(context.Background(), cfg)
	assertInitializeCleanupRetry(t, got, err, failed, replacement, factory)
	if err := mgr.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func TestInitializeFailureRetainsCleanupOwnerForManagerCloseRetry(t *testing.T) {
	initErr := errors.New("initialize failed before manager close")
	closeErr := errors.New("initialize cleanup close failed once")
	failed := &provisionalCleanupClient{
		initializeErr: initErr,
		closeErrors:   []error{closeErr, nil},
	}
	factory := &provisionalCleanupFactory{clients: []Client{failed}}
	mgr := &manager{
		instanceID: "test-lifecycle-close-manager",
		factory:    ClientFactoryFunc(factory.newClient),
		workspaces: make(map[string]*workspaceClient),
	}
	cfg := workspaceConfig{
		key:        "initialize-manager-close:go",
		rootPath:   t.TempDir(),
		rootURI:    "file:///initialize-manager-close",
		languageID: "go",
	}

	client, err := mgr.ensureClient(context.Background(), cfg)
	assertInitializeCleanupFailure(t, client, err, factory, initErr, closeErr)
	if err := mgr.Close(); err != nil {
		t.Fatalf("manager Close() retry error = %v, want successful retained cleanup", err)
	}
	if got := failed.closeCallCount(); got != 2 {
		t.Fatalf("failed provisional Close calls = %d, want initial failure plus manager retry", got)
	}
	if got := failed.shutdownCallCount(); got != 0 {
		t.Fatalf("failed initialization Shutdown calls = %d, want 0", got)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls before manager cleanup = %d, want 1", got)
	}
}

func assertInitializeCleanupFailure(
	t *testing.T,
	client Client,
	err error,
	factory *provisionalCleanupFactory,
	initErr error,
	closeErr error,
) {
	t.Helper()
	if client != nil {
		t.Fatalf("first ensure client = %T, want nil", client)
	}
	if !errors.Is(err, initErr) || !errors.Is(err, closeErr) {
		t.Fatalf("first ensure error = %v, want initialize and cleanup failures", err)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls after failed cleanup = %d, want 1", got)
	}
}

func assertInitializeCleanupRetry(
	t *testing.T,
	client Client,
	err error,
	failed *provisionalCleanupClient,
	replacement Client,
	factory *provisionalCleanupFactory,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("retry ensure client: %v", err)
	}
	if client != replacement {
		t.Fatalf("retry ensure client = %T, want replacement", client)
	}
	if got := failed.closeCallCount(); got != 2 {
		t.Fatalf("failed provisional Close calls = %d, want failed attempt plus successful retry", got)
	}
	if got := failed.shutdownCallCount(); got != 0 {
		t.Fatalf("failed initialization Shutdown calls = %d, want 0", got)
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls after cleanup retry = %d, want replacement only after cleanup succeeded", got)
	}
}

func TestCreateAndRegisterClientRetainsDiscardedExistingClientCleanup(t *testing.T) {
	closeErr := errors.New("discarded close failed once")
	existing := &provisionalCleanupClient{}
	discarded := &provisionalCleanupClient{closeErrors: []error{closeErr, nil}}
	factory := &provisionalCleanupFactory{clients: []Client{discarded}}
	cfg := workspaceConfig{
		key:        "existing-cleanup:go",
		rootPath:   t.TempDir(),
		rootURI:    "file:///existing-cleanup",
		languageID: "go",
	}
	mgr := &manager{
		instanceID: "test-lifecycle-discard-manager",
		factory:    ClientFactoryFunc(factory.newClient),
		workspaces: map[string]*workspaceClient{
			cfg.key: {key: cfg.key, client: existing, generation: 1, state: workspaceStateActive},
		},
	}

	got, err := mgr.createAndRegisterClient(context.Background(), cfg)
	if got != existing {
		t.Fatalf("createAndRegisterClient() client = %T, want existing", got)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("createAndRegisterClient() error = %v, want discarded Close failure", err)
	}

	got, err = mgr.ensureClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("ensure existing client after cleanup retry: %v", err)
	}
	if got != existing {
		t.Fatalf("ensure existing client = %T, want original existing", got)
	}
	if got := discarded.shutdownCallCount(); got != 1 {
		t.Fatalf("discarded Shutdown calls = %d, want one graceful attempt", got)
	}
	if got := discarded.closeCallCount(); got != 2 {
		t.Fatalf("discarded Close calls = %d, want failed attempt plus successful retry", got)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls = %d, want no second client while discarded cleanup was pending", got)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}

func TestManagerCloseRetriesProvisionalCleanupRacingRegistration(t *testing.T) {
	closeErr := errors.New("registration cleanup close failed once")
	initializeStarted := make(chan struct{})
	initializeRelease := make(chan struct{})
	client := &provisionalCleanupClient{
		initializeStarted: initializeStarted,
		initializeRelease: initializeRelease,
		closeErrors:       []error{closeErr, nil},
	}
	factory := &provisionalCleanupFactory{clients: []Client{client}}
	mgr := &manager{
		instanceID: "test-lifecycle-race-manager",
		factory:    ClientFactoryFunc(factory.newClient),
		workspaces: make(map[string]*workspaceClient),
	}
	cfg := workspaceConfig{
		key:        "registration-close-race:go",
		rootPath:   t.TempDir(),
		rootURI:    "file:///registration-close-race",
		languageID: "go",
	}

	ensureDone := make(chan provisionalEnsureResult, 1)
	safego.Go(context.Background(), nil, "multilsp.provisionalCleanup.ensure", func(context.Context) {
		got, err := mgr.ensureClient(context.Background(), cfg)
		ensureDone <- provisionalEnsureResult{client: got, err: err}
	})
	waitForProvisionalSignal(t, initializeStarted, "client initialize start")

	closeDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "multilsp.provisionalCleanup.close", func(context.Context) {
		closeDone <- mgr.Close()
	})
	waitForManagerClosedState(t, mgr)
	close(initializeRelease)

	ensureResult := waitForProvisionalEnsureResult(t, ensureDone)
	if ensureResult.client != nil {
		t.Fatalf("racing ensure client = %T, want nil", ensureResult.client)
	}
	if !errors.Is(ensureResult.err, ErrManagerClosed) || !errors.Is(ensureResult.err, closeErr) {
		t.Fatalf("racing ensure error = %v, want manager closed and cleanup failure", ensureResult.err)
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("manager Close() error = %v, want retained cleanup retry to succeed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager Close() did not finish after initialize released")
	}
	if got := client.shutdownCallCount(); got != 1 {
		t.Fatalf("racing provisional Shutdown calls = %d, want 1", got)
	}
	if got := client.closeCallCount(); got != 2 {
		t.Fatalf("racing provisional Close calls = %d, want failed registration cleanup plus manager retry", got)
	}
	if got := factory.callCount(); got != 1 {
		t.Fatalf("factory calls during close race = %d, want 1", got)
	}
}

type provisionalEnsureResult struct {
	client Client
	err    error
}

type provisionalCleanupFactory struct {
	mu      sync.Mutex
	clients []Client
	calls   int
}

func (f *provisionalCleanupFactory) newClient(string, protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.clients) {
		return nil, errors.New("provisional cleanup factory exhausted")
	}
	client := f.clients[f.calls]
	f.calls++
	return client, nil
}

func (f *provisionalCleanupFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type provisionalCleanupClient struct {
	noopClient

	mu                sync.Mutex
	initializeOnce    sync.Once
	initializeErr     error
	initializeStarted chan struct{}
	initializeRelease <-chan struct{}
	shutdownCalls     int
	closeCalls        int
	closeErrors       []error
}

func (c *provisionalCleanupClient) Initialize(ctx context.Context, _ string) error {
	if c.initializeStarted != nil {
		c.initializeOnce.Do(func() { close(c.initializeStarted) })
	}
	if c.initializeRelease != nil {
		select {
		case <-c.initializeRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.initializeErr
}

func (c *provisionalCleanupClient) Shutdown(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.shutdownCalls++
	return nil
}

func (c *provisionalCleanupClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := c.closeCalls
	c.closeCalls++
	if index < len(c.closeErrors) {
		return c.closeErrors[index]
	}
	return nil
}

func (c *provisionalCleanupClient) shutdownCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shutdownCalls
}

func (c *provisionalCleanupClient) closeCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

func waitForProvisionalSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func waitForManagerClosedState(t *testing.T, mgr *manager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.RLock()
		closed := mgr.closed
		mgr.mu.RUnlock()
		if closed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("manager did not enter closed state")
}

func waitForProvisionalEnsureResult(t *testing.T, result <-chan provisionalEnsureResult) provisionalEnsureResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ensure result")
		return provisionalEnsureResult{}
	}
}
