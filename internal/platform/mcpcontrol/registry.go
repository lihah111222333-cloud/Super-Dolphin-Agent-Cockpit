package mcpcontrol

import (
	"context"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultHeartbeatInterval    = 10 * time.Second
	defaultNotifyTimeout        = 2 * time.Second
	defaultFanoutParallelism    = 8
	defaultPeerFailureThreshold = 3
)

type LeaseKey = dto.LeaseKey

type ToolInstance struct {
	Lease               LeaseKey
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
	ConnectedAt         time.Time
	RegisteredAt        time.Time
	LastHeartbeat       time.Time
	Status              string
	ConfigVersion       int64
	ConsecutiveFailures int
	Peer                Peer
}

type Peer interface {
	Notify(ctx context.Context, method string, params any) error
	Callback(ctx context.Context, method string, params any, result any) error
	Close() error
}

type ToolRegistry struct {
	mu               sync.RWMutex
	instances        map[LeaseKey]*ToolInstance
	bySubscription   map[string]map[LeaseKey]struct{}
	byCapability     map[string]map[LeaseKey]struct{}
	byAgent          map[string]map[LeaseKey]struct{}
	byPeerKind       map[string]map[LeaseKey]struct{}
	latestByInstance map[string]LeaseKey
	reportReceipts   map[LeaseKey]map[string]reportReceipt

	heartbeatInterval    time.Duration
	notifyTimeout        time.Duration
	fanoutParallelism    int
	peerFailureThreshold int
}

type RegistryOptions struct {
	HeartbeatInterval    time.Duration
	NotifyTimeout        time.Duration
	FanoutParallelism    int
	PeerFailureThreshold int
}

var _ contract.ToolRegistry = (*ToolRegistry)(nil)

func NewRegistry() *ToolRegistry {
	return NewToolRegistry(RegistryOptions{})
}

func NewToolRegistry(opts RegistryOptions) *ToolRegistry {
	return &ToolRegistry{
		instances:            make(map[LeaseKey]*ToolInstance),
		bySubscription:       make(map[string]map[LeaseKey]struct{}),
		byCapability:         make(map[string]map[LeaseKey]struct{}),
		byAgent:              make(map[string]map[LeaseKey]struct{}),
		byPeerKind:           make(map[string]map[LeaseKey]struct{}),
		latestByInstance:     make(map[string]LeaseKey),
		reportReceipts:       make(map[LeaseKey]map[string]reportReceipt),
		heartbeatInterval:    durationOrDefault(opts.HeartbeatInterval, defaultHeartbeatInterval),
		notifyTimeout:        durationOrDefault(opts.NotifyTimeout, defaultNotifyTimeout),
		fanoutParallelism:    intOrDefault(opts.FanoutParallelism, defaultFanoutParallelism),
		peerFailureThreshold: intOrDefault(opts.PeerFailureThreshold, defaultPeerFailureThreshold),
	}
}

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
		Capabilities:  cloneStrings(normalized.CapabilitiesOffered),
		Subscriptions: cloneStrings(normalized.Subscriptions),
		PeerKind:      normalized.PeerKind,
		ClientKind:    normalized.ClientKind,
		ConnectedAt:   now,
		RegisteredAt:  now,
		LastHeartbeat: now,
		Status:        dto.StatusActive,
		ConfigVersion: 1,
		Peer:          peer,
	}

	var previous Peer
	r.mu.Lock()
	if latest, ok := r.latestByInstance[instance.Lease.InstanceID]; ok {
		if current := r.instances[latest]; current != nil {
			instance.Lease.Generation = current.Lease.Generation + 1
		}
		previous = r.evictLocked(latest)
	}
	r.instances[instance.Lease] = instance
	r.latestByInstance[instance.Lease.InstanceID] = instance.Lease
	r.indexLocked(instance)
	r.mu.Unlock()

	closePeer(previous)
	return dto.RegisterResponse{
		Lease:                  instance.Lease,
		PeerKind:               instance.PeerKind,
		CapabilitiesNegotiated: cloneStrings(instance.Capabilities),
		HeartbeatIntervalMs:    int(r.heartbeatInterval / time.Millisecond),
		HeartbeatTimeoutMs:     int(defaultHeartbeatTTL / time.Millisecond),
		SendTimeoutMs:          int(r.notifyTimeout / time.Millisecond),
		SweeperIntervalMs:      int(defaultSweepTick / time.Millisecond),
		ConfigVersion:          instance.ConfigVersion,
	}, nil
}

func (r *ToolRegistry) Heartbeat(_ context.Context, req dto.HeartbeatRequest) (dto.HeartbeatResponse, error) {
	key, err := normalizeLeaseKey(req.Lease)
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
		closePeer(peer)
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

func (r *ToolRegistry) GetInstance(key dto.LeaseKey) (contract.ToolInstance, bool) {
	internal, ok := r.lookupInstance(key)
	if !ok {
		return contract.ToolInstance{}, false
	}
	return toContractInstance(internal), true
}

func (r *ToolRegistry) NotifyBySubscription(ctx context.Context, topic, method string, params any) error {
	return r.notifyTargets(ctx, r.snapshotTargets(r.bySubscription, topic), method, params)
}

func (r *ToolRegistry) NotifyByCapability(ctx context.Context, capability, method string, params any) error {
	return r.notifyTargets(ctx, r.snapshotTargets(r.byCapability, capability), method, params)
}

func (r *ToolRegistry) ShutdownInstance(ctx context.Context, key dto.LeaseKey, req dto.ShutdownRequest) error {
	instance, err := r.resolveLease(key, key, true)
	if err != nil {
		return err
	}
	if instance.Peer == nil {
		return errPeerUnavailable("mcp peer %s/%d is not available", key.InstanceID, key.Generation)
	}
	req.Lease = key
	callCtx, cancel := withTimeoutContext(ctx, r.notifyTimeout)
	defer cancel()

	var resp map[string]any
	err = instance.Peer.Callback(callCtx, dto.MethodShutdown, req, &resp)
	if err != nil {
		closePeer(r.notePeerFailure(key))
		return errPeerUnavailable("mcp shutdown callback failed for %s/%d: %v", key.InstanceID, key.Generation, err)
	}
	r.resetPeerFailure(key)
	return nil
}

func (r *ToolRegistry) OnDisconnect(key LeaseKey) {
	r.mu.Lock()
	instance := r.instances[key]
	if instance != nil {
		instance.Status = dto.StatusDisconnected
	}
	peer := r.evictLocked(key)
	r.mu.Unlock()
	closePeer(peer)
}

func (r *ToolRegistry) lookupInstance(key dto.LeaseKey) (*ToolInstance, bool) {
	normalized, err := normalizeLeaseKey(key)
	if err != nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	instance := r.instances[normalized]
	if instance == nil {
		return nil, false
	}
	return cloneInstance(instance), true
}
