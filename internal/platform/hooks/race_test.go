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

	releaseCallbacks := make(chan struct{})
	callbackEntered := make(chan struct{})
	shutdownDone := make(chan struct{})
	var callbackOnce sync.Once
	var callbackCount atomic.Int32

	errCh := make(chan error, 64)
	dispatcher := mustNewHookDispatcher(t, registry, stubPeerCallback{
		before: func(_ context.Context, gotLease mcp.LeaseKey, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
			if gotLease != lease {
				errCh <- fmt.Errorf("callback lease = %#v, want %#v", gotLease, lease)
			}
			if payload.AgentID != "agent-race" || payload.ThreadID != "thread-race" {
				errCh <- fmt.Errorf("callback payload scope = (%q,%q), want (%q,%q)", payload.AgentID, payload.ThreadID, "agent-race", "thread-race")
			}
			callbackCount.Add(1)
			callbackOnce.Do(func() {
				close(callbackEntered)
			})
			<-releaseCallbacks
			return mcp.BeforeDecision{Decision: mcp.HookDecisionAllow}, nil
		},
	}, WithDispatcherParallelism(4))

	var cancelCalls atomic.Int32
	store := &managerReviewStoreStub{
		cancelPendingReviewsByLeaseFunc: func(_ context.Context, subscriberLease string) (int, error) {
			cancelCalls.Add(1)
			if subscriberLease != "lease-race/1" {
				return 0, fmt.Errorf("CancelPendingReviewsByLease() lease = %q, want %q", subscriberLease, "lease-race/1")
			}
			return 1, nil
		},
	}
	manager := mustNewManager(t, registry, dispatcher, mustNewHookResolver(t, store))

	const workers = 16
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(workers + 1)
	var done sync.WaitGroup
	done.Add(workers + 1)
	results := make(chan string, workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start

			decision, err := manager.DispatchBefore(context.Background(), TopicToolBefore, mcp.HookPayload{
				AgentID:  "agent-race",
				ThreadID: "thread-race",
			})
			if err != nil {
				errCh <- fmt.Errorf("DispatchBefore() error = %w", err)
				return
			}
			results <- decision.Decision
		}()
	}

	go func() {
		defer done.Done()
		ready.Done()
		<-start
		<-callbackEntered
		if err := manager.ShutdownHooks(context.Background(), lease); err != nil {
			errCh <- fmt.Errorf("ShutdownHooks() error = %w", err)
		}
		close(shutdownDone)
	}()

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
	if callbackCount.Load() == 0 {
		t.Fatal("expected at least one in-flight callback during shutdown")
	}
	if allowCount == 0 {
		t.Fatal("expected at least one dispatch to complete after entering callback")
	}
	if cancelCalls.Load() != 1 {
		t.Fatalf("CancelPendingReviewsByLease() calls = %d, want 1", cancelCalls.Load())
	}
	if _, ok := registry.GetSubscription(lease); ok {
		t.Fatal("ShutdownHooks() left subscription registered")
	}

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
