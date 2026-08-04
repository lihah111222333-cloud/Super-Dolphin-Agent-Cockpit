package multilsp

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestReleaseScopeCloseFailureReceiptRetriesUntilClosed(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-close-retry", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	closeErr := errors.New("transient close failure")
	client := &retryCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{},
		failures:          1,
		err:               closeErr,
	}
	attachWorkspaceClientForReleaseTest(scoped, "close-retry", client)

	first, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-close-retry",
		ThreadID:  "thread-1",
		Drain:     true,
		Reason:    "close_retry_first",
	})
	if !errors.Is(err, closeErr) {
		t.Fatalf("ReleaseScope(first) error = %v, want %v", err, closeErr)
	}
	if first.MatchedManagers != 1 || first.ClosedManagers != 0 || first.Drained {
		t.Fatalf("ReleaseScope(first) = %#v, want matched failure receipt", first)
	}

	second, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-close-retry",
		ThreadID:  "thread-1",
		Drain:     true,
		Reason:    "close_retry_second",
	})
	if err != nil {
		t.Fatalf("ReleaseScope(second): %v", err)
	}
	if second.MatchedManagers != 1 || second.ClosedManagers != 1 || !second.Drained {
		t.Fatalf("ReleaseScope(second) = %#v, want retained receipt to close and drain", second)
	}
	if got := client.closeCallCount(); got != 2 {
		t.Fatalf("client Close calls = %d, want 2", got)
	}
}

func TestDeferredReleaseCloseFailureRemainsRetryable(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-deferred-retry", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	closeErr := errors.New("transient deferred close failure")
	client := &retryCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{},
		failures:          1,
		err:               closeErr,
	}
	attachWorkspaceClientForReleaseTest(scoped, "deferred-close-retry", client)
	lease := acquireDeferredRetryLease(t, scoped, client)
	first := mustReleaseDeferredRetryScope(t, mgr, "deferred_retry_first")
	assertDeferredRetryInitialReceipt(t, first)

	if err := lease.Release(); err != nil {
		t.Fatalf("release deferred lease: %v", err)
	}
	ageWorkspaceForLifecycleTest(t, scoped, client)

	second, err := releaseDeferredRetryScope(mgr, "deferred_retry_second")
	if !errors.Is(err, closeErr) {
		t.Fatalf("ReleaseScope(second) error = %v, want %v from first deferred close retry", err, closeErr)
	}
	assertDeferredRetryFailureReceipt(t, second)
	third := mustReleaseDeferredRetryScope(t, mgr, "deferred_retry_third")
	assertDeferredRetryClosedReceipt(t, third)
	assertRetryCloseCallCount(t, client, 2)
}

func acquireDeferredRetryLease(t *testing.T, scoped *manager, client Client) leasedClient {
	t.Helper()
	lease, bound, err := scoped.leaseBoundClient(client)
	if err != nil || !bound {
		t.Fatalf("leaseBoundClient(deferred): bound=%v err=%v", bound, err)
	}
	return lease
}

func releaseDeferredRetryScope(mgr *manager, reason string) (ReleaseScopeResult, error) {
	return mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-deferred-retry",
		ThreadID:  "thread-1",
		Drain:     true,
		Reason:    reason,
	})
}

func mustReleaseDeferredRetryScope(t *testing.T, mgr *manager, reason string) ReleaseScopeResult {
	t.Helper()
	result, err := releaseDeferredRetryScope(mgr, reason)
	if err != nil {
		t.Fatalf("ReleaseScope(%s): %v", reason, err)
	}
	return result
}

func assertDeferredRetryInitialReceipt(t *testing.T, result ReleaseScopeResult) {
	t.Helper()
	if result.BusyLeases != 1 || result.Drained {
		t.Fatalf("ReleaseScope(first) = %#v, want one deferred lease", result)
	}
}

func assertDeferredRetryFailureReceipt(t *testing.T, result ReleaseScopeResult) {
	t.Helper()
	if result.MatchedManagers != 1 || result.ClosedManagers != 0 || result.BusyLeases != 0 || result.Drained {
		t.Fatalf("ReleaseScope(second) = %#v, want retained retry receipt after close failure", result)
	}
}

func assertDeferredRetryClosedReceipt(t *testing.T, result ReleaseScopeResult) {
	t.Helper()
	if result.MatchedManagers != 1 || result.ClosedManagers != 1 || !result.Drained {
		t.Fatalf("ReleaseScope(third) = %#v, want deferred failure retried to completion", result)
	}
}

func assertRetryCloseCallCount(t *testing.T, client *retryCloseP2Client, want int) {
	t.Helper()
	if got := client.closeCallCount(); got != want {
		t.Fatalf("client Close calls = %d, want %d", got, want)
	}
}

func TestReleaseScopeNonDrainDetachRejectsLeaseBeforeClose(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-nondrain-gate", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	client := &p2LifecycleClient{}
	attachWorkspaceClientForReleaseTest(scoped, "nondrain-gate", client)

	_, toClose, _ := mgr.pool.detachReleaseScopeManagers(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-nondrain-gate",
		ThreadID:  "thread-1",
		Drain:     false,
		Reason:    "nondrain_gate",
	})
	t.Cleanup(func() {
		for _, release := range toClose {
			if release.manager != nil {
				_ = release.manager.closeWithoutPool()
			}
		}
	})
	if len(toClose) != 1 || toClose[0].manager != scoped {
		t.Fatalf("detached managers = %#v, want scoped manager", toClose)
	}
	if _, bound, err := scoped.leaseBoundClient(client); !errors.Is(err, ErrManagerClosed) || bound {
		t.Fatalf("leaseBoundClient after detach = bound=%v err=%v, want false/ErrManagerClosed", bound, err)
	}
}

func TestManagerPoolCloseIsTerminalAndRetriesCleanup(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-pool-close", "thread-old")
	scoped := scopedManagerForTest(t, mgr, scope)
	closeErr := errors.New("transient pool close failure")
	client := &retryCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{},
		failures:          1,
		err:               closeErr,
	}
	attachWorkspaceClientForReleaseTest(scoped, "pool-close-retry", client)

	if err := mgr.pool.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close(first) error = %v, want %v", err, closeErr)
	}
	for name, candidate := range map[string]LSPToolScope{
		"old": scope,
		"new": testLSPToolScope(root, "agent-pool-close", "thread-new"),
	} {
		if _, err := mgr.pool.ForScope(candidate); !errors.Is(err, ErrManagerPoolClosed) {
			t.Fatalf("ForScope(%s) error = %v, want ErrManagerPoolClosed", name, err)
		}
	}
	if err := mgr.pool.Close(); err != nil {
		t.Fatalf("Close(second): %v", err)
	}
	if err := mgr.pool.Close(); err != nil {
		t.Fatalf("Close(third): %v", err)
	}
	if got := client.closeCallCount(); got != 2 {
		t.Fatalf("client Close calls = %d, want 2", got)
	}
}

func TestManagerPoolCapEvictionFailureRetainsCleanupOwner(t *testing.T) {
	t.Setenv(lspPoolSizeEnv, "1")
	t.Setenv(lspPoolShardCapEnv, "1")
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	first := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-cap-owner", "thread-old"))
	closeErr := errors.New("transient cap cleanup failure")
	client := &retryCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{},
		failures:          1,
		err:               closeErr,
	}
	attachWorkspaceClientForReleaseTest(first, "cap-cleanup-owner", client)

	if _, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-cap-owner", "thread-new")); !errors.Is(err, closeErr) {
		t.Fatalf("ForScope(new) error = %v, want %v", err, closeErr)
	}
	if got := pendingReleaseCountForTest(mgr.pool); got != 1 {
		t.Fatalf("pending cleanup owners after cap failure = %d, want 1", got)
	}

	mgr.pool.drainPendingReleases()
	if got := pendingReleaseCountForTest(mgr.pool); got != 0 {
		t.Fatalf("pending cleanup owners after retry = %d, want 0", got)
	}
	if got := client.closeCallCount(); got != 2 {
		t.Fatalf("client Close calls = %d, want 2", got)
	}
}

type retryCloseP2Client struct {
	*p2LifecycleClient

	mu       sync.Mutex
	failures int
	calls    int
	err      error
}

func (c *retryCloseP2Client) Close() error {
	c.mu.Lock()
	c.calls++
	shouldFail := c.calls <= c.failures
	c.mu.Unlock()
	if shouldFail {
		return c.err
	}
	return c.p2LifecycleClient.Close()
}

func (c *retryCloseP2Client) closeCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func attachWorkspaceClientForReleaseTest(mgr *manager, key string, client Client) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.workspaces[key] = &workspaceClient{
		key:          key,
		client:       client,
		generation:   1,
		state:        workspaceStateIdleCountdown,
		idleSince:    time.Now().Add(-2 * idleTimeoutForTest()),
		lastActivity: time.Now().Add(-2 * idleTimeoutForTest()),
	}
}

func pendingReleaseCountForTest(pool *ManagerPool) int {
	pool.pendingMu.Lock()
	defer pool.pendingMu.Unlock()
	return len(pool.pendingReleases)
}
