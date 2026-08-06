package mcpcontrol

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestManagedCallbackAllowsRegistryReentryAndConcurrentLeaseProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	registry, lease, entered, release, notifyDone, joined := startBlockingManagedCallback(t, ctx)
	waitManagedCallbackEntered(t, entered)
	registerAndHeartbeatConcurrentLSP(t, registry)
	evictPinnedManagedLease(t, registry, lease)
	release()
	requireManagedCallbackStale(t, ctx, notifyDone)
	select {
	case <-joined:
	case <-ctx.Done():
		t.Fatalf("callback worker did not join: %v", ctx.Err())
	}
}

func startBlockingManagedCallback(
	t *testing.T,
	ctx context.Context,
) (*ToolRegistry, dto.LeaseKey, <-chan struct{}, func(), <-chan error, <-chan struct{}) {
	t.Helper()
	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: managedOrchInstanceID, Generation: 1}
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCallback := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(releaseCallback)
	peer := &stubCallbackPeer{
		notifyFn: func(ctx context.Context, _ string, _ any) error {
			if _, err := registry.Heartbeat(ctx, dto.HeartbeatRequest{
				InstanceID: lease.InstanceID,
				Generation: lease.Generation,
			}); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		},
	}
	instance := &ToolInstance{
		Lease:        lease,
		BinaryName:   "mcp-orch",
		ClientKind:   dto.ClientKindOrch,
		PeerKind:     dto.PeerKindSharedService,
		Shared:       true,
		Managed:      true,
		Capabilities: []string{"blocking-callback"},
		Status:       dto.StatusActive,
		Peer:         peer,
		runtime:      newLeaseRuntime(peer),
	}
	registry.mu.Lock()
	addIndexedInstance(registry, instance)
	registry.mu.Unlock()

	notifyDone := make(chan error, 1)
	var callback sync.WaitGroup
	callback.Go(func() {
		notifyDone <- registry.NotifyByCapability(
			ctx,
			"blocking-callback",
			"test/block",
			map[string]bool{"ok": true},
		)
	})
	joined := make(chan struct{})
	var joiner sync.WaitGroup
	joiner.Go(func() {
		callback.Wait()
		close(joined)
	})
	return registry, lease, entered, releaseCallback, notifyDone, joined
}

func waitManagedCallbackEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("callback did not reenter Heartbeat; registry lock may be held during network callback")
	}
}

func registerAndHeartbeatConcurrentLSP(t *testing.T, registry *ToolRegistry) {
	t.Helper()
	local := jrpcserver.NewLocal(handler.Map{
		dto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, request dto.RegisterRequest) (dto.RegisterResponse, error) {
			return registry.Register(ctx, request)
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()
	var registered dto.RegisterResponse
	registerCtx, cancelRegister := context.WithTimeout(context.Background(), time.Second)
	defer cancelRegister()
	if err := local.Client.CallResult(registerCtx, dto.MethodRegister, dto.RegisterRequest{
		InstanceID: "concurrent-lsp",
		BinaryName: "mcp-lsp",
		ClientKind: dto.ClientKindLSP,
		PeerKind:   dto.PeerKindTool,
		PID:        101,
	}, &registered); err != nil {
		t.Fatalf("concurrent LSP Register() error = %v", err)
	}
	if _, err := registry.Heartbeat(context.Background(), dto.HeartbeatRequest{
		InstanceID: registered.InstanceID,
		Generation: registered.Generation,
	}); err != nil {
		t.Fatalf("concurrent LSP Heartbeat() error = %v", err)
	}
}

func evictPinnedManagedLease(t *testing.T, registry *ToolRegistry, lease dto.LeaseKey) {
	t.Helper()
	registry.mu.Lock()
	retiredPeer := registry.evictLocked(lease)
	registry.mu.Unlock()
	if retiredPeer != nil {
		t.Fatal("eviction returned peer while callback pin was live")
	}
}

func requireManagedCallbackStale(t *testing.T, ctx context.Context, notifyDone <-chan error) {
	t.Helper()
	select {
	case err := <-notifyDone:
		if err == nil || !strings.Contains(err.Error(), "became stale during callback") {
			t.Fatalf("NotifyByCapability() error = %v, want stale generation fence", err)
		}
	case <-ctx.Done():
		t.Fatalf("callback did not finish after replacement fence: %v", ctx.Err())
	}
}

func TestManagedLeasePinFencesReplacementAndDefersClose(t *testing.T) {
	registry := newStrictManagedTestRegistry(NewMemoryGenerationStore())
	bootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("IssueManagedAuthority() error = %v", err)
	}
	_, err = callManagedRegister(t, registry, managedRegisterRequest(bootstrap, "request-1"))
	if err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	instance := registry.FindActiveByKind(dto.ClientKindOrch)[0]
	pin, err := instance.Pin()
	if err != nil {
		t.Fatalf("Pin() error = %v", err)
	}

	replacementBootstrap, err := registry.IssueManagedAuthority(context.Background(), dto.ManagedAuthorityIssueRequest{BinaryName: "mcp-orch"})
	if err != nil {
		t.Fatalf("replacement IssueManagedAuthority() error = %v", err)
	}
	if _, err := callManagedRegister(t, registry, managedRegisterRequest(replacementBootstrap, "request-2")); err != nil {
		t.Fatalf("replacement Register() error = %v", err)
	}
	if pin.Current() {
		t.Fatal("old pin Current() = true after replacement")
	}
	if err := pin.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := instance.Pin(); !errors.Is(err, ErrManagedLeaseStale) {
		t.Fatalf("old instance Pin() error = %v, want ErrManagedLeaseStale", err)
	}
}
