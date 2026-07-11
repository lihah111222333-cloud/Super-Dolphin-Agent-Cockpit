package mcpcontrol

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type registeredLocalRegistry struct {
	registry *ToolRegistry
	lease    dto.LeaseKey
}

type stubCallbackPeer struct {
	notifyFn   func(context.Context, string, any) error
	callbackFn func(context.Context, string, any, any) error
}

func (p *stubCallbackPeer) Notify(ctx context.Context, method string, params any) error {
	if p.notifyFn != nil {
		return p.notifyFn(ctx, method, params)
	}
	return nil
}

func (p *stubCallbackPeer) Callback(ctx context.Context, method string, params any, result any) error {
	if p.callbackFn != nil {
		return p.callbackFn(ctx, method, params, result)
	}
	return nil
}

func (p *stubCallbackPeer) Close() error { return nil }

func addIndexedInstance(registry *ToolRegistry, instance *ToolInstance) {
	if registry == nil || instance == nil {
		return
	}
	if instance.Status == "" {
		instance.Status = dto.StatusActive
	}
	registry.instances[instance.Lease] = instance
	registry.latestByInstance[instance.Lease.InstanceID] = instance.Lease
	registry.indexLocked(instance)
}

func newRegisteredLocalRegistry(t *testing.T, extra handler.Map, req dto.RegisterRequest) *registeredLocalRegistry {
	t.Helper()

	registry := NewRegistry()
	methods := handler.Map{
		dto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
			return registry.Register(ctx, req)
		}),
	}
	maps.Copy(methods, extra)
	local := jrpcserver.NewLocal(methods, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{AllowPush: true}})
	t.Cleanup(func() {
		local.Close()
	})

	if req.InstanceID == "" {
		req.InstanceID = "instance-1"
	}
	if req.BinaryName == "" {
		req.BinaryName = "mcp-orch"
	}
	if req.ClientKind == "" {
		req.ClientKind = dto.ClientKindOrch
	}
	if req.PeerKind == "" {
		req.PeerKind = dto.PeerKindTool
	}

	var resp dto.RegisterResponse
	if err := local.Client.CallResult(context.Background(), dto.MethodRegister, req, &resp); err != nil {
		t.Fatalf("CallResult(register) error = %v", err)
	}
	return &registeredLocalRegistry{registry: registry, lease: dto.LeaseKey{InstanceID: resp.InstanceID, Generation: resp.Generation}}
}

func TestNewToolRegistry_Basic(t *testing.T) {
	t.Parallel()

	registry := NewToolRegistry(RegistryOptions{})
	assertRegistryDefaults(t, registry)
}

func assertRegistryDefaults(t *testing.T, registry *ToolRegistry) {
	t.Helper()
	if registry == nil {
		t.Fatal("NewToolRegistry() = nil")
	}
	assertRegistryIndexes(t, registry)
	assertRegistryTimingDefaults(t, registry)
}

func assertRegistryIndexes(t *testing.T, registry *ToolRegistry) {
	t.Helper()
	if registry.instances == nil || registry.bySubscription == nil || registry.byCapability == nil {
		t.Fatal("NewToolRegistry() did not initialize registry indexes")
	}
	if registry.latestByInstance == nil || registry.reportReceipts == nil {
		t.Fatal("NewToolRegistry() did not initialize lease bookkeeping")
	}
	if registry.configVersion != 1 {
		t.Fatalf("configVersion = %d, want 1", registry.configVersion)
	}
}

func assertRegistryTimingDefaults(t *testing.T, registry *ToolRegistry) {
	t.Helper()
	if registry.heartbeatInterval != defaultHeartbeatInterval {
		t.Fatalf("heartbeatInterval = %s, want %s", registry.heartbeatInterval, defaultHeartbeatInterval)
	}
	if registry.notifyTimeout != defaultNotifyTimeout {
		t.Fatalf("notifyTimeout = %s, want %s", registry.notifyTimeout, defaultNotifyTimeout)
	}
	if registry.fanoutParallelism != defaultFanoutParallelism {
		t.Fatalf("fanoutParallelism = %d, want %d", registry.fanoutParallelism, defaultFanoutParallelism)
	}
	if registry.peerFailureThreshold != defaultPeerFailureThreshold {
		t.Fatalf("peerFailureThreshold = %d, want %d", registry.peerFailureThreshold, defaultPeerFailureThreshold)
	}
}

func TestToolRegistry_Register_And_GetInstance(t *testing.T) {
	t.Parallel()

	env := newRegisteredLocalRegistry(t, nil, dto.RegisterRequest{
		InstanceID:          "instance-get",
		BinaryName:          "mcp-orch",
		AgentID:             "agent-get",
		ThreadID:            "thread-get",
		ClientKind:          dto.ClientKindOrch,
		PeerKind:            dto.PeerKindTool,
		PID:                 4321,
		CapabilitiesOffered: []string{"hooks", "reports"},
		Subscriptions:       []string{"config/agent", "config/thread"},
	})

	got, ok := env.registry.GetInstance(env.lease)
	if !ok {
		t.Fatal("GetInstance() ok = false, want true")
	}
	assertRegisteredInstance(t, got, env.lease)
}

func assertRegisteredInstance(t *testing.T, got contract.ToolInstance, lease dto.LeaseKey) {
	t.Helper()
	assertRegisteredInstanceIdentity(t, got, lease)
	assertRegisteredInstanceKinds(t, got)
	assertRegisteredInstanceCapabilities(t, got)
}

func assertRegisteredInstanceIdentity(t *testing.T, got contract.ToolInstance, lease dto.LeaseKey) {
	t.Helper()
	if got.Lease != lease {
		t.Fatalf("GetInstance().Lease = %#v, want %#v", got.Lease, lease)
	}
	if got.AgentID != "agent-get" {
		t.Fatalf("GetInstance().AgentID = %q, want %q", got.AgentID, "agent-get")
	}
	if got.ThreadID != "thread-get" {
		t.Fatalf("GetInstance().ThreadID = %q, want %q", got.ThreadID, "thread-get")
	}
	if got.PID != 4321 {
		t.Fatalf("GetInstance().PID = %d, want 4321", got.PID)
	}
}

func assertRegisteredInstanceKinds(t *testing.T, got contract.ToolInstance) {
	t.Helper()
	if got.ClientKind != dto.ClientKindOrch {
		t.Fatalf("GetInstance().ClientKind = %q, want %q", got.ClientKind, dto.ClientKindOrch)
	}
	if got.PeerKind != dto.PeerKindSharedService {
		t.Fatalf("GetInstance().PeerKind = %q, want %q", got.PeerKind, dto.PeerKindSharedService)
	}
	if got.Status != dto.StatusActive {
		t.Fatalf("GetInstance().Status = %q, want %q", got.Status, dto.StatusActive)
	}
	if got.ConfigVersion != 1 {
		t.Fatalf("GetInstance().ConfigVersion = %d, want 1", got.ConfigVersion)
	}
}

func assertRegisteredInstanceCapabilities(t *testing.T, got contract.ToolInstance) {
	t.Helper()
	if !slices.Equal(got.Capabilities, []string{"hooks", "reports"}) {
		t.Fatalf("GetInstance().Capabilities = %#v, want %#v", got.Capabilities, []string{"hooks", "reports"})
	}
	if !slices.Equal(got.Subscriptions, []string{"config/agent", "config/thread"}) {
		t.Fatalf("GetInstance().Subscriptions = %#v, want %#v", got.Subscriptions, []string{"config/agent", "config/thread"})
	}
}

func TestNotifyBySubscription_Success(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-sub", Generation: 1}
	var gotMethod string
	var gotParams map[string]any
	addIndexedInstance(registry, &ToolInstance{
		Lease:         lease,
		Subscriptions: []string{"topic.sub"},
		Peer: &stubCallbackPeer{
			notifyFn: func(_ context.Context, method string, params any) error {
				gotMethod = method
				typed, ok := params.(map[string]any)
				if !ok {
					return fmt.Errorf("params type = %T, want map[string]any", params)
				}
				gotParams = typed
				return nil
			},
		},
	})

	err := registry.NotifyBySubscription(context.Background(), "topic.sub", "notify/sub", map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("NotifyBySubscription() error = %v", err)
	}
	if gotMethod != "notify/sub" {
		t.Fatalf("NotifyBySubscription() method = %q, want %q", gotMethod, "notify/sub")
	}
	if got := gotParams["ok"]; got != true {
		t.Fatalf("NotifyBySubscription() params = %#v, want ok=true", gotParams)
	}
}

func TestNotifyByCapability_Success(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-cap", Generation: 1}
	var gotMethod string
	var gotParams map[string]any
	addIndexedInstance(registry, &ToolInstance{
		Lease:        lease,
		Capabilities: []string{"cap.exec"},
		Peer: &stubCallbackPeer{
			notifyFn: func(_ context.Context, method string, params any) error {
				gotMethod = method
				typed, ok := params.(map[string]any)
				if !ok {
					return fmt.Errorf("params type = %T, want map[string]any", params)
				}
				gotParams = typed
				return nil
			},
		},
	})

	err := registry.NotifyByCapability(context.Background(), "cap.exec", "notify/cap", map[string]any{"kind": "capability"})
	if err != nil {
		t.Fatalf("NotifyByCapability() error = %v", err)
	}
	if gotMethod != "notify/cap" {
		t.Fatalf("NotifyByCapability() method = %q, want %q", gotMethod, "notify/cap")
	}
	if got := gotParams["kind"]; got != "capability" {
		t.Fatalf("NotifyByCapability() params = %#v, want kind=capability", gotParams)
	}
}

func TestNotifyBySelector_Success(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	lease := dto.LeaseKey{InstanceID: "instance-sel", Generation: 1}
	var gotMethod string
	var gotParams map[string]any
	addIndexedInstance(registry, &ToolInstance{
		Lease:         lease,
		AgentID:       "agent-sel",
		ThreadID:      "thread-sel",
		ClientKind:    dto.ClientKindOrch,
		Subscriptions: []string{"topic.sel"},
		Peer: &stubCallbackPeer{
			notifyFn: func(_ context.Context, method string, params any) error {
				gotMethod = method
				typed, ok := params.(map[string]any)
				if !ok {
					return fmt.Errorf("params type = %T, want map[string]any", params)
				}
				gotParams = typed
				return nil
			},
		},
	})

	err := registry.NotifyBySelector(context.Background(), dto.Selector{
		Subscription: "topic.sel",
		Scope: &dto.SelectorScope{
			AgentID:    "agent-sel",
			ClientKind: dto.ClientKindOrch,
			InstanceID: "instance-sel",
		},
	}, "notify/selector", map[string]any{"kind": "selector"})
	if err != nil {
		t.Fatalf("NotifyBySelector() error = %v", err)
	}
	if gotMethod != "notify/selector" {
		t.Fatalf("NotifyBySelector() method = %q, want %q", gotMethod, "notify/selector")
	}
	if got := gotParams["kind"]; got != "selector" {
		t.Fatalf("NotifyBySelector() params = %#v, want kind=selector", gotParams)
	}
}
