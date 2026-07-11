package mcpcontrol

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	// 注册表默认超时和并发参数，需与 sweeper/notify 的控制面行为保持一致。
	defaultHeartbeatInterval    = 10 * time.Second
	defaultNotifyTimeout        = 2 * time.Second
	defaultCleanupTimeout       = 3 * time.Second
	defaultFanoutParallelism    = 8
	defaultPeerFailureThreshold = 3
	controlPlaneProtocolVersion = dto.ProtocolVersion
)

// LeaseKey 标识一次 MCP 工具注册租约；Generation 用来区分同一实例的重连代际。
type LeaseKey = dto.LeaseKey

// ToolInstance 保存已注册 MCP peer 的元数据和活动连接；写入只能在 ToolRegistry 锁内完成。
type ToolInstance struct {
	Lease LeaseKey // 当前租约代际。
	// Deprecated: 请改用 LeaseKey；兼容旧字段到 2026-06-30 后可移除。
	LeaseID             string    // 旧版租约 ID，仅兼容历史调用方。
	BinaryName          string    // 注册进程名。
	AgentID             string    // 绑定的 agent，shared-service 可为空。
	ThreadID            string    // 绑定的 thread，agent 级服务可为空。
	PID                 int       // peer 进程号，用于日志和诊断。
	Capabilities        []string  // 已接受能力集合。
	Subscriptions       []string  // 配置/事件订阅 topic。
	PeerKind            string    // tool、ui 或 shared-service。
	ClientKind          string    // orch、lsp、ida 或 custom。
	Shared              bool      // 是否可跨 agent 复用。
	ConnectedAt         time.Time // 当前连接建立时间。
	RegisteredAt        time.Time // 本代租约注册时间。
	LastHeartbeat       time.Time // 最近 heartbeat 时间。
	Status              string    // active、stale 或 disconnected。
	ConfigVersion       int64     // peer 应观察的配置版本。
	ConsecutiveFailures int       // notify/callback 连续失败次数。
	Peer                Peer      // 当前 jrpc2 连接封装。
}

// Peer 抽象已注册 MCP 工具的反向 RPC 连接，注册表通过它发送通知和回调。
type Peer interface {
	Notify(ctx context.Context, method string, params any) error
	Callback(ctx context.Context, method string, params any, result any) error
	Close() error
}

// ToolRegistry 管理 MCP peer 租约、索引和控制面 fanout；所有 map 写入都受 mu 保护。
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

// RegistryOptions 配置 heartbeat、通知超时和连续失败驱逐阈值，零值使用控制面默认值。
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

// NewRegistry 使用默认选项创建控制面注册表。
func NewRegistry() *ToolRegistry {
	return NewToolRegistry(RegistryOptions{})
}

// NewToolRegistry 初始化所有租约索引，configVersion 从 1 开始以便 peer 首次注册即可对齐版本。
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

// Register 绑定当前 jrpc2 peer 并分配新租约代际；同 instance 重连会先驱逐上一代 peer。
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
		LeaseID:       platformshared.NewID("mcp_lease"), // Deprecated: 为旧 contract 字段保留到 2026-06-30。
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

// Heartbeat 刷新租约存活时间；peer 主动声明 disconnected 时会立即移出索引并清理 hook。
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

// GetInstance 返回租约快照，调用方拿到的是 contract 视图，不能修改注册表内部状态。
func (r *ToolRegistry) GetInstance(key dto.LeaseKey) (contract.ToolInstance, bool) {
	internal, ok := r.lookupInstance(key)
	if !ok {
		return contract.ToolInstance{}, false
	}
	return toContractInstance(internal), true
}

// ListInstances 返回当前注册实例的 contract 快照列表，用于只读诊断和控制面查询。
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

// NotifyBySubscription 向订阅 topic 的 active peer 广播通知，发送前会先做目标快照。
func (r *ToolRegistry) NotifyBySubscription(ctx context.Context, topic, method string, params any) error {
	return r.notifyTargets(ctx, r.snapshotTargets(r.bySubscription, topic), method, params)
}

// NotifyByCapability 向声明指定能力的 active peer 广播通知，失败计数由 fanout 层统一处理。
func (r *ToolRegistry) NotifyByCapability(ctx context.Context, capability, method string, params any) error {
	return r.notifyTargets(ctx, r.snapshotTargets(r.byCapability, capability), method, params)
}

// ShutdownInstance 请求指定租约优雅关闭；无论 peer 回调是否成功都会尝试清理 hook 生命周期。
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

// OnDisconnect 标记租约断开并异步安全清理 peer，供底层连接关闭回调调用。
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
