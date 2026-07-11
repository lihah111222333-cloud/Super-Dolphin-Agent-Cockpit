package mcpcontrol

import (
	"context"
	"errors"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// setHookLifecycle 在注册表启动时注入 hook 生命周期管理器，nil 注册表直接忽略以兼容测试组装。
func (r *ToolRegistry) setHookLifecycle(hookLifecycle contract.HookLifecycle) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.hookLifecycle = hookLifecycle
	r.mu.Unlock()
}

// currentConfigVersionLocked 返回当前配置版本；调用方必须已持有注册表锁。
func (r *ToolRegistry) currentConfigVersionLocked() int64 {
	if r == nil || r.configVersion < 1 {
		return 1
	}
	return r.configVersion
}

// advanceConfigVersion 递增配置版本并同步到所有已注册实例，用于配置变更广播前的版本推进。
func (r *ToolRegistry) advanceConfigVersion() int64 {
	if r == nil {
		return 1
	}
	r.mu.Lock()
	if r.configVersion < 1 {
		r.configVersion = 1
	}
	r.configVersion++
	next := r.configVersion
	for _, instance := range r.instances {
		if instance != nil {
			instance.ConfigVersion = next
		}
	}
	r.mu.Unlock()
	return next
}

// shutdownHooks 调用租约关联的 hook 生命周期清理；缺少生命周期管理器时视为无需清理。
func (r *ToolRegistry) shutdownHooks(ctx context.Context, key LeaseKey) error {
	if r == nil {
		return nil
	}
	if key == (LeaseKey{}) {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.RLock()
	hookLifecycle := r.hookLifecycle
	r.mu.RUnlock()
	if hookLifecycle == nil {
		return nil
	}
	return hookLifecycle.ShutdownHooks(ctx, key)
}

// cleanupLease 以调用方提供的上下文清理 hook，错误只记录日志以免遮蔽连接关闭主流程。
func (r *ToolRegistry) cleanupLease(ctx context.Context, key LeaseKey) {
	if err := r.shutdownHooks(ctx, key); err != nil {
		pkglogger.Warn("mcp lease hook cleanup failed", "instance_id", key.InstanceID, "generation", key.Generation, "err", err)
	}
}

// activeLeaseKeys 返回 active 租约快照并稳定排序，供 shutdown 时锁外逐个清理。
func (r *ToolRegistry) activeLeaseKeys() []LeaseKey {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	leases := make([]LeaseKey, 0, len(r.instances))
	for key, instance := range r.instances {
		if instance == nil || instance.Status != dto.StatusActive {
			continue
		}
		leases = append(leases, key)
	}
	sort.Slice(leases, func(i, j int) bool {
		if leases[i].InstanceID != leases[j].InstanceID {
			return leases[i].InstanceID < leases[j].InstanceID
		}
		return leases[i].Generation < leases[j].Generation
	})
	return leases
}

// shutdownActiveLeases 逐个清理当前 active 租约，所有清理错误会合并返回给 fx OnStop。
func (r *ToolRegistry) shutdownActiveLeases(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var joined error
	for _, key := range r.activeLeaseKeys() {
		joined = errors.Join(joined, r.cleanupLeaseWithTimeout(ctx, key))
	}
	return joined
}

// cleanupLeaseWithTimeout 为单个租约创建默认清理超时，避免 OnStop 被异常 hook 长时间阻塞。
func (r *ToolRegistry) cleanupLeaseWithTimeout(parent context.Context, key LeaseKey) error {
	ctx, cancel := withTimeoutContext(parent, defaultCleanupTimeout)
	defer cancel()
	err := r.shutdownHooks(ctx, key)
	if err != nil {
		pkglogger.Warn("mcp lease hook cleanup failed", "instance_id", key.InstanceID, "generation", key.Generation, "err", err)
	}
	return err
}

// lookupInstance 读取允许 stale 的实例快照，供诊断和只读查询路径使用。
func (r *ToolRegistry) lookupInstance(key dto.LeaseKey) (*ToolInstance, bool) {
	instance, err := lookupLease(leaseLookupOptions{
		registry:   r,
		key:        key,
		allowStale: true,
	})
	if err != nil {
		return nil, false
	}
	return instance, true
}
