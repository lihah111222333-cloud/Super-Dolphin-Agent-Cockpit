package waitaggregate

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type waitCall struct {
	index  int
	result accesswait.WaitResult
	err    error
}

type collectionState struct {
	items               []Item
	remaining           int
	indeterminateReturn bool
	conditionReached    bool
}

// compatibilityClock 是旧 New/Wait 路径的非权威兼容时间源。
// candidate 生产接线不得依赖它，必须从已认证 security-time owner 注入 Clock。
func compatibilityClock() time.Time { return time.Now().UTC() }

// Wait 执行一次有界聚合等待，便于不需要保留 Aggregator 的调用方使用。
func Wait(ctx context.Context, waiter Waiter, request Request) (Result, error) {
	aggregator, err := New(waiter)
	if err != nil {
		return Result{}, err
	}
	return aggregator.Wait(ctx, request)
}

// WaitWithClock 是绑定显式安全时间 seam 的一次性候选调用。
func WaitWithClock(ctx context.Context, waiter Waiter, request Request, clock Clock) (Result, error) {
	aggregator, err := NewWithClock(waiter, clock)
	if err != nil {
		return Result{}, err
	}
	return aggregator.Wait(ctx, request)
}

// Aggregate 是 Wait 的语义别名，保留给聚合调用方使用。
func Aggregate(ctx context.Context, waiter Waiter, request Request) (Result, error) {
	return Wait(ctx, waiter, request)
}

// AggregateWithClock 是绑定显式安全时间 seam 的 Aggregate 别名。
func AggregateWithClock(ctx context.Context, waiter Waiter, request Request, clock Clock) (Result, error) {
	return WaitWithClock(ctx, waiter, request, clock)
}

// Wait 并发等待固定目标；成功时只取消内存中的兄弟 context，不取消 durable 句柄。
func (a *Aggregator) Wait(ctx context.Context, request Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("wait aggregate context is nil")
	}
	if a == nil || a.clock == nil {
		return Result{}, ErrNilClock
	}
	now := a.clock()
	if err := validateClockNow(now); err != nil {
		return Result{}, err
	}
	return a.waitAt(ctx, request, now)
}

// WaitAt 执行一次已捕获安全时间观察的聚合等待。
//
// Handler 等跨层调用方应在构造 deadline 后把同一次 now 传入，避免
// request.ValidateAt 与 context timer 因连续 clock 采样发生边界漂移。
func (a *Aggregator) WaitAt(ctx context.Context, request Request, now time.Time) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("wait aggregate context is nil")
	}
	if a == nil || a.clock == nil {
		return Result{}, ErrNilClock
	}
	if err := validateClockNow(now); err != nil {
		return Result{}, err
	}
	return a.waitAt(ctx, request, now)
}

func (a *Aggregator) waitAt(ctx context.Context, request Request, now time.Time) (Result, error) {
	if err := request.ValidateAt(now); err != nil {
		return Result{}, err
	}
	// Derive the process timer from the security-clock duration rather than
	// feeding a potentially skewed wall timestamp to context.WithDeadline.
	// This keeps a future/past owner observation bounded without silently
	// consulting a second wall clock for validation.
	workCtx, cancel := context.WithTimeout(ctx, request.DeadlineAt.Sub(now))
	defer cancel()
	calls := startWaitCalls(workCtx, request.Targets, a)
	state, err := a.collectCalls(ctx, workCtx, cancel, request, calls)
	if err != nil {
		return Result{Condition: request.Condition, Items: state.items}, err
	}
	reached := state.conditionReached || conditionReachedByItems(request, state.items)
	return finalize(request, state.items, reached, state.indeterminateReturn), nil
}

func validateClockNow(now time.Time) error {
	if now.IsZero() || now.Location() != time.UTC {
		return ErrInvalidClock
	}
	return nil
}

func startWaitCalls(ctx context.Context, targets []Target, aggregator *Aggregator) chan waitCall {
	calls := make(chan waitCall, len(targets))
	for index, target := range targets {
		index, target := index, target
		runtimesafe.SafeGo(ctx, nil, "waitaggregate.target", func(runCtx context.Context) {
			aggregator.waitOne(runCtx, target, index, calls)
		})
	}
	return calls
}

func (a *Aggregator) collectCalls(parent, work context.Context, cancel context.CancelFunc, request Request, calls <-chan waitCall) (collectionState, error) {
	state := collectionState{items: pendingItems(request.Targets), remaining: len(request.Targets)}
	var workDone <-chan struct{} = work.Done()
	var drain <-chan time.Time
	for state.remaining > 0 {
		call, stop, err := nextWaitCall(parent, workDone, drain, calls, request, state.items)
		if err != nil {
			return state, err
		}
		if stop {
			return state, nil
		}
		state.remaining--
		state.items[call.index] = itemFromCall(request, call)
		state, workDone, drain = updateCollectionState(state, request, cancel, workDone, state.items[call.index])
	}
	return state, nil
}

func nextWaitCall(parent context.Context, workDone <-chan struct{}, drain <-chan time.Time, calls <-chan waitCall, request Request, items []Item) (waitCall, bool, error) {
	select {
	case call := <-calls:
		return call, false, nil
	case <-parent.Done():
		return waitCall{}, true, parent.Err()
	case <-workDone:
		drainAvailableCalls(request, items, calls)
		return waitCall{}, true, nil
	case <-drain:
		return waitCall{}, true, nil
	}
}

func updateCollectionState(state collectionState, request Request, cancel context.CancelFunc, workDone <-chan struct{}, item Item) (collectionState, <-chan struct{}, <-chan time.Time) {
	// A worker that exits without a result is an owner failure, not ordinary
	// target indeterminacy. Cancel siblings immediately so a panic/Goexit
	// cannot leave the aggregate waiting until its deadline.
	if item.Outcome == OutcomeIndeterminate && (request.ReturnOnIndeterminate || errors.Is(item.Err, ErrWaiterFailure)) {
		state.indeterminateReturn = true
		cancel()
		return state, nil, time.After(10 * time.Millisecond)
	}
	if item.Outcome == OutcomeSuccess && conditionReachedByItems(request, state.items) {
		state.conditionReached = true
		cancel()
		return state, nil, time.After(10 * time.Millisecond)
	}
	return state, workDone, nil
}

func (a *Aggregator) waitOne(ctx context.Context, target Target, index int, calls chan<- waitCall) {
	var (
		result    accesswait.WaitResult
		err       error
		completed bool
	)
	defer func() {
		// SafeGo also recovers panics at the goroutine boundary, but that
		// recovery cannot publish a waitCall. Recover here at the worker owner
		// boundary so every started target contributes one bounded call. Keep a
		// stable error only; never expose the panic value or stack to callers.
		if recovered := recover(); recovered != nil || !completed {
			result = accesswait.WaitResult{}
			err = ErrWaiterFailure
		}
		// calls is sized to the target count and each worker publishes exactly
		// once. The send therefore remains bounded even when Wait cancels its
		// siblings and the collector has already stopped receiving.
		calls <- waitCall{index: index, result: result, err: err}
	}()
	result, err = a.waiter.Wait(ctx, target.Poll)
	completed = true
}

func itemFromCall(request Request, call waitCall) Item {
	item := Item{TargetKey: request.Targets[call.index].Key, Result: call.result, Err: call.err}
	item.Outcome = classify(request.Predicate, call.result, call.err)
	return item
}

func drainAvailableCalls(request Request, items []Item, calls <-chan waitCall) {
	for {
		select {
		case call := <-calls:
			items[call.index] = itemFromCall(request, call)
		default:
			return
		}
	}
}
