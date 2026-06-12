package hooks

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type managerReviewStoreStub struct {
	savePendingReviewFunc           func(context.Context, mcp.PendingHookReview) error
	saved                           []mcp.PendingHookReview
	byID                            map[string]mcp.PendingHookReview
	cancelPendingReviewsByLeaseFunc func(context.Context, string) (int, error)
}

func (s *managerReviewStoreStub) SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error {
	if s.savePendingReviewFunc != nil {
		if err := s.savePendingReviewFunc(ctx, review); err != nil {
			return err
		}
	}
	if s.byID == nil {
		s.byID = make(map[string]mcp.PendingHookReview)
	}
	s.saved = append(s.saved, review)
	s.byID[review.HookCallID] = review
	return nil
}

func (s *managerReviewStoreStub) GetPendingReview(_ context.Context, hookCallID string) (mcp.PendingHookReview, error) {
	review, ok := s.byID[hookCallID]
	if !ok {
		return mcp.PendingHookReview{}, contract.ErrHookReviewNotFound
	}
	return review, nil
}

func (s *managerReviewStoreStub) GetResolvedReview(_ context.Context, _ string) (string, time.Time, string, error) {
	return "", time.Time{}, "", contract.ErrHookReviewNotFound
}

func (s *managerReviewStoreStub) ListPendingReviews(_ context.Context, _ string) ([]mcp.PendingHookReview, error) {
	return nil, nil
}

func (s *managerReviewStoreStub) ResolvePendingReview(_ context.Context, _, _, _, _, _ string) error {
	return nil
}

func (s *managerReviewStoreStub) CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error) {
	if s.cancelPendingReviewsByLeaseFunc != nil {
		return s.cancelPendingReviewsByLeaseFunc(ctx, subscriberLease)
	}
	return 0, nil
}

func (s *managerReviewStoreStub) CancelPendingReviewsByAgent(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (s *managerReviewStoreStub) CancelExpiredReviews(_ context.Context) (int, error) {
	return 0, nil
}

func (s *managerReviewStoreStub) RecoverOnStartup(_ context.Context) ([]mcp.PendingHookReview, error) {
	return nil, nil
}

func mustNewHookDispatcher(t testing.TB, registry *HookRegistry, cb contract.PeerCallback, opts ...DispatcherOption) *HookDispatcher {
	t.Helper()
	dispatcher, err := NewHookDispatcher(registry, cb, opts...)
	if err != nil {
		t.Fatalf("NewHookDispatcher() error = %v", err)
	}
	return dispatcher
}

func mustNewHookResolver(t testing.TB, store contract.HookReviewStore, opts ...ResolverOption) *HookResolver {
	t.Helper()
	resolver, err := NewHookResolver(store, opts...)
	if err != nil {
		t.Fatalf("NewHookResolver() error = %v", err)
	}
	return resolver
}

func mustNewManager(t testing.TB, registry *HookRegistry, dispatcher *HookDispatcher, resolver *HookResolver, opts ...ManagerOption) *Manager {
	t.Helper()
	manager, err := NewManager(registry, dispatcher, resolver, opts...)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func newDepthTestManager(args ...any) (*Manager, *HookRegistry) {
	var (
		t    testing.TB
		cb   contract.PeerCallback
		opts []ManagerOption
	)
	switch first := args[0].(type) {
	case testing.TB:
		t = first
		if len(args) < 2 {
			t.Fatal("newDepthTestManager() missing peer callback")
		}
		var ok bool
		cb, ok = args[1].(contract.PeerCallback)
		if !ok {
			t.Fatalf("newDepthTestManager() peer callback type = %T, want contract.PeerCallback", args[1])
		}
		opts = castManagerOptions(t, args[2:]...)
	case contract.PeerCallback:
		cb = first
		opts = castManagerOptions(nil, args[1:]...)
	default:
		// archguard:ignore panic_count -- helper misuse is a test authoring error.
		panic(fmt.Sprintf("newDepthTestManager() first arg type = %T", args[0]))
	}
	if t != nil {
		t.Helper()
	}
	registry := NewHookRegistry()
	if t == nil {
		dispatcher, err := NewHookDispatcher(registry, cb)
		if err != nil {
			// archguard:ignore panic_count -- helper constructs a fixed-valid hook dispatcher.
			panic(err)
		}
		resolver, err := NewHookResolver(&managerReviewStoreStub{})
		if err != nil {
			// archguard:ignore panic_count -- helper constructs a fixed-valid hook resolver.
			panic(err)
		}
		manager, err := NewManager(registry, dispatcher, resolver, opts...)
		if err != nil {
			// archguard:ignore panic_count -- helper constructs a fixed-valid hook manager.
			panic(err)
		}
		return manager, registry
	}
	dispatcher := mustNewHookDispatcher(t, registry, cb)
	resolver := mustNewHookResolver(t, &managerReviewStoreStub{})
	return mustNewManager(t, registry, dispatcher, resolver, opts...), registry
}

func subscribeDepthTestTopics(t *testing.T, registry *HookRegistry, topics ...string) {
	t.Helper()

	_, err := registry.Subscribe(mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}, mcp.HookSubscribeRequest{
		SubscriptionID: "depth-sub",
		Topics:         topics,
		Scope: mcp.Selector{Scope: &mcp.SelectorScope{
			AgentID:  "agent-1",
			ThreadID: "thread-1",
		}},
	})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
}

func depthTestPayload(depth int) mcp.HookPayload {
	return mcp.HookPayload{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Depth:    depth,
	}
}

func castManagerOptions(t testing.TB, args ...any) []ManagerOption {
	opts := make([]ManagerOption, 0, len(args))
	for _, arg := range args {
		opt, ok := arg.(ManagerOption)
		if !ok {
			if t != nil {
				t.Fatalf("manager option type = %T, want hooks.ManagerOption", arg)
			}
			// archguard:ignore panic_count -- helper misuse without testing.TB must fail loudly.
			panic(fmt.Sprintf("manager option type = %T", arg))
		}
		opts = append(opts, opt)
	}
	return opts
}
