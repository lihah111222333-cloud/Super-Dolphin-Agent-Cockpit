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
		closePeer(r.notePeerFailure(target.key))
		return fmt.Errorf("%s/%d: %w", target.key.InstanceID, target.key.Generation, err)
	}
	r.resetPeerFailure(target.key)
	return nil
}

func (r *ToolRegistry) notePeerFailure(key LeaseKey) Peer {
	r.mu.Lock()
	defer r.mu.Unlock()

	instance := r.instances[key]
	if instance == nil {
		return nil
	}
	instance.ConsecutiveFailures++
	if instance.ConsecutiveFailures < r.peerFailureThreshold {
		return nil
	}
	instance.Status = dto.StatusDisconnected
	return r.evictLocked(key)
}

func (r *ToolRegistry) resetPeerFailure(key LeaseKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if instance := r.instances[key]; instance != nil {
		instance.ConsecutiveFailures = 0
	}
}
