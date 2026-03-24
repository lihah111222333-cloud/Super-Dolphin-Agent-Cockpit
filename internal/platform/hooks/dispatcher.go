package hooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

const (
	defaultDispatcherParallelism = 8
	defaultPeerTimeout           = 2 * time.Second
	hookCallIDPrefix             = "hook"
)

var (
	errNilDispatcher   = errors.New("hooks dispatcher is nil")
	errNilHookRegistry = errors.New("hooks dispatcher registry is nil")
	errNilPeerCallback = errors.New("hooks peer callback is not configured")
)

// PeerCallback is the dispatcher-side callback bridge.
// mcpcontrol injects an implementation so hooks does not hold peer connections.
type PeerCallback interface {
	CallbackBefore(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error)
	CallbackCheck(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.CheckDecision, error)
	CallbackAfter(ctx context.Context, lease mcp.LeaseKey, payload mcp.HookPayload) (mcp.AfterDecision, error)
}

// HookDispatcher fans hook callbacks out to subscribed peers.
type HookDispatcher struct {
	registry     *HookRegistry
	peerCallback PeerCallback
	parallelism  int
	peerTimeout  time.Duration
	failMu       sync.Mutex
	failCounts   map[mcp.LeaseKey]int
}

type DispatcherOption func(*HookDispatcher)

func WithDispatcherParallelism(n int) DispatcherOption {
	return func(d *HookDispatcher) {
		if d != nil && n > 0 {
			d.parallelism = n
		}
	}
}

func WithPeerTimeout(timeout time.Duration) DispatcherOption {
	return func(d *HookDispatcher) {
		if d != nil && timeout > 0 {
			d.peerTimeout = timeout
		}
	}
}

func NewHookDispatcher(registry *HookRegistry, cb PeerCallback, opts ...DispatcherOption) *HookDispatcher {
	if registry == nil {
		panic(errNilHookRegistry)
	}
	if cb == nil {
		panic(errNilPeerCallback)
	}

	dispatcher := &HookDispatcher{
		registry:     registry,
		peerCallback: cb,
		parallelism:  defaultDispatcherParallelism,
		peerTimeout:  defaultPeerTimeout,
		failCounts:   make(map[mcp.LeaseKey]int),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(dispatcher)
		}
	}
	return dispatcher
}

func (d *HookDispatcher) DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) ([]peerDecision[mcp.BeforeDecision], error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	leases, payload := d.prepareDispatch(topic, payload)
	if len(leases) == 0 {
		return nil, nil
	}
	return dispatchDecisions(d, ctx, leases, payload, d.peerCallback.CallbackBefore), nil
}

func (d *HookDispatcher) DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) ([]peerDecision[mcp.CheckDecision], error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	leases, payload := d.prepareDispatch(topic, payload)
	if len(leases) == 0 {
		return nil, nil
	}
	return dispatchDecisions(d, ctx, leases, payload, d.peerCallback.CallbackCheck), nil
}

func (d *HookDispatcher) DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) ([]peerDecision[mcp.AfterDecision], error) {
	if err := d.validate(); err != nil {
		return nil, err
	}
	leases, payload := d.prepareDispatch(topic, payload)
	return d.dispatchPreparedAfter(ctx, leases, payload)
}

func (d *HookDispatcher) dispatchPreparedAfter(ctx context.Context, leases []mcp.LeaseKey, payload mcp.HookPayload) ([]peerDecision[mcp.AfterDecision], error) {
	if len(leases) == 0 {
		return nil, nil
	}
	return dispatchDecisions(d, ctx, leases, payload, d.peerCallback.CallbackAfter), nil
}

func (d *HookDispatcher) prepareDispatch(topic string, payload mcp.HookPayload) ([]mcp.LeaseKey, mcp.HookPayload) {
	topic = strings.TrimSpace(topic)
	payload = cloneHookPayload(payload)
	payload.Depth++
	payload.Topic = topic
	payload.HookCallID = strings.TrimSpace(payload.HookCallID)
	if payload.HookCallID == "" {
		payload.HookCallID = newHookCallID()
	}
	if topic == "" {
		return nil, payload
	}
	return d.registry.GetSubscribers(topic), payload
}

func (d *HookDispatcher) validate() error {
	if d == nil {
		return errNilDispatcher
	}
	if d.registry == nil {
		return errNilHookRegistry
	}
	if d.peerCallback == nil {
		return errNilPeerCallback
	}
	return nil
}

func (d *HookDispatcher) parallelismOrDefault() int {
	if d == nil || d.parallelism <= 0 {
		return defaultDispatcherParallelism
	}
	return d.parallelism
}

func (d *HookDispatcher) peerTimeoutOrDefault() time.Duration {
	if d == nil || d.peerTimeout <= 0 {
		return defaultPeerTimeout
	}
	return d.peerTimeout
}

type dispatchJob struct {
	index int
	lease mcp.LeaseKey
}

func dispatchDecisions[T any](
	d *HookDispatcher,
	ctx context.Context,
	leases []mcp.LeaseKey,
	payload mcp.HookPayload,
	invoke func(context.Context, mcp.LeaseKey, mcp.HookPayload) (T, error),
) []peerDecision[T] {
	results := make([]peerDecision[T], len(leases))
	jobs := make(chan dispatchJob, len(leases))
	workers := min(d.parallelismOrDefault(), len(leases))

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			runDispatchWorker(d, ctx, jobs, payload, results, invoke)
		}()
	}
	for i, lease := range leases {
		jobs <- dispatchJob{index: i, lease: lease}
	}
	close(jobs)
	wg.Wait()
	return results
}

func runDispatchWorker[T any](
	d *HookDispatcher,
	ctx context.Context,
	jobs <-chan dispatchJob,
	payload mcp.HookPayload,
	results []peerDecision[T],
	invoke func(context.Context, mcp.LeaseKey, mcp.HookPayload) (T, error),
) {
	for job := range jobs {
		callCtx, cancel := config.WithPeerTimeout(ctx, d.peerTimeoutOrDefault())
		decision, err := invoke(callCtx, job.lease, cloneHookPayload(payload))
		cancel()
		failures := d.recordPeerResult(job.lease, err)
		results[job.index] = peerDecision[T]{
			Lease:               job.lease,
			Decision:            decision,
			Err:                 err,
			ConsecutiveFailures: failures,
		}
	}
}

func (d *HookDispatcher) recordPeerResult(lease mcp.LeaseKey, err error) int {
	d.failMu.Lock()
	defer d.failMu.Unlock()

	if d.failCounts == nil {
		d.failCounts = make(map[mcp.LeaseKey]int)
	}

	if err == nil {
		delete(d.failCounts, lease)
		return 0
	}
	d.failCounts[lease]++
	return d.failCounts[lease]
}

// ForgetLease clears failure tracking for a lease after unsubscribe to avoid leaks.
func (d *HookDispatcher) ForgetLease(lease mcp.LeaseKey) {
	d.failMu.Lock()
	delete(d.failCounts, lease)
	d.failMu.Unlock()
}

func cloneHookPayload(payload mcp.HookPayload) mcp.HookPayload {
	cloned := payload
	cloned.Context = cloneRawMessage(payload.Context)
	return cloned
}

func newHookCallID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d_fallback", hookCallIDPrefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%d_%s", hookCallIDPrefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}
