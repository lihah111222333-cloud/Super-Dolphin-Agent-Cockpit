package hooks

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestEscalate_Normal(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}
	resolver := mustNewHookResolver(
		t,
		store,
		WithResolverTTL(2*time.Minute),
		WithResolverLogger(pkglogger.New(pkglogger.NewTextHandler(io.Discard, nil))),
	)

	got, err := resolver.Escalate(context.TODO(), "call-escalate", mcp.HookPayload{
		Topic:    " " + TopicToolAfter + " ",
		AgentID:  " agent-1 ",
		ThreadID: " thread-1 ",
		TurnID:   " turn-1 ",
	}, mcp.LeaseKey{InstanceID: " lease-a ", Generation: 1}, 0)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved reviews = %d, want 1", len(store.saved))
	}
	assertEscalatedReview(t, got)
}

func TestEscalate_EmptyHookCallID(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}
	resolver := mustNewHookResolver(t, store)

	_, err := resolver.Escalate(context.Background(), "", mcp.HookPayload{
		Topic:   TopicToolAfter,
		AgentID: "agent-1",
	}, mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}, 0)
	if err == nil {
		t.Fatal("Escalate() error = nil, want hook_call_id error")
	}
	if !strings.Contains(err.Error(), "hook_call_id") {
		t.Fatalf("Escalate() error = %v, want hook_call_id error", err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved reviews = %d, want 0", len(store.saved))
	}
}

func TestEscalate_UsesExplicitTTLMs(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}
	resolver := mustNewHookResolver(
		t,
		store,
		WithResolverTTL(2*time.Minute),
		WithResolverLogger(pkglogger.New(pkglogger.NewTextHandler(io.Discard, nil))),
	)

	got, err := resolver.Escalate(context.TODO(), "call-escalate-ttl", mcp.HookPayload{
		Topic:      TopicToolAfter,
		AgentID:    "agent-1",
		ThreadID:   "thread-1",
		DeadlineMs: time.Now().Add(10 * time.Minute).UnixMilli(),
	}, mcp.LeaseKey{InstanceID: "lease-a", Generation: 1}, 30_000)
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if got.DeadlineAt.Sub(got.CreatedAt) != 30*time.Second {
		t.Fatalf("DeadlineAt-CreatedAt = %s, want %s", got.DeadlineAt.Sub(got.CreatedAt), 30*time.Second)
	}
}

func TestSweepExpired_Delegates(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{
		cancelExpiredReviewsFunc: func(ctx context.Context) (int, error) {
			if ctx == nil {
				t.Fatal("CancelExpiredReviews() ctx = nil, want background context")
			}
			return 2, nil
		},
	}

	got, err := mustNewHookResolver(t, store).SweepExpired(context.TODO())
	if err != nil {
		t.Fatalf("SweepExpired() error = %v", err)
	}
	if got != 2 {
		t.Fatalf("SweepExpired() = %d, want %d", got, 2)
	}
	if store.cancelExpiredCalls != 1 {
		t.Fatalf("cancelExpiredCalls = %d, want 1", store.cancelExpiredCalls)
	}
}

func TestCancelByLease_Delegates(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{
		cancelPendingReviewsByLeaseFunc: func(ctx context.Context, subscriberLease string) (int, error) {
			if ctx == nil {
				t.Fatal("CancelPendingReviewsByLease() ctx = nil, want background context")
			}
			if subscriberLease != "lease-a/2" {
				t.Fatalf("subscriberLease = %q, want %q", subscriberLease, "lease-a/2")
			}
			return 3, nil
		},
	}

	got, err := mustNewHookResolver(t, store).CancelByLease(context.TODO(), mcp.LeaseKey{InstanceID: " lease-a ", Generation: 2})
	if err != nil {
		t.Fatalf("CancelByLease() error = %v", err)
	}
	if got != 3 {
		t.Fatalf("CancelByLease() = %d, want %d", got, 3)
	}
}

func TestCancelByAgent_Delegates(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{
		cancelPendingReviewsByAgentFunc: func(ctx context.Context, agentID string) (int, error) {
			if ctx == nil {
				t.Fatal("CancelPendingReviewsByAgent() ctx = nil, want background context")
			}
			if agentID != "agent-1" {
				t.Fatalf("agentID = %q, want %q", agentID, "agent-1")
			}
			return 4, nil
		},
	}

	got, err := mustNewHookResolver(t, store).CancelByAgent(context.TODO(), " agent-1 ")
	if err != nil {
		t.Fatalf("CancelByAgent() error = %v", err)
	}
	if got != 4 {
		t.Fatalf("CancelByAgent() = %d, want %d", got, 4)
	}
}

func TestRecoverOnStartup_Delegates(t *testing.T) {
	t.Parallel()

	want := []mcp.PendingHookReview{{HookCallID: "call-recover", AgentID: "agent-1"}}
	store := &stubHookReviewStore{
		recoverOnStartupResult: want,
		recoverOnStartupFunc: func(ctx context.Context) ([]mcp.PendingHookReview, error) {
			if ctx == nil {
				t.Fatal("RecoverOnStartup() ctx = nil, want background context")
			}
			return want, nil
		},
	}

	got, err := mustNewHookResolver(t, store).RecoverOnStartup(context.TODO())
	if err != nil {
		t.Fatalf("RecoverOnStartup() error = %v", err)
	}
	if len(got) != 1 || got[0].HookCallID != "call-recover" {
		t.Fatalf("RecoverOnStartup() = %#v, want %#v", got, want)
	}
	if store.recoverCalls != 1 {
		t.Fatalf("recoverCalls = %d, want 1", store.recoverCalls)
	}
}

func TestListPendingReviews_EmptyAgentID(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}

	_, err := mustNewHookResolver(t, store).ListPendingReviews(context.Background(), "   ")
	if err == nil {
		t.Fatal("ListPendingReviews() error = nil, want agentID error")
	}
	if !strings.Contains(err.Error(), "agentID is required") {
		t.Fatalf("ListPendingReviews() error = %v, want agentID error", err)
	}
	if len(store.listAgentIDs) != 0 {
		t.Fatalf("list calls = %d, want 0", len(store.listAgentIDs))
	}
}

func assertEscalatedReview(t *testing.T, got mcp.PendingHookReview) {
	t.Helper()

	assertHookString(t, "HookCallID", got.HookCallID, "call-escalate")
	assertHookString(t, "Topic", got.Topic, TopicToolAfter)
	assertHookString(t, "AgentID", got.AgentID, "agent-1")
	assertHookString(t, "ThreadID", got.ThreadID, "thread-1")
	assertHookString(t, "TurnID", got.TurnID, "turn-1")
	assertHookString(t, "SubscriberLease", got.SubscriberLease, "lease-a/1")
	assertHookPayloadContains(t, got.Payload, `"thread_id":" thread-1 "`)
	assertHookString(t, "DefaultAction", got.DefaultAction, pendingDefaultDecision)
	assertHookDuration(t, "DeadlineAt-CreatedAt", got.DeadlineAt.Sub(got.CreatedAt), 2*time.Minute)
}

func assertHookString(t *testing.T, name, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertHookPayloadContains(t *testing.T, payload []byte, want string) {
	t.Helper()

	if !strings.Contains(string(payload), want) {
		t.Fatalf("Payload = %s, want %s", string(payload), want)
	}
}

func assertHookDuration(t *testing.T, name string, got, want time.Duration) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %s, want %s", name, got, want)
	}
}
