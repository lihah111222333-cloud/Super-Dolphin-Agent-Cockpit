package multilsp

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
			workspace.idleSince = time.Now().Add(-2 * idleTimeout)
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
	if !got.After(releasedAt) {
		t.Fatalf("lastActivity after release = %s, want after %s", got, releasedAt)
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
		idleSince: now.Add(-2 * idleTimeout), lastActivity: now.Add(-2 * idleTimeout),
	}
	r := &poolRecycler{now: func() time.Time { return now }}
	candidate, ok := r.managerIdleCandidate(mgr, *mgr.workspaces["race"], now, idleTimeout)
	if !ok {
		t.Fatal("managerIdleCandidate() rejected an eligible workspace")
	}
	if _, bound, err := mgr.leaseBoundClient(client); err != nil || !bound {
		t.Fatalf("leaseBoundClient after scanner snapshot = bound=%v err=%v", bound, err)
	}
	if detached := detachWorkspaceClientGeneration(mgr, candidate.key, candidate.client, candidate.generation, candidate.idleSince); detached != nil {
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
		idleSince:  now.Add(-2 * idleTimeout),
	}
	if idleEligible(workspace, now, idleTimeout) {
		t.Fatal("bootstrapping workspace became idle eligible")
	}
}

func TestUnhealthyOwnedWorkspaceStillReachesIdleEligible(t *testing.T) {
	now := time.Now()
	workspace := &workspaceClient{
		client:     &p2LifecycleClient{healthy: false},
		generation: 1,
		state:      workspaceStateIdleCountdown,
		idleSince:  now.Add(-2 * idleTimeout),
	}
	if !idleEligible(workspace, now, idleTimeout) {
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

	firstDone := make(chan struct {
		client Client
		err    error
	}, 1)
	go func() {
		got, err := mgr.EnsureClient(ctx, "", "javascript")
		firstDone <- struct {
			client Client
			err    error
		}{got, err}
	}()
	select {
	case <-client.openStarted:
	case <-time.After(time.Second):
		t.Fatal("first EnsureClient did not reach bootstrap DidOpen")
	}

	secondDone := make(chan struct {
		client Client
		err    error
	}, 1)
	go func() {
		got, err := mgr.EnsureClient(ctx, "", "javascript")
		secondDone <- struct {
			client Client
			err    error
		}{got, err}
	}()
	select {
	case result := <-secondDone:
		t.Fatalf("second EnsureClient returned before first publish: client=%T err=%v", result.client, result.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(client.openRelease)

	var first, second struct {
		client Client
		err    error
	}
	select {
	case first = <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first EnsureClient did not finish after bootstrap release")
	}
	select {
	case second = <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second EnsureClient did not finish after first publish")
	}
	if first.err != nil || second.err != nil {
		t.Fatalf("EnsureClient results = first(%T, %v), second(%T, %v), want both successful", first.client, first.err, second.client, second.err)
	}
	if first.client != client || second.client != client {
		t.Fatalf("EnsureClient clients = first(%p), second(%p), want published client %p", first.client, second.client, client)
	}

	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	var workspace *workspaceClient
	for _, candidate := range mgr.workspaces {
		if candidate != nil && candidate.client == client {
			workspace = candidate
			break
		}
	}
	if workspace == nil || workspace.state == workspaceStateBootstrapping {
		t.Fatalf("published workspace = %#v, want ready state", workspace)
	}
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
	if !errors.Is(err, didOpenErr) || !errors.Is(err, closeErr) {
		t.Fatalf("EnsureClient() error = %v, want DidOpen and Close errors", err)
	}
	mgr.mu.RLock()
	var pending *workspaceClient
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			pending = workspace
			break
		}
	}
	mgr.mu.RUnlock()
	if pending == nil || pending.generation == 0 || pending.state != workspaceStateCleanupPending || pending.activeLeases != 0 {
		t.Fatalf("bootstrap cleanup owner = %#v, want generation-owned CleanupPending with no leases", pending)
	}
	if got := client.closeCallCount(); got != 1 {
		t.Fatalf("bootstrap owner Close calls = %d, want one deterministic cleanup attempt", got)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("manager Close retry: %v", err)
	}
	if got := client.closeCallCount(); got != 2 {
		t.Fatalf("bootstrap owner Close calls after manager retry = %d, want 2", got)
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
	once        sync.Once
}

func (c *barrierBootstrapClient) DidOpen(context.Context, string, string, int, string) error {
	c.once.Do(func() { close(c.openStarted) })
	select {
	case <-c.openRelease:
		return nil
	}
}

type bootstrapFailureOwnerClient struct {
	noopClient
	mu         sync.Mutex
	didOpenErr error
	closeErr   error
	closeCalls int
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
