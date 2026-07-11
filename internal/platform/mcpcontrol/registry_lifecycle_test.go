package mcpcontrol

import (
	"context"
	"reflect"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"go.uber.org/fx"
)

type stubHookLifecycle struct {
	calls      []dto.LeaseKey
	shutdownFn func(context.Context, dto.LeaseKey) error
}

func (s *stubHookLifecycle) ShutdownHooks(ctx context.Context, lease dto.LeaseKey) error {
	s.calls = append(s.calls, lease)
	if s.shutdownFn != nil {
		return s.shutdownFn(ctx, lease)
	}
	return nil
}

type stubLifecyclePeer struct {
	callbackCount int
	closeCount    int
	callbackFn    func(context.Context, string, any, any) error
}

type lifecycleHookRecorder struct {
	hooks []fx.Hook
}

func (r *lifecycleHookRecorder) Append(hook fx.Hook) {
	r.hooks = append(r.hooks, hook)
}

func (s *stubLifecyclePeer) Notify(context.Context, string, any) error { return nil }

func (s *stubLifecyclePeer) Callback(ctx context.Context, method string, params any, result any) error {
	s.callbackCount++
	if s.callbackFn != nil {
		return s.callbackFn(ctx, method, params, result)
	}
	return nil
}

func (s *stubLifecyclePeer) Close() error {
	s.closeCount++
	return nil
}

func TestShutdownInstance_CleansUpHooksBeforeCallback(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lifecycle := &stubHookLifecycle{}
	lease := dto.LeaseKey{InstanceID: "tool-1", Generation: 1}
	peer := &stubLifecyclePeer{
		callbackFn: func(_ context.Context, method string, params any, _ any) error {
			if method != dto.MethodShutdown {
				t.Fatalf("Callback() method = %q, want %q", method, dto.MethodShutdown)
			}
			if len(lifecycle.calls) != 1 || lifecycle.calls[0] != lease {
				t.Fatalf("ShutdownHooks() calls = %#v, want [%#v] before callback", lifecycle.calls, lease)
			}
			req, ok := params.(dto.ShutdownRequest)
			if !ok {
				t.Fatalf("Callback() params type = %T, want dto.ShutdownRequest", params)
			}
			gotLease := dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation}
			if gotLease != lease {
				t.Fatalf("ShutdownRequest lease = %#v, want %#v", gotLease, lease)
			}
			return nil
		},
	}
	registry.setHookLifecycle(lifecycle)
	registry.instances[lease] = &ToolInstance{Lease: lease, Peer: peer, Status: dto.StatusActive}
	registry.latestByInstance[lease.InstanceID] = lease

	if err := registry.ShutdownInstance(context.Background(), lease, dto.ShutdownRequest{}); err != nil {
		t.Fatalf("ShutdownInstance() error = %v", err)
	}
	if peer.callbackCount != 1 {
		t.Fatalf("Callback() count = %d, want 1", peer.callbackCount)
	}
	if len(lifecycle.calls) != 1 || lifecycle.calls[0] != lease {
		t.Fatalf("ShutdownHooks() calls = %#v, want [%#v]", lifecycle.calls, lease)
	}
}

func TestHeartbeat_DisconnectedCleansUpHooksAndEvictsLease(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lifecycle := &stubHookLifecycle{}
	lease := dto.LeaseKey{InstanceID: "tool-1", Generation: 2}
	peer := &stubLifecyclePeer{}
	registry.setHookLifecycle(lifecycle)
	registry.instances[lease] = &ToolInstance{Lease: lease, Peer: peer, Status: dto.StatusActive, ConfigVersion: 7}
	registry.latestByInstance[lease.InstanceID] = lease

	resp, err := registry.Heartbeat(context.Background(), dto.HeartbeatRequest{
		InstanceID: lease.InstanceID,
		Generation: lease.Generation,
		Status:     dto.StatusDisconnected,
	})
	if err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	if !resp.OK {
		t.Fatal("Heartbeat() OK = false, want true")
	}
	if len(lifecycle.calls) != 1 || lifecycle.calls[0] != lease {
		t.Fatalf("ShutdownHooks() calls = %#v, want [%#v]", lifecycle.calls, lease)
	}
	if _, ok := registry.instances[lease]; ok {
		t.Fatal("Heartbeat() left disconnected lease registered")
	}
	if peer.closeCount != 1 {
		t.Fatalf("Close() count = %d, want 1", peer.closeCount)
	}
}

func TestOnDisconnect_CleansUpHooksAndEvictsLease(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lifecycle := &stubHookLifecycle{}
	lease := dto.LeaseKey{InstanceID: "tool-1", Generation: 3}
	peer := &stubLifecyclePeer{}
	registry.setHookLifecycle(lifecycle)
	registry.instances[lease] = &ToolInstance{Lease: lease, Peer: peer, Status: dto.StatusActive}
	registry.latestByInstance[lease.InstanceID] = lease

	registry.OnDisconnect(lease)

	if len(lifecycle.calls) != 1 || lifecycle.calls[0] != lease {
		t.Fatalf("ShutdownHooks() calls = %#v, want [%#v]", lifecycle.calls, lease)
	}
	if _, ok := registry.instances[lease]; ok {
		t.Fatal("OnDisconnect() left lease registered")
	}
	if peer.closeCount != 1 {
		t.Fatalf("Close() count = %d, want 1", peer.closeCount)
	}
}

func TestRegisterRegistryLifecycle_OnStopCleansActiveLeases(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lifecycle := &stubHookLifecycle{
		shutdownFn: func(ctx context.Context, _ dto.LeaseKey) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("ShutdownHooks() ctx missing deadline")
			}
			return nil
		},
	}
	activeA := dto.LeaseKey{InstanceID: "tool-a", Generation: 1}
	activeB := dto.LeaseKey{InstanceID: "tool-b", Generation: 2}
	disconnected := dto.LeaseKey{InstanceID: "tool-c", Generation: 3}
	registry.instances[activeB] = &ToolInstance{Lease: activeB, Status: dto.StatusActive}
	registry.instances[disconnected] = &ToolInstance{Lease: disconnected, Status: dto.StatusDisconnected}
	registry.instances[activeA] = &ToolInstance{Lease: activeA, Status: dto.StatusActive}

	recorder := &lifecycleHookRecorder{}
	registerRegistryLifecycle(recorder, registry, lifecycle)

	if len(recorder.hooks) != 1 {
		t.Fatalf("registered hooks = %d, want 1", len(recorder.hooks))
	}
	if recorder.hooks[0].OnStop == nil {
		t.Fatal("OnStop = nil, want registry cleanup hook")
	}
	if err := recorder.hooks[0].OnStop(context.Background()); err != nil {
		t.Fatalf("OnStop() error = %v", err)
	}

	want := []dto.LeaseKey{activeA, activeB}
	if !reflect.DeepEqual(lifecycle.calls, want) {
		t.Fatalf("ShutdownHooks() calls = %#v, want %#v", lifecycle.calls, want)
	}
}
