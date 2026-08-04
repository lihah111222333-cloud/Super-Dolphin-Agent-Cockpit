package multilsp

import (
	"testing"
	"time"
)

func TestNewManagerWithErrorRejectsMissingIdleTimeout(t *testing.T) {
	if _, err := NewManagerWithError(Config{}); err == nil {
		t.Fatal("NewManagerWithError() error = nil, want missing idle timeout")
	}
}

func TestManagerClonePreservesConfiguredIdleTimeout(t *testing.T) {
	const want = 19 * time.Minute
	mgr := NewManager(Config{WorkspaceRoot: t.TempDir(), IdleTimeout: want}).(*manager)
	clone := mgr.cloneForWorkspace(t.TempDir())
	if clone.idleTimeout != want {
		t.Fatalf("clone idle timeout = %s, want %s", clone.idleTimeout, want)
	}
}

func TestPoolReleaseEligibilityUsesManagerIdleTimeout(t *testing.T) {
	const timeout = 2 * time.Second
	mgr := NewManager(Config{WorkspaceRoot: t.TempDir(), IdleTimeout: timeout}).(*manager)
	now := time.Now()
	mgr.workspaces["workspace"] = &workspaceClient{
		key:          "workspace",
		client:       &p2LifecycleClient{},
		generation:   1,
		state:        workspaceStateIdleCountdown,
		idleSince:    now.Add(-3 * time.Second),
		lastActivity: now.Add(-3 * time.Second),
	}
	if !mgr.pool.managerIdleEligibleForRelease(mgr) {
		t.Fatal("manager release should be idle-eligible after configured timeout")
	}
	mgr.idleTimeout = 4 * time.Second
	if mgr.pool.managerIdleEligibleForRelease(mgr) {
		t.Fatal("manager release used a stale or language-specific idle timeout")
	}
}
