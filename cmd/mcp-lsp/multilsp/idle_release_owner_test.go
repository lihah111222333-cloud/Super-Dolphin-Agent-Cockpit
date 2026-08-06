package multilsp

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestRecyclerIdleGoWorkspaceUsesIdleReleaseOwner exercises the production
// manager/recycler path. The probe models a gopls root lease owner: its idle
// release closes only the forwarder and publishes the completion receipt.
func TestRecyclerIdleGoWorkspaceUsesIdleReleaseOwner(t *testing.T) {
	now := time.Now()
	probe := &idleReleaseOwnerProbe{closed: make(chan struct{}, 1)}
	mgr := &manager{
		idleTimeout: 15 * time.Minute,
		workspaces: map[string]*workspaceClient{
			"go-root": {
				key:          "go-root",
				rootPath:     "/workspace/go",
				languageID:   "go",
				client:       probe,
				generation:   7,
				state:        workspaceStateIdleCountdown,
				lastActivity: now.Add(-15*time.Minute - time.Second),
				idleSince:    now.Add(-15*time.Minute - time.Second),
			},
		},
	}
	recycler := newPoolRecycler(nil)
	recycler.now = func() time.Time { return now }

	recycler.checkIdleWorkspaces(mgr, ResolvedLSPToolScope{LSPToolScope: LSPToolScope{LanguageID: "go"}, WorkspaceKey: "go-root"})

	if got := probe.releaseCalls.Load(); got != 1 {
		t.Fatalf("idle recycler ReleaseForIdle calls = %d, want 1", got)
	}
	if got := probe.closeCalls.Load(); got != 0 {
		t.Fatalf("idle recycler direct Close calls = %d, want 0", got)
	}
	if got := probe.shutdownCalls.Load(); got != 1 {
		t.Fatalf("idle recycler Shutdown calls = %d, want 1", got)
	}
	if got, _ := probe.completionReceipt.Load().(string); got == "" {
		t.Fatal("idle root lease release did not publish a completion receipt")
	}
	select {
	case <-probe.closed:
	default:
		t.Fatal("idle root lease release did not close the forwarder")
	}
	if _, ok := mgr.workspaces["go-root"]; ok {
		t.Fatal("idle workspace remained bound after successful owner release")
	}
}

func TestRecyclerRequiredIdleReleaseFailsClosedWithoutOwner(t *testing.T) {
	now := time.Now()
	probe := &idleReleaseRequiredWithoutOwnerProbe{}
	mgr := &manager{
		idleTimeout: 15 * time.Minute,
		workspaces: map[string]*workspaceClient{
			"go-root": {
				key:          "go-root",
				rootPath:     "/workspace/go",
				languageID:   "go",
				client:       probe,
				generation:   8,
				state:        workspaceStateIdleCountdown,
				lastActivity: now.Add(-16 * time.Minute),
				idleSince:    now.Add(-16 * time.Minute),
			},
		},
	}
	recycler := newPoolRecycler(nil)
	recycler.now = func() time.Time { return now }

	recycler.checkIdleWorkspaces(mgr, ResolvedLSPToolScope{LSPToolScope: LSPToolScope{LanguageID: "go"}, WorkspaceKey: "go-root"})

	if got := probe.closeCalls.Load(); got != 0 {
		t.Fatalf("missing idle owner direct Close calls = %d, want 0", got)
	}
	if got := probe.shutdownCalls.Load(); got != 0 {
		t.Fatalf("missing idle owner Shutdown calls = %d, want 0", got)
	}
	workspace, ok := mgr.workspaces["go-root"]
	if !ok || workspace.state != workspaceStateCleanupPending {
		t.Fatalf("missing idle owner workspace = (%+v, %v), want CleanupPending retention", workspace, ok)
	}
	if _, err := shutdownWorkspaceClientForIdle(probe); !errors.Is(err, ErrIdleReleaseOwnerUnavailable) {
		t.Fatalf("missing idle owner error = %v, want ErrIdleReleaseOwnerUnavailable", err)
	}
}

type idleReleaseOwnerProbe struct {
	Client
	shutdownCalls     atomic.Int32
	closeCalls        atomic.Int32
	releaseCalls      atomic.Int32
	completionReceipt atomic.Value
	closed            chan struct{}
}

func (p *idleReleaseOwnerProbe) Shutdown(context.Context) error {
	p.shutdownCalls.Add(1)
	return nil
}

func (p *idleReleaseOwnerProbe) Close() error {
	p.closeCalls.Add(1)
	return nil
}

func (p *idleReleaseOwnerProbe) RequiresIdleRelease() bool { return true }

func (p *idleReleaseOwnerProbe) ReleaseForIdle() error {
	if p.releaseCalls.Add(1) == 1 {
		p.completionReceipt.Store("gopls-root-drain-complete-test")
		p.closed <- struct{}{}
	}
	return nil
}

type idleReleaseRequiredWithoutOwnerProbe struct {
	Client
	shutdownCalls atomic.Int32
	closeCalls    atomic.Int32
}

func (p *idleReleaseRequiredWithoutOwnerProbe) Shutdown(context.Context) error {
	p.shutdownCalls.Add(1)
	return nil
}

func (p *idleReleaseRequiredWithoutOwnerProbe) Close() error {
	p.closeCalls.Add(1)
	return nil
}

func (p *idleReleaseRequiredWithoutOwnerProbe) RequiresIdleRelease() bool { return true }
