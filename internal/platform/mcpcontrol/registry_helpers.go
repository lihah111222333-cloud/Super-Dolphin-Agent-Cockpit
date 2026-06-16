package mcpcontrol

import (
	"context"
	"errors"
	"sort"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func (r *ToolRegistry) setHookLifecycle(hookLifecycle contract.HookLifecycle) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.hookLifecycle = hookLifecycle
	r.mu.Unlock()
}

func (r *ToolRegistry) currentConfigVersionLocked() int64 {
	if r == nil || r.configVersion < 1 {
		return 1
	}
	return r.configVersion
}

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

func (r *ToolRegistry) cleanupLease(ctx context.Context, key LeaseKey) {
	if err := r.shutdownHooks(ctx, key); err != nil {
		pkglogger.Warn("mcp lease hook cleanup failed", "instance_id", key.InstanceID, "generation", key.Generation, "err", err)
	}
}

// activeLeaseKeys 处理active租约键。
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

func (r *ToolRegistry) cleanupLeaseWithTimeout(parent context.Context, key LeaseKey) error {
	ctx, cancel := withTimeoutContext(parent, defaultCleanupTimeout)
	defer cancel()
	err := r.shutdownHooks(ctx, key)
	if err != nil {
		pkglogger.Warn("mcp lease hook cleanup failed", "instance_id", key.InstanceID, "generation", key.Generation, "err", err)
	}
	return err
}

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
