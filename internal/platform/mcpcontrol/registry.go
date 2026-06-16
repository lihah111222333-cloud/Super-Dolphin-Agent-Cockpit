package mcpcontrol

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

const (
	defaultHeartbeatInterval    = 10 * time.Second
	defaultNotifyTimeout        = 2 * time.Second
	defaultCleanupTimeout       = 3 * time.Second
	defaultFanoutParallelism    = 8
	defaultPeerFailureThreshold = 3
	controlPlaneProtocolVersion = dto.ProtocolVersion
)

// LeaseKey identifies a registered MCP tool lease.
type LeaseKey = dto.LeaseKey

// ToolInstance captures the metadata and live peer for a registered MCP tool lease.
type ToolInstance struct {
	Lease LeaseKey
	// Deprecated: use LeaseKey. Will be removed after 2026-06-30.
	LeaseID             string
	BinaryName          string
	AgentID             string
	ThreadID            string
	PID                 int
	Capabilities        []string
	Subscriptions       []string
	PeerKind            string
	ClientKind          string
	Shared              bool
	ConnectedAt         time.Time
	RegisteredAt        time.Time
	LastHeartbeat       time.Time
	Status              string
	ConfigVersion       int64
	ConsecutiveFailures int
	Peer                Peer
}

// Peer represents the live RPC connection back to a registered MCP tool.
type Peer interface {
	Notify(ctx context.Context, method string, params any) error
	Callback(ctx context.Context, method string, params any, result any) error
	Close() error
}

// ToolRegistry manages registered MCP tool instances, their lease indexes, and control-plane fanout.
type ToolRegistry struct {
	mu               sync.RWMutex
	instances        map[LeaseKey]*ToolInstance
	bySubscription   map[string]map[LeaseKey]struct{}
	byCapability     map[string]map[LeaseKey]struct{}
	byAgent          map[string]map[LeaseKey]struct{}
	byThread         map[string]map[LeaseKey]struct{}
	byClientKind     map[string]map[LeaseKey]struct{}
	byInstance       map[string]map[LeaseKey]struct{}
	byPeerKind       map[string]map[LeaseKey]struct{}
	latestByInstance map[string]LeaseKey
	reportReceipts   map[LeaseKey]map[string]reportReceipt
	configVersion    int64

	heartbeatInterval    time.Duration
	notifyTimeout        time.Duration
	fanoutParallelism    int
	peerFailureThreshold int
	hookLifecycle        contract.HookLifecycle
}

// RegistryOptions configures heartbeat, notification, and peer failure behavior for a ToolRegistry.
type RegistryOptions struct {
	HeartbeatInterval    time.Duration
	NotifyTimeout        time.Duration
	FanoutParallelism    int
	PeerFailureThreshold int
}

var (
	_ contract.ToolRegistry     = (*ToolRegistry)(nil)
	_ contract.ToolNotifier     = (*ToolRegistry)(nil)
	_ contract.ToolHookCallback = (*ToolRegistry)(nil)
	_ contract.PeerCallback     = (*ToolRegistry)(nil)
	_ contract.ToolControlPlane = (*ToolRegistry)(nil)
)

// NewRegistry 创建注册表。
func NewRegistry() *ToolRegistry {
	return NewToolRegistry(RegistryOptions{})
}

// NewToolRegistry 创建工具注册表。
func NewToolRegistry(opts RegistryOptions) *ToolRegistry {
	return &ToolRegistry{
		instances:            make(map[LeaseKey]*ToolInstance),
		bySubscription:       make(map[string]map[LeaseKey]struct{}),
		byCapability:         make(map[string]map[LeaseKey]struct{}),
		byAgent:              make(map[string]map[LeaseKey]struct{}),
		byThread:             make(map[string]map[LeaseKey]struct{}),
		byClientKind:         make(map[string]map[LeaseKey]struct{}),
		byInstance:           make(map[string]map[LeaseKey]struct{}),
		byPeerKind:           make(map[string]map[LeaseKey]struct{}),
		latestByInstance:     make(map[string]LeaseKey),
		reportReceipts:       make(map[LeaseKey]map[string]reportReceipt),
		configVersion:        1,
		heartbeatInterval:    durationOrDefault(opts.HeartbeatInterval, defaultHeartbeatInterval),
		notifyTimeout:        durationOrDefault(opts.NotifyTimeout, defaultNotifyTimeout),
		fanoutParallelism:    intOrDefault(opts.FanoutParallelism, defaultFanoutParallelism),
		peerFailureThreshold: intOrDefault(opts.PeerFailureThreshold, defaultPeerFailureThreshold),
	}
}

// Register 注册平台mcpcontrol。
func (r *ToolRegistry) Register(ctx context.Context, req dto.RegisterRequest) (dto.RegisterResponse, error) {
	normalized, err := normalizeRegisterRequest(req)
	if err != nil {
		return dto.RegisterResponse{}, err
	}
	peer, err := peerFromContext(ctx)
	if err != nil {
		return dto.RegisterResponse{}, err
	}

	now := time.Now()
	lease := LeaseKey{InstanceID: normalized.InstanceID, Generation: 1}
	instance := &ToolInstance{
		Lease:         lease,
		LeaseID:       platformshared.NewID("mcp_lease"), // Deprecated: use LeaseKey. Will be removed after 2026-06-30.
		BinaryName:    normalized.BinaryName,
		AgentID:       normalized.AgentID,
		ThreadID:      normalized.ThreadID,
		PID:           normalized.PID,
		Capabilities:  platformshared.CloneStrings(normalized.CapabilitiesOffered),
		Subscriptions: platformshared.CloneStrings(normalized.Subscriptions),
		PeerKind:      normalized.PeerKind,
		ClientKind:    normalized.ClientKind,
		Shared:        normalized.Shared,
		ConnectedAt:   now,
		RegisteredAt:  now,
		LastHeartbeat: now,
		Status:        dto.StatusActive,
		Peer:          peer,
	}

	var previous Peer
	var replaced LeaseKey
	r.mu.Lock()
	if latest, ok := r.latestByInstance[instance.Lease.InstanceID]; ok {
		if current := r.instances[latest]; current != nil {
			instance.Lease.Generation = current.Lease.Generation + 1
		}
		previous = r.evictLocked(latest)
		replaced = latest
	}
	instance.ConfigVersion = r.currentConfigVersionLocked()
	r.instances[instance.Lease] = instance
	r.latestByInstance[instance.Lease.InstanceID] = instance.Lease
	r.indexLocked(instance)
	r.mu.Unlock()

	_ = r.disconnectLease(replaced, disconnectLeaseOptions{
		ctx:  ctx,
		peer: previous,
	})
	return dto.RegisterResponse{
		InstanceID:             instance.Lease.InstanceID,
		Generation:             instance.Lease.Generation,
		AcceptedGeneration:     instance.Lease.Generation,
		PeerKind:               instance.PeerKind,
		CapabilitiesNegotiated: platformshared.CloneStrings(instance.Capabilities),
		CapabilitiesRejected:   []string{},
		HeartbeatIntervalMs:    int(r.heartbeatInterval / time.Millisecond),
		HeartbeatTimeoutMs:     int(defaultHeartbeatTTL / time.Millisecond),
		SendTimeoutMs:          int(r.notifyTimeout / time.Millisecond),
		SweeperIntervalMs:      int(defaultSweepTick / time.Millisecond),
		ServerProtocolVersion:  controlPlaneProtocolVersion,
		ConfigVersion:          instance.ConfigVersion,
	}, nil
}

// Heartbeat 刷新锁或租约的存活时间。
func (r *ToolRegistry) Heartbeat(ctx context.Context, req dto.HeartbeatRequest) (dto.HeartbeatResponse, error) {
	key, err := normalizeLeaseKey(dto.LeaseKey{InstanceID: req.InstanceID, Generation: req.Generation})
	if err != nil {
		return dto.HeartbeatResponse{}, err
	}
	now := time.Now()
	status := req.Status

	r.mu.Lock()
	instance := r.instances[key]
	if instance == nil {
		r.mu.Unlock()
		return dto.HeartbeatResponse{}, errLeaseNotFound("mcp lease %s/%d not found", key.InstanceID, key.Generation)
	}
	if status == dto.StatusDisconnected {
		instance.Status = dto.StatusDisconnected
		peer := r.evictLocked(key)
		r.mu.Unlock()
		_ = r.disconnectLease(key, disconnectLeaseOptions{
			ctx:  ctx,
			peer: peer,
		})
		return dto.HeartbeatResponse{
			OK:              true,
			ServerTime:      now.UnixMilli(),
			ConfigVersion:   instance.ConfigVersion,
			NextHeartbeatMs: int(r.heartbeatInterval / time.Millisecond),
		}, nil
	}
	instance.LastHeartbeat = now
	instance.ConsecutiveFailures = 0
	if instance.Status != dto.StatusDisconnected {
		instance.Status = dto.StatusActive
	}
	r.mu.Unlock()
	return dto.HeartbeatResponse{
		OK:              true,
		ServerTime:      now.UnixMilli(),
		ConfigVersion:   instance.ConfigVersion,
		NextHeartbeatMs: int(r.heartbeatInterval / time.Millisecond),
	}, nil
}

// GetInstance 读取instance。
func (r *ToolRegistry) GetInstance(key dto.LeaseKey) (contract.ToolInstance, bool) {
	internal, ok := r.lookupInstance(key)
	if !ok {
		return contract.ToolInstance{}, false
	}
	return toContractInstance(internal), true
}

// ListInstances 列出instances。
func (r *ToolRegistry) ListInstances() []contract.ToolInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]contract.ToolInstance, 0, len(r.instances))
	for _, instance := range r.instances {
		if instance != nil {
			items = append(items, toContractInstance(instance))
		}
	}
	return items
}

// NotifyBySubscription 按subscription处理notify。
func (r *ToolRegistry) NotifyBySubscription(ctx context.Context, topic, method string, params any) error {
	return r.notifyTargets(ctx, r.snapshotTargets(r.bySubscription, topic), method, params)
}

// NotifyByCapability 按capability处理notify。
func (r *ToolRegistry) NotifyByCapability(ctx context.Context, capability, method string, params any) error {
	return r.notifyTargets(ctx, r.snapshotTargets(r.byCapability, capability), method, params)
}

// ShutdownInstance 处理shutdowninstance。
func (r *ToolRegistry) ShutdownInstance(ctx context.Context, key dto.LeaseKey, req dto.ShutdownRequest) error {
	instance, err := r.resolveLease(key, key, true)
	if err != nil {
		return err
	}
	cleanupErr := r.shutdownHooks(ctx, key)
	if instance.Peer == nil {
		peerErr := errPeerUnavailable("mcp peer %s/%d is not available", key.InstanceID, key.Generation)
		if cleanupErr != nil {
			return errors.Join(peerErr, cleanupErr)
		}
		return peerErr
	}
	req.InstanceID = key.InstanceID
	req.Generation = key.Generation
	callCtx, cancel := withTimeoutContext(ctx, r.notifyTimeout)
	defer cancel()

	var resp map[string]any
	err = instance.Peer.Callback(callCtx, dto.MethodShutdown, req, &resp)
	if err != nil {
		peer, evicted := r.notePeerFailure(key)
		if evicted {
			_ = r.disconnectLease(key, disconnectLeaseOptions{
				ctx:  ctx,
				peer: peer,
			})
		} else {
			closePeer(peer)
		}
		peerErr := errPeerUnavailable("mcp shutdown callback failed for %s/%d: %v", key.InstanceID, key.Generation, err)
		if cleanupErr != nil {
			return errors.Join(peerErr, cleanupErr)
		}
		return peerErr
	}
	r.resetPeerFailure(key)
	return cleanupErr
}

// OnDisconnect 处理ondisconnect。
func (r *ToolRegistry) OnDisconnect(key LeaseKey) {
	r.mu.Lock()
	instance := r.instances[key]
	if instance != nil {
		instance.Status = dto.StatusDisconnected
	}
	peer := r.evictLocked(key)
	r.mu.Unlock()
	_ = r.disconnectLease(key, disconnectLeaseOptions{
		peer:    peer,
		timeout: true,
	})
}
