package multilsp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type releaseOutcome struct {
	result ReleaseScopeResult
	err    error
}

func TestReleaseScopeDoesNotSynchronouslyDrainOtherPendingReleases(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	const pendingCount = 3
	shutdownStarted := make(chan *blockingShutdownP2Client, pendingCount)
	pendingScopes, pendingManagers, pendingClients := preparePendingReleaseScopes(t, mgr, root, shutdownStarted)
	t.Cleanup(func() {
		allowPendingShutdowns(pendingClients)
	})

	assertUnrelatedReleaseDoesNotDrainPending(t, mgr, shutdownStarted, pendingClients)
	runPendingReleaseRecycler(t, mgr, shutdownStarted, pendingManagers, pendingClients)
	assertCompletedPendingReleaseReceipts(t, mgr, pendingScopes)
}

func preparePendingReleaseScopes(
	t *testing.T,
	mgr *manager,
	root string,
	shutdownStarted chan *blockingShutdownP2Client,
) ([]LSPToolScope, []*manager, []*blockingShutdownP2Client) {
	t.Helper()
	const pendingCount = 3
	pendingScopes := make([]LSPToolScope, 0, pendingCount)
	pendingManagers := make([]*manager, 0, pendingCount)
	pendingClients := make([]*blockingShutdownP2Client, 0, pendingCount)
	for _, agentID := range []string{
		"agent-pending-release-1",
		"agent-pending-release-2",
		"agent-pending-release-3",
	} {
		scope := testLSPToolScope(root, agentID, "thread-1")
		pending := scopedManagerForTest(t, mgr, scope)
		client := &blockingShutdownP2Client{
			p2LifecycleClient: &p2LifecycleClient{},
			shutdownStarted:   shutdownStarted,
			allowShutdown:     make(chan struct{}),
		}
		attachWorkspaceClientForReleaseTest(pending, agentID, client)
		lease := acquireDeferredRetryLease(t, pending, client)
		initial, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
			ScopeKind: ReleaseScopeAgentThread,
			AgentID:   scope.AgentID,
			ThreadID:  scope.ThreadID,
			Drain:     true,
			Reason:    "defer_pending_release",
		})
		if err != nil {
			t.Fatalf("ReleaseScope(initial %s): %v", agentID, err)
		}
		assertDeferredRetryInitialReceipt(t, initial)
		if err := lease.Release(); err != nil {
			t.Fatalf("release deferred lease for %s: %v", agentID, err)
		}
		ageWorkspaceForLifecycleTest(t, pending, client)
		pendingScopes = append(pendingScopes, scope)
		pendingManagers = append(pendingManagers, pending)
		pendingClients = append(pendingClients, client)
	}
	return pendingScopes, pendingManagers, pendingClients
}

func assertUnrelatedReleaseDoesNotDrainPending(
	t *testing.T,
	mgr *manager,
	shutdownStarted <-chan *blockingShutdownP2Client,
	pendingClients []*blockingShutdownP2Client,
) {
	t.Helper()
	releaseDone := make(chan releaseOutcome, 1)
	go func() {
		result, releaseErr := mgr.pool.ReleaseScope(ReleaseScopeRequest{
			ScopeKind: ReleaseScopeAgentThread,
			AgentID:   "agent-unrelated-release",
			ThreadID:  "thread-1",
			Drain:     true,
			Reason:    "must_not_drain_other_pending_release",
		})
		releaseDone <- releaseOutcome{result: result, err: releaseErr}
	}()

	select {
	case client := <-shutdownStarted:
		client.allow()
		allowPendingShutdowns(pendingClients)
		<-releaseDone
		t.Fatal("ReleaseScope synchronously drained unrelated pending releases")
	case outcome := <-releaseDone:
		if outcome.err != nil {
			t.Fatalf("ReleaseScope(unrelated): %v", outcome.err)
		}
		if outcome.result.MatchedManagers != 0 || outcome.result.ClosedManagers != 0 || !outcome.result.Drained {
			t.Fatalf("ReleaseScope(unrelated) = %#v, want empty drained receipt", outcome.result)
		}
	case <-time.After(250 * time.Millisecond):
		allowPendingShutdowns(pendingClients)
		<-releaseDone
		t.Fatal("ReleaseScope(unrelated) exceeded the multi-pending cleanup latency bound")
	}
}

func runPendingReleaseRecycler(
	t *testing.T,
	mgr *manager,
	shutdownStarted <-chan *blockingShutdownP2Client,
	pendingManagers []*manager,
	pendingClients []*blockingShutdownP2Client,
) {
	t.Helper()
	recycler, ok := mgr.pool.RecyclerRunner().(*poolRecycler)
	if !ok || recycler == nil {
		t.Fatalf("RecyclerRunner() = %T, want *poolRecycler", mgr.pool.RecyclerRunner())
	}
	recyclerDone := make(chan struct{})
	go func() {
		recycler.check()
		close(recyclerDone)
	}()
	for range pendingClients {
		select {
		case client := <-shutdownStarted:
			client.allow()
		case <-time.After(time.Second):
			t.Fatal("recycler did not begin deferred pending cleanup")
		}
	}
	select {
	case <-recyclerDone:
	case <-time.After(time.Second):
		t.Fatal("recycler did not finish deferred pending cleanup")
	}
	for _, pending := range pendingManagers {
		if !managerIsClosed(pending) {
			t.Fatal("pending manager stayed open after recycler cleanup")
		}
	}
}

func assertCompletedPendingReleaseReceipts(t *testing.T, mgr *manager, pendingScopes []LSPToolScope) {
	t.Helper()
	for _, scope := range pendingScopes {
		completed, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
			ScopeKind: ReleaseScopeAgentThread,
			AgentID:   scope.AgentID,
			ThreadID:  scope.ThreadID,
			Drain:     true,
			Reason:    "consume_completed_pending_release",
		})
		if err != nil {
			t.Fatalf("ReleaseScope(completed %s): %v", scope.AgentID, err)
		}
		if completed.MatchedManagers != 1 || completed.ClosedManagers != 1 || !completed.Drained {
			t.Fatalf("ReleaseScope(completed %s) = %#v, want completed drain receipt", scope.AgentID, completed)
		}
	}
}

func allowPendingShutdowns(clients []*blockingShutdownP2Client) {
	for _, client := range clients {
		client.allow()
	}
}

func TestReleaseScopeCloseFailureReceiptRetriesUntilClosed(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	var logs bytes.Buffer
	mgr.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	agentID, threadID := "agent-"+root, "thread-"+root
	scope := testLSPToolScope(root, agentID, threadID)
	scoped := scopedManagerForTest(t, mgr, scope)
	closeErr := errors.New("transient close failure at " + root)
	client := &retryCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{},
		failures:          1,
		err:               closeErr,
	}
	attachWorkspaceClientForReleaseTest(scoped, "close-retry", client)

	first, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   agentID,
		ThreadID:  threadID,
		Drain:     true,
		Reason:    "close_retry_first_" + root,
	})
	if !errors.Is(err, closeErr) {
		t.Fatalf("ReleaseScope(first) error = %v, want %v", err, closeErr)
	}
	if first.MatchedManagers != 1 || first.ClosedManagers != 0 || first.Drained {
		t.Fatalf("ReleaseScope(first) = %#v, want matched failure receipt", first)
	}
	logText := logs.String()
	assertStructuredLogContains(t, "release scope cleanup log", logText,
		"LSP release scope closing manager",
		"LSP release scope close failed",
		`"manager_key_sha256":`,
		`"scope_key_sha256":`,
		`"workspace_sha256":`,
		`"cleanup_error_sha256":`,
		`"cleanup_error_class":"shutdown_or_close"`,
		`"release_reason_sha256":`,
	)
	assertStructuredLogOmits(t, "release scope cleanup log", logText,
		root, closeErr.Error(), agentID, threadID, `"manager_key":"`, `"scope_key":"`)
	runManagedRecyclerCheck(t, mgr.pool)

	second, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   agentID,
		ThreadID:  threadID,
		Drain:     true,
		Reason:    "close_retry_second_" + root,
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
	var logs bytes.Buffer
	mgr.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	scope := testLSPToolScope(root, "agent-deferred-retry", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	closeErr := errors.New("transient deferred close failure at " + root)
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
	runManagedRecyclerCheck(t, mgr.pool)

	second, err := releaseDeferredRetryScope(mgr, "deferred_retry_second")
	if !errors.Is(err, closeErr) {
		t.Fatalf("ReleaseScope(second) error = %v, want %v from first deferred close retry", err, closeErr)
	}
	logText := logs.String()
	assertStructuredLogContains(t, "deferred cleanup log", logText,
		"LSP deferred release close failed",
		`"manager_key_sha256":`,
		`"scope_key_sha256":`,
		`"workspace_sha256":`,
		`"cleanup_error_sha256":`,
		`"cleanup_error_class":"shutdown_or_close"`,
		`"action":"close"`,
		`"action_result":"failed"`,
	)
	assertStructuredLogOmits(t, "deferred cleanup log", logText,
		root, closeErr.Error(), `"manager_key":"`, `"scope_key":"`)
	assertDeferredRetryFailureReceipt(t, second)
	runManagedRecyclerCheck(t, mgr.pool)
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

func runManagedRecyclerCheck(t *testing.T, pool *ManagerPool) {
	t.Helper()
	recycler, ok := pool.RecyclerRunner().(*poolRecycler)
	if !ok || recycler == nil {
		t.Fatalf("RecyclerRunner() = %T, want *poolRecycler", pool.RecyclerRunner())
	}
	recycler.check()
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
	var logs bytes.Buffer
	mgr.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	first := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-cap-owner", "thread-old"))
	closeErr := errors.New("transient cap cleanup failure at " + root)
	client := &retryCloseP2Client{
		p2LifecycleClient: &p2LifecycleClient{},
		failures:          1,
		err:               closeErr,
	}
	attachWorkspaceClientForReleaseTest(first, "cap-cleanup-owner", client)

	if _, err := mgr.pool.ForScope(testLSPToolScope(root, "agent-cap-owner", "thread-new")); !errors.Is(err, closeErr) {
		t.Fatalf("ForScope(new) error = %v, want %v", err, closeErr)
	}
	logText := logs.String()
	assertStructuredLogContains(t, "cap eviction cleanup log", logText,
		"LSP detached manager close failed",
		`"manager_key_sha256":`,
		`"scope_key_sha256":`,
		`"workspace_sha256":`,
		`"cleanup_error_sha256":`,
		`"cleanup_error_class":"shutdown_or_close"`,
		`"action":"close"`,
		`"action_result":"failed"`,
	)
	assertStructuredLogOmits(t, "cap eviction cleanup log", logText,
		root, closeErr.Error(), `"manager_key":"`, `"scope_key":"`)
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

type blockingShutdownP2Client struct {
	*p2LifecycleClient

	shutdownStarted chan<- *blockingShutdownP2Client
	allowShutdown   chan struct{}
	shutdownOnce    sync.Once
	allowOnce       sync.Once
}

func (c *blockingShutdownP2Client) Shutdown(ctx context.Context) error {
	c.shutdownOnce.Do(func() { c.shutdownStarted <- c })
	select {
	case <-c.allowShutdown:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *blockingShutdownP2Client) allow() {
	c.allowOnce.Do(func() { close(c.allowShutdown) })
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
