package mcpcontrol

import (
	"context"
	"fmt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// sendTarget 是一次 fanout 调用的最小目标，保留租约键用于失败计数和驱逐。
type sendTarget struct {
	key  LeaseKey
	peer Peer
}

// selectorBucket 保存某个 selector 维度命中的租约集合，交集计算会优先遍历最小桶。
type selectorBucket struct {
	leases map[LeaseKey]struct{}
}

// IntersectTargets 返回同时满足所有非空 selector 维度的 active peer。
// 它先选最小索引桶再做 O(1) 交集检查；scope.agent_id 只匹配显式绑定的实例。
func (r *ToolRegistry) IntersectTargets(sel dto.Selector) []sendTarget {
	r.mu.RLock()
	defer r.mu.RUnlock()

	buckets, ok := r.selectorBucketsLocked(sel)
	if !ok {
		return nil
	}
	if len(buckets) == 0 {
		return r.activeTargetsLocked()
	}
	return r.targetsFromBucketsLocked(buckets)
}

// selectorBucketsLocked 在持锁状态下把 selector 维度转换成索引桶；任一维度无命中即返回 false。
func (r *ToolRegistry) selectorBucketsLocked(sel dto.Selector) ([]selectorBucket, bool) {
	scope := shared.NormalizeSelectorScope(sel.Scope)
	specs := []struct {
		key   string
		index map[string]map[LeaseKey]struct{}
	}{
		{key: strings.TrimSpace(sel.Subscription), index: r.bySubscription},
		{key: strings.TrimSpace(sel.Capability), index: r.byCapability},
		{key: scope.AgentID, index: r.byAgent},
		{key: scope.ThreadID, index: r.byThread},
		{key: scope.ClientKind, index: r.byClientKind},
		{key: scope.InstanceID, index: r.byInstance},
	}
	buckets := make([]selectorBucket, 0, len(specs))
	for _, spec := range specs {
		leases, ok := selectorIndexBucket(spec.key, spec.index)
		if !ok {
			return nil, false
		}
		if leases != nil {
			buckets = append(buckets, selectorBucket{leases: leases})
		}
	}
	return buckets, true
}

// snapshotTargets 对单个索引桶生成 active 目标快照，避免后续 RPC 调用持有注册表锁。
func (r *ToolRegistry) snapshotTargets(index map[string]map[LeaseKey]struct{}, bucket string) []sendTarget {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := index[bucket]
	targets := make([]sendTarget, 0, len(keys))
	for key := range keys {
		instance := r.instances[key]
		if instance == nil || instance.Peer == nil || instance.Status != dto.StatusActive {
			continue
		}
		targets = append(targets, sendTarget{key: key, peer: instance.Peer})
	}
	return targets
}

// activeTargetsLocked 返回所有 active peer；调用方必须已持有读锁或写锁。
func (r *ToolRegistry) activeTargetsLocked() []sendTarget {
	targets := make([]sendTarget, 0, len(r.instances))
	for key := range r.instances {
		target, ok := r.activeTargetLocked(key)
		if ok {
			targets = append(targets, target)
		}
	}
	return targets
}

// targetsFromBucketsLocked 从最小 selector 桶起步取交集，减少多维广播的扫描成本。
func (r *ToolRegistry) targetsFromBucketsLocked(buckets []selectorBucket) []sendTarget {
	smallest := smallestSelectorBucket(buckets)
	targets := make([]sendTarget, 0, len(smallest.leases))
	for key := range smallest.leases {
		if !matchesSelectorBuckets(key, buckets) {
			continue
		}
		target, ok := r.activeTargetLocked(key)
		if ok {
			targets = append(targets, target)
		}
	}
	return targets
}

// activeTargetLocked 把租约键转换成可调用目标，非 active 或无 peer 的实例会被跳过。
func (r *ToolRegistry) activeTargetLocked(key LeaseKey) (sendTarget, bool) {
	instance := r.instances[key]
	if instance == nil || instance.Peer == nil || instance.Status != dto.StatusActive {
		return sendTarget{}, false
	}
	return sendTarget{key: key, peer: instance.Peer}, true
}

// selectorIndexBucket 读取单个 selector 维度；空维度表示不过滤，非空无命中表示整次查询失败。
func selectorIndexBucket(key string, index map[string]map[LeaseKey]struct{}) (map[LeaseKey]struct{}, bool) {
	if key == "" {
		return nil, true
	}
	leases := index[key]
	return leases, len(leases) != 0
}

// smallestSelectorBucket 选出候选最少的桶作为交集入口。
func smallestSelectorBucket(buckets []selectorBucket) selectorBucket {
	smallest := buckets[0]
	for _, bucket := range buckets[1:] {
		if len(bucket.leases) < len(smallest.leases) {
			smallest = bucket
		}
	}
	return smallest
}

// matchesSelectorBuckets 确认租约键存在于所有 selector 桶中。
func matchesSelectorBuckets(key LeaseKey, buckets []selectorBucket) bool {
	for _, bucket := range buckets {
		if _, ok := bucket.leases[key]; !ok {
			return false
		}
	}
	return true
}

// notifyTargets 把普通通知封装成通用 fanoutOperation，实际发送由 fanoutTargets 统一限时和计错。
func (r *ToolRegistry) notifyTargets(ctx context.Context, targets []sendTarget, method string, params any) error {
	return r.fanoutTargets(ctx, targets, method, fanoutOperation{
		name: "notify",
		invoke: func(ctx context.Context, peer Peer) error {
			return peer.Notify(ctx, method, params)
		},
	})
}

// recoverWorkerPanic 将目标 peer 的 panic 记为失败并按阈值驱逐，防止单个 peer 破坏整批广播。
func (r *ToolRegistry) recoverWorkerPanic(ctx context.Context, operation, method string, target sendTarget, rec any) error {
	err := fmt.Errorf("mcp %s worker panic for %s/%d method=%s: %v", operation, target.key.InstanceID, target.key.Generation, method, rec)
	peer, evicted := r.notePeerFailure(target.key)
	if evicted {
		_ = r.disconnectLease(target.key, disconnectLeaseOptions{
			ctx:     ctx,
			peer:    peer,
			timeout: true,
		})
	} else {
		closePeer(peer)
	}
	pkglogger.Error("mcp worker panic",
		"operation", operation,
		"method", method,
		"lease_key", target.key,
		"panic", rec,
		"error", err,
	)
	return err
}

// notePeerFailure 在写锁下累计 peer 连续失败，达到阈值时标记断开并移出索引。
func (r *ToolRegistry) notePeerFailure(key LeaseKey) (Peer, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	instance := r.instances[key]
	if instance == nil {
		return nil, false
	}
	instance.ConsecutiveFailures++
	if instance.ConsecutiveFailures < r.peerFailureThreshold {
		return nil, false
	}
	instance.Status = dto.StatusDisconnected
	return r.evictLocked(key), true
}

// resetPeerFailure 在成功调用后清零连续失败计数，避免一次短暂故障永久污染 peer 状态。
func (r *ToolRegistry) resetPeerFailure(key LeaseKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if instance := r.instances[key]; instance != nil {
		instance.ConsecutiveFailures = 0
	}
}
