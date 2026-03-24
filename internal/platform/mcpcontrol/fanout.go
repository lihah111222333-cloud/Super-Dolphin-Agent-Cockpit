package mcpcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
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
	scope := normalizeSelectorScope(sel.Scope)
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

func normalizeSelectorScope(scope *dto.SelectorScope) dto.SelectorScope {
	if scope == nil {
		return dto.SelectorScope{}
	}
	return dto.SelectorScope{
		AgentID:    strings.TrimSpace(scope.AgentID),
		ThreadID:   strings.TrimSpace(scope.ThreadID),
		ClientKind: strings.TrimSpace(scope.ClientKind),
		InstanceID: strings.TrimSpace(scope.InstanceID),
	}
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
	method = strings.TrimSpace(method)
	if method == "" {
		return errInvalidParams("mcp notify method is required")
	}
	if len(targets) == 0 {
		return nil
	}

	workers := min(r.fanoutParallelism, len(targets))
	jobs := make(chan sendTarget, len(targets))
	errs := make(chan error, len(targets))
	for i := 0; i < workers; i++ {
		go r.runNotifyWorker(ctx, jobs, errs, method, params)
	}
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)

	var joined error
	for range targets {
		joined = errors.Join(joined, <-errs)
	}
	return joined
}

func (r *ToolRegistry) runNotifyWorker(ctx context.Context, jobs <-chan sendTarget, errs chan<- error, method string, params any) {
	for target := range jobs {
		errs <- r.notifyTarget(ctx, target, method, params)
	}
}

func (r *ToolRegistry) notifyTarget(ctx context.Context, target sendTarget, method string, params any) error {
	callCtx, cancel := withTimeoutContext(ctx, r.notifyTimeout)
	defer cancel()
	if err := target.peer.Notify(callCtx, method, params); err != nil {
		peer, evicted := r.notePeerFailure(target.key)
		if evicted {
			r.cleanupLease(context.Background(), target.key)
		}
		closePeer(peer)
		return fmt.Errorf("%s/%d: %w", target.key.InstanceID, target.key.Generation, err)
	}
	r.resetPeerFailure(target.key)
	return nil
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
