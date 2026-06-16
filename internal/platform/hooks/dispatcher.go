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

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

const (
	defaultDispatcherParallelism = 8
	defaultPeerTimeout           = 2 * time.Second
	hookCallIDPrefix             = "hook"
)

var (
	errNilDispatcher       = errors.New("hooks dispatcher is nil")
	errNilHookRegistry     = errors.New("hooks dispatcher registry is nil")
	errNilPeerCallback     = errors.New("hooks peer callback is not configured")
	errDispatchWorkerPanic = errors.New("hooks dispatch worker panic")
)

// HookDispatcher fans hook callbacks out to subscribed peers.
type HookDispatcher struct {
	registry     *HookRegistry
	peerCallback contract.PeerCallback
	parallelism  int
	peerTimeout  time.Duration
	failMu       sync.Mutex
	failCounts   map[mcp.LeaseKey]int
}

type DispatcherOption func(*HookDispatcher)

// WithDispatcherParallelism 设置调度器parallelism。
func WithDispatcherParallelism(n int) DispatcherOption {
	return func(d *HookDispatcher) {
		if d != nil && n > 0 {
			d.parallelism = n
		}
	}
}

// WithPeerTimeout 设置peer超时。
func WithPeerTimeout(timeout time.Duration) DispatcherOption {
	return func(d *HookDispatcher) {
		if d != nil && timeout > 0 {
			d.peerTimeout = timeout
		}
	}
}

// NewHookDispatcher 创建hook调度器。
func NewHookDispatcher(registry *HookRegistry, cb contract.PeerCallback, opts ...DispatcherOption) (*HookDispatcher, error) {
	if registry == nil {
		return nil, errNilHookRegistry
	}
	if cb == nil {
		return nil, errNilPeerCallback
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
	return dispatcher, nil
}

// DispatchBefore 派发before。
func (d *HookDispatcher) DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) ([]peerDecision[mcp.BeforeDecision], error) {
	return d.dispatchBeforeBySelector(ctx, mcp.Selector{Subscription: topic}, payload)
}

func (d *HookDispatcher) dispatchBeforeBySelector(ctx context.Context, sel mcp.Selector, payload mcp.HookPayload) ([]peerDecision[mcp.BeforeDecision], error) {
	return dispatchBySelector(d, ctx, sel, payload, d.peerCallback.CallbackBefore)
}

// DispatchCheck 派发check。
func (d *HookDispatcher) DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) ([]peerDecision[mcp.CheckDecision], error) {
	return d.dispatchCheckBySelector(ctx, mcp.Selector{Subscription: topic}, payload)
}

func (d *HookDispatcher) dispatchCheckBySelector(ctx context.Context, sel mcp.Selector, payload mcp.HookPayload) ([]peerDecision[mcp.CheckDecision], error) {
	return dispatchBySelector(d, ctx, sel, payload, d.peerCallback.CallbackCheck)
}

// DispatchAfter 派发后置。
func (d *HookDispatcher) DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) ([]peerDecision[mcp.AfterDecision], error) {
	return d.dispatchAfterBySelector(ctx, mcp.Selector{Subscription: topic}, payload)
}

func (d *HookDispatcher) dispatchAfterBySelector(ctx context.Context, sel mcp.Selector, payload mcp.HookPayload) ([]peerDecision[mcp.AfterDecision], error) {
	return dispatchBySelector(d, ctx, sel, payload, d.peerCallback.CallbackAfter)
}

func (d *HookDispatcher) dispatchPreparedAfter(ctx context.Context, leases []mcp.LeaseKey, payload mcp.HookPayload) ([]peerDecision[mcp.AfterDecision], error) {
	return dispatchPrepared(d, ctx, leases, payload, d.peerCallback.CallbackAfter)
}

func (d *HookDispatcher) prepareDispatchBySelector(sel mcp.Selector, payload mcp.HookPayload) ([]mcp.LeaseKey, mcp.HookPayload) {
	topic := strings.TrimSpace(sel.Subscription)
	payload = shared.CloneHookPayload(payload)
	payload.Depth++
	payload.Topic = topic
	payload.HookCallID = strings.TrimSpace(payload.HookCallID)
	if payload.HookCallID == "" {
		payload.HookCallID = newHookCallID()
	}
	if topic == "" {
		return nil, payload
	}
	sel.Subscription = topic
	return d.registry.GetSubscribersBySelector(sel), payload
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

type dispatchWorkerState struct {
	current    dispatchJob
	hasCurrent bool
}

// dispatchDecisions 派发decisions。
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
		state := &dispatchWorkerState{}
		go func(state *dispatchWorkerState) {
			defer func() {
				if rec := recover(); rec != nil {
					markDispatchWorkerPanicResult(d, results, state.current, state.hasCurrent, rec)
				}
				wg.Done()
			}()
			runDispatchWorker(d, ctx, jobs, payload, results, invoke, state)
		}(state)
	}
	for i, lease := range leases {
		jobs <- dispatchJob{index: i, lease: lease}
	}
	close(jobs)
	wg.Wait()
	return results
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
// ForgetLease 处理forget租约。
func (d *HookDispatcher) ForgetLease(lease mcp.LeaseKey) {
	d.failMu.Lock()
	delete(d.failCounts, lease)
	d.failMu.Unlock()
}

func newHookCallID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d_fallback", hookCallIDPrefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%d_%s", hookCallIDPrefix, time.Now().UnixMilli(), hex.EncodeToString(buf))
}
