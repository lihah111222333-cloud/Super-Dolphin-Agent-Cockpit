package multilsp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestReleaseScopeDrainRejectsNewLeaseOnDetachedManager(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module gated\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-gated", "thread-1"))
	client, err := scoped.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	lease, bound, leaseErr := scoped.leaseBoundClient(client)
	if leaseErr != nil || !bound {
		t.Fatalf("leaseBoundClient(gated): bound=%v err=%v", bound, leaseErr)
	}

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-gated",
		ThreadID:  "thread-1",
		Drain:     true,
		Reason:    "lease_registration_gate",
	})
	if err != nil {
		t.Fatalf("ReleaseScope(gated): %v", err)
	}
	if result.BusyLeases != 1 || result.Drained {
		t.Fatalf("ReleaseScope(gated) = %#v, want one busy lease and drained=false", result)
	}
	if _, _, leaseErr := scoped.leaseBoundClient(client); !errors.Is(leaseErr, ErrManagerClosed) {
		t.Fatalf("leaseBoundClient() error = %v, want ErrManagerClosed after drain gate", leaseErr)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("release gated lease: %v", err)
	}
	ageWorkspaceForLifecycleTest(t, scoped, client)
	mgr.pool.drainPendingReleases()
	if !managerIsClosed(scoped) {
		t.Fatal("gated manager stayed open after its final lease was released")
	}
}

func TestReleaseScopeCloseFailureDoesNotReportClosedOrDrained(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-close-failure", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	closeErr := errors.New("close failed")
	scoped.mu.Lock()
	scoped.workspaces["close-failure"] = &workspaceClient{
		key:        "close-failure",
		client:     &failingCloseP2Client{p2LifecycleClient: &p2LifecycleClient{healthy: true}, err: closeErr},
		generation: 1,
		state:      workspaceStateIdleCountdown,
		idleSince:  time.Now().Add(-2 * idleTimeoutForTest()),
	}
	scoped.mu.Unlock()

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-close-failure",
		ThreadID:  "thread-1",
		Drain:     true,
		Reason:    "close_failure",
	})
	if !errors.Is(err, closeErr) {
		t.Fatalf("ReleaseScope() error = %v, want %v", err, closeErr)
	}
	if result.MatchedManagers != 1 || result.ClosedManagers != 0 || result.Drained {
		t.Fatalf("ReleaseScope() result = %#v, want matched=1 closed=0 drained=false", result)
	}
}

type failingCloseP2Client struct {
	*p2LifecycleClient
	err    error
	failed bool
}

func (c *failingCloseP2Client) Close() error {
	c.p2LifecycleClient.mu.Lock()
	defer c.p2LifecycleClient.mu.Unlock()
	if !c.failed {
		c.failed = true
		return c.err
	}
	c.closed = true
	c.healthy = false
	return nil
}

func TestPoolRecyclerEvictsIdleEmptyCloneByTTL(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-idle-clone", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope: %v", err)
	}
	shard := mgr.pool.shardForKey(resolved.ShardKey)
	shard.mu.Lock()
	clone := shard.clones[resolved.ManagerKey]
	if clone == nil {
		shard.mu.Unlock()
		t.Fatalf("clone %q not found", resolved.ManagerKey)
	}
	clone.lastUsedAt = time.Now().Add(-idleTimeoutForTest() - time.Second)
	shard.mu.Unlock()

	mgr.pool.recycler.check()

	shard.mu.RLock()
	_, retained := shard.clones[resolved.ManagerKey]
	shard.mu.RUnlock()
	if retained {
		t.Fatalf("idle empty clone %q was retained after recycler check", resolved.ManagerKey)
	}
	if !managerIsClosed(scoped) {
		t.Fatal("idle empty clone manager was detached without Close")
	}
}

func TestPoolIdleCloneDetachRejectsNewClientBeforeClose(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module ttl-gate\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scope := testLSPToolScope(root, "agent-ttl-gate", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope: %v", err)
	}
	shard := mgr.pool.shardForKey(resolved.ShardKey)
	shard.mu.Lock()
	clone := shard.clones[resolved.ManagerKey]
	clone.lastUsedAt = time.Now().Add(-idleTimeoutForTest() - time.Second)
	shard.mu.Unlock()

	detached := mgr.pool.detachIdleEmptyClones(time.Now().Add(-idleTimeoutForTest()))
	if len(detached) != 1 || detached[0].manager != scoped {
		t.Fatalf("detachIdleEmptyClones() = %#v, want detached scoped manager", detached)
	}
	_, err = scoped.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target, "go")
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("EnsureClient() error = %v, want ErrManagerClosed after TTL detach", err)
	}
	if got := factory.callCount(); got != 0 {
		t.Fatalf("factory calls after TTL detach = %d, want 0", got)
	}
}
