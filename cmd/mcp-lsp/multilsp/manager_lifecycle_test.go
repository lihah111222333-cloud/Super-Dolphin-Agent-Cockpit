package multilsp

import (
	"context"
	"errors"
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
		workspaces: map[string]*workspaceClient{
			"repo:go": {key: "repo:go", client: existing},
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

type lockProbeShutdownClient struct {
	noopClient
	manager       *manager
	workspaceKey  string
	lockAvailable chan<- bool
	hasDeadline   chan<- bool
	closed        int
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
		factory: ClientFactoryFunc(factory.newClient),
		workspaces: map[string]*workspaceClient{
			cfg.key: {key: cfg.key, client: existing},
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
