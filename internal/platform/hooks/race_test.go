package hooks

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestConcurrentDispatchAndShutdown(t *testing.T) {
	registry := NewHookRegistry()
	lease := mcp.LeaseKey{InstanceID: "lease-race", Generation: 1}
	subscribeRaceHook(t, registry, lease)

	releaseCallbacks := make(chan struct{})
	callbackEntered := make(chan struct{})
	shutdownDone := make(chan struct{})
	var callbackOnce sync.Once
	var callbackCount atomic.Int32

	errCh := make(chan error, 64)
	dispatcher := newRaceHookDispatcher(t, registry, lease, releaseCallbacks, callbackEntered, &callbackOnce, &callbackCount, errCh)

	var cancelCalls atomic.Int32
	manager := newRaceHookManager(t, registry, dispatcher, &cancelCalls)

	const workers = 16
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(workers + 1)
	var done sync.WaitGroup
	results := make(chan string, workers)

	startRaceDispatchWorkers(manager, workers, start, &ready, &done, results, errCh)
	startRaceShutdownWorker(manager, lease, start, callbackEntered, shutdownDone, &ready, &done, errCh)

	ready.Wait()
	close(start)
	<-shutdownDone
	close(releaseCallbacks)
	done.Wait()
	close(results)
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	allowCount, _ := countRaceDispatchDecisions(t, results)
	assertRaceShutdownState(t, registry, lease, callbackCount.Load(), allowCount, cancelCalls.Load())

	decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-race",
		ThreadID: "thread-race",
	})
	if err != nil {
		t.Fatalf("DispatchBefore() after shutdown error = %v", err)
	}
	if decision.Decision != mcp.HookDecisionDeny {
		t.Fatalf("DispatchBefore() after shutdown decision = %q, want %q", decision.Decision, mcp.HookDecisionDeny)
	}
}

func subscribeRaceHook(t *testing.T, registry *HookRegistry, lease mcp.LeaseKey) {
	t.Helper()

	_, err := registry.Subscribe(lease, mcp.HookSubscribeRequest{
		SubscriptionID: "sub-race",
		Topics:         []string{TopicToolBefore},
		Scope: mcp.Selector{Scope: &mcp.SelectorScope{
			AgentID:  "agent-race",
			ThreadID: "thread-race",
		}},
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
}

func newRaceHookDispatcher(
	t *testing.T,
	registry *HookRegistry,
	lease mcp.LeaseKey,
	releaseCallbacks <-chan struct{},
	callbackEntered chan<- struct{},
	callbackOnce *sync.Once,
	callbackCount *atomic.Int32,
	errCh chan<- error,
) *HookDispatcher {
	t.Helper()

	return mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if gotLease != lease {
				errCh <- fmt.Errorf("callback lease = %#v, want %#v", gotLease, lease)
			}
			if payload.AgentID != "agent-race" {
				errCh <- fmt.Errorf("callback AgentID = %q, want agent-race", payload.AgentID)
			}
			if payload.ThreadID != "thread-race" {
				errCh <- fmt.Errorf("callback ThreadID = %q, want thread-race", payload.ThreadID)
			}
			callbackCount.Add(1)
			callbackOnce.Do(func() { close(callbackEntered) })
			<-releaseCallbacks
			return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
		},
	}, WithDispatcherParallelism(4))
}

func newRaceHookManager(t *testing.T, registry *HookRegistry, dispatcher *HookDispatcher, cancelCalls *atomic.Int32) *Manager {
	t.Helper()

	store := &managerReviewStoreStub{
		cancelPendingReviewsByLeaseFunc: func(_ context.Context, subscriberLease string) (int, error) {
			cancelCalls.Add(1)
			if subscriberLease != "lease-race/1" {
				return 0, fmt.Errorf("CancelPendingReviewsByLease() lease = %q, want %q", subscriberLease, "lease-race/1")
			}
			return 1, nil
		},
	}
	return mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))
}

func startRaceDispatchWorkers(
	manager *Manager,
	workers int,
	start <-chan struct{},
	ready *sync.WaitGroup,
	done *sync.WaitGroup,
	results chan<- string,
	errCh chan<- error,
) {
	for range workers {
		done.Go(func() {
			ready.Done()
			<-start
			dispatchRaceBeforeHook(manager, results, errCh)
		})
	}
}

func dispatchRaceBeforeHook(manager *Manager, results chan<- string, errCh chan<- error) {
	decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
		AgentID:  "agent-race",
		ThreadID: "thread-race",
	})
	if err != nil {
		errCh <- fmt.Errorf("DispatchBefore() error = %w", err)
		return
	}
	results <- decision.Decision
}

func startRaceShutdownWorker(
	manager *Manager,
	lease mcp.LeaseKey,
	start <-chan struct{},
	callbackEntered <-chan struct{},
	shutdownDone chan<- struct{},
	ready *sync.WaitGroup,
	done *sync.WaitGroup,
	errCh chan<- error,
) {
	done.Go(func() {
		ready.Done()
		<-start
		<-callbackEntered
		if err := manager.ShutdownHooks(context.Background(), lease); err != nil {
			errCh <- fmt.Errorf("ShutdownHooks() error = %w", err)
		}
		close(shutdownDone)
	})
}

func countRaceDispatchDecisions(t *testing.T, results <-chan string) (int, int) {
	t.Helper()

	var allowCount, denyCount int
	for decision := range results {
		switch decision {
		case mcp.HookDecisionAllow:
			allowCount++
		case mcp.HookDecisionDeny:
			denyCount++
		default:
			t.Fatalf("DispatchBefore() decision = %q, want allow or deny", decision)
		}
	}
	return allowCount, denyCount
}

func assertRaceShutdownState(t *testing.T, registry *HookRegistry, lease mcp.LeaseKey, callbackCount int32, allowCount int, cancelCalls int32) {
	t.Helper()

	if callbackCount == 0 {
		t.Fatal("expected at least one in-flight callback during shutdown")
	}
	if allowCount == 0 {
		t.Fatal("expected at least one dispatch to complete after entering callback")
	}
	if cancelCalls != 1 {
		t.Fatalf("CancelPendingReviewsByLease() calls = %d, want 1", cancelCalls)
	}
	if _, ok := registry.GetSubscription(lease); ok {
		t.Fatal("ShutdownHooks() left subscription registered")
	}
}
