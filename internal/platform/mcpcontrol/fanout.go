package mcpcontrol

import (
	"context"
	"fmt"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type sendTarget struct {
	key  LeaseKey
	peer Peer
}

type selectorBucket struct {
	leases map[LeaseKey]struct{}
}

// IntersectTargets returns active peers that satisfy every populated selector dimension.
// It walks the smallest bucket first and checks the remaining buckets with O(1) membership lookups.
// scope.agent_id only matches instances that explicitly set AgentID; bootstrap shared-service peers with AgentID="" are excluded.
// IntersectTargets 处理intersecttargets。
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

// snapshotTargets 处理快照targets。
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

func (r *ToolRegistry) activeTargetLocked(key LeaseKey) (sendTarget, bool) {
	instance := r.instances[key]
	if instance == nil || instance.Peer == nil || instance.Status != dto.StatusActive {
		return sendTarget{}, false
	}
	return sendTarget{key: key, peer: instance.Peer}, true
}

func selectorIndexBucket(key string, index map[string]map[LeaseKey]struct{}) (map[LeaseKey]struct{}, bool) {
	if key == "" {
		return nil, true
	}
	leases := index[key]
	return leases, len(leases) != 0
}

func smallestSelectorBucket(buckets []selectorBucket) selectorBucket {
	smallest := buckets[0]
	for _, bucket := range buckets[1:] {
		if len(bucket.leases) < len(smallest.leases) {
			smallest = bucket
		}
	}
	return smallest
}

func matchesSelectorBuckets(key LeaseKey, buckets []selectorBucket) bool {
	for _, bucket := range buckets {
		if _, ok := bucket.leases[key]; !ok {
			return false
		}
	}
	return true
}

func (r *ToolRegistry) notifyTargets(ctx context.Context, targets []sendTarget, method string, params any) error {
	return r.fanoutTargets(ctx, targets, method, fanoutOperation{
		name: "notify",
		invoke: func(ctx context.Context, peer Peer) error {
			return peer.Notify(ctx, method, params)
		},
	})
}

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

func (r *ToolRegistry) resetPeerFailure(key LeaseKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if instance := r.instances[key]; instance != nil {
		instance.ConsecutiveFailures = 0
	}
}
