package hooks

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type resolvePendingReviewCall struct {
	hookCallID     string
	decision       string
	reason         string
	idempotencyKey string
}

type stubResolvedReview struct {
	decision   string
	resolvedAt time.Time
	err        error
}

type stubHookReviewStore struct {
	pending                  map[string]mcp.PendingHookReview
	resolved                 map[string]stubResolvedReview
	listPendingReviewsResult []mcp.PendingHookReview
	recoverOnStartupResult   []mcp.PendingHookReview

	saved              []mcp.PendingHookReview
	resolveCalls       []resolvePendingReviewCall
	listAgentIDs       []string
	cancelByLeaseCalls []string
	cancelByAgentCalls []string
	cancelExpiredCalls int
	recoverCalls       int

	savePendingReviewFunc           func(context.Context, mcp.PendingHookReview) error
	getPendingReviewFunc            func(context.Context, string) (mcp.PendingHookReview, error)
	listPendingReviewsFunc          func(context.Context, string) ([]mcp.PendingHookReview, error)
	resolvePendingReviewFunc        func(context.Context, string, string, string, string) error
	cancelPendingReviewsByLeaseFunc func(context.Context, string) (int, error)
	cancelPendingReviewsByAgentFunc func(context.Context, string) (int, error)
	cancelExpiredReviewsFunc        func(context.Context) (int, error)
	recoverOnStartupFunc            func(context.Context) ([]mcp.PendingHookReview, error)
}

type stubResolvedReviewStore struct {
	*stubHookReviewStore
}

func (s *stubHookReviewStore) SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error {
	if s.savePendingReviewFunc != nil {
		return s.savePendingReviewFunc(ctx, review)
	}
	if s.pending == nil {
		s.pending = make(map[string]mcp.PendingHookReview)
	}
	s.saved = append(s.saved, review)
	s.pending[review.HookCallID] = review
	return nil
}

func (s *stubHookReviewStore) GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error) {
	if s.getPendingReviewFunc != nil {
		return s.getPendingReviewFunc(ctx, hookCallID)
	}
	return s.pending[hookCallID], nil
}

func (s *stubHookReviewStore) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	s.listAgentIDs = append(s.listAgentIDs, agentID)
	if s.listPendingReviewsFunc != nil {
		return s.listPendingReviewsFunc(ctx, agentID)
	}
	return s.listPendingReviewsResult, nil
}

func (s *stubHookReviewStore) ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey string) error {
	s.resolveCalls = append(s.resolveCalls, resolvePendingReviewCall{
		hookCallID:     hookCallID,
		decision:       decision,
		reason:         reason,
		idempotencyKey: idempotencyKey,
	})
	if s.resolvePendingReviewFunc != nil {
		return s.resolvePendingReviewFunc(ctx, hookCallID, decision, reason, idempotencyKey)
	}
	return nil
}

func (s *stubHookReviewStore) CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error) {
	s.cancelByLeaseCalls = append(s.cancelByLeaseCalls, subscriberLease)
	if s.cancelPendingReviewsByLeaseFunc != nil {
		return s.cancelPendingReviewsByLeaseFunc(ctx, subscriberLease)
	}
	return 0, nil
}

func (s *stubHookReviewStore) CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error) {
	s.cancelByAgentCalls = append(s.cancelByAgentCalls, agentID)
	if s.cancelPendingReviewsByAgentFunc != nil {
		return s.cancelPendingReviewsByAgentFunc(ctx, agentID)
	}
	return 0, nil
}

func (s *stubHookReviewStore) CancelExpiredReviews(ctx context.Context) (int, error) {
	s.cancelExpiredCalls++
	if s.cancelExpiredReviewsFunc != nil {
		return s.cancelExpiredReviewsFunc(ctx)
	}
	return 0, nil
}

func (s *stubHookReviewStore) RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error) {
	s.recoverCalls++
	if s.recoverOnStartupFunc != nil {
		return s.recoverOnStartupFunc(ctx)
	}
	return s.recoverOnStartupResult, nil
}

func (s *stubResolvedReviewStore) GetResolvedReview(_ context.Context, hookCallID string) (string, time.Time, error) {
	if s.resolved == nil {
		return "", time.Time{}, nil
	}
	review, ok := s.resolved[hookCallID]
	if !ok {
		return "", time.Time{}, nil
	}
	return review.decision, review.resolvedAt, review.err
}

func TestEscalate_Normal(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}
	resolver := NewHookResolver(
		store,
		WithResolverTTL(2*time.Minute),
		WithResolverLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)

	got, err := resolver.Escalate(nil, "call-escalate", mcp.HookPayload{
		Topic:   " " + TopicToolAfter + " ",
		AgentID: " agent-1 ",
	}, mcp.LeaseKey{InstanceID: " lease-a ", Generation: 1})
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved reviews = %d, want 1", len(store.saved))
	}
	if got.HookCallID != "call-escalate" {
		t.Fatalf("HookCallID = %q, want %q", got.HookCallID, "call-escalate")
	}
	if got.Topic != TopicToolAfter {
		t.Fatalf("Topic = %q, want %q", got.Topic, TopicToolAfter)
	}
	if got.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want %q", got.AgentID, "agent-1")
	}
	if got.SubscriberLease != "lease-a/1" {
		t.Fatalf("SubscriberLease = %q, want %q", got.SubscriberLease, "lease-a/1")
	}
	if got.DefaultAction != pendingDefaultDecision {
		t.Fatalf("DefaultAction = %q, want %q", got.DefaultAction, pendingDefaultDecision)
	}
	if got.DeadlineAt.Sub(got.CreatedAt) != 2*time.Minute {
		t.Fatalf("DeadlineAt-CreatedAt = %s, want %s", got.DeadlineAt.Sub(got.CreatedAt), 2*time.Minute)
	}
}

func TestEscalate_EmptyHookCallID(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}
	resolver := NewHookResolver(store)

	_, err := resolver.Escalate(context.Background(), "", mcp.HookPayload{
		Topic:   TopicToolAfter,
		AgentID: "agent-1",
	}, mcp.LeaseKey{InstanceID: "lease-a", Generation: 1})
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

func TestResolve_Approve(t *testing.T) {
	t.Parallel()

	resolvedAt := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	base := &stubHookReviewStore{}
	base.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey string) error {
		if hookCallID != "call-approve" {
			t.Fatalf("hookCallID = %q, want %q", hookCallID, "call-approve")
		}
		if decision != mcp.HookDecisionApprove {
			t.Fatalf("decision = %q, want %q", decision, mcp.HookDecisionApprove)
		}
		if reason != "looks good" {
			t.Fatalf("reason = %q, want %q", reason, "looks good")
		}
		if idempotencyKey != "idem-approve" {
			t.Fatalf("idempotencyKey = %q, want %q", idempotencyKey, "idem-approve")
		}
		if base.resolved == nil {
			base.resolved = make(map[string]stubResolvedReview)
		}
		base.resolved[hookCallID] = stubResolvedReview{
			decision:   decision,
			resolvedAt: resolvedAt,
		}
		return nil
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	got, err := NewHookResolver(store).Resolve(nil, mcp.LeaseKey{}, mcp.HookResolveRequest{
		HookCallID:     " call-approve ",
		Decision:       " APPROVE ",
		Reason:         " looks good ",
		IdempotencyKey: " idem-approve ",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !got.Accepted {
		t.Fatal("Resolve() Accepted = false, want true")
	}
	if got.CanonicalDecision != mcp.HookDecisionApprove {
		t.Fatalf("CanonicalDecision = %q, want %q", got.CanonicalDecision, mcp.HookDecisionApprove)
	}
	if got.PendingState != pendingStateResolved {
		t.Fatalf("PendingState = %q, want %q", got.PendingState, pendingStateResolved)
	}
	if got.ResolvedAt != resolvedAt.Format(time.RFC3339Nano) {
		t.Fatalf("ResolvedAt = %q, want %q", got.ResolvedAt, resolvedAt.Format(time.RFC3339Nano))
	}
	if len(base.resolveCalls) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(base.resolveCalls))
	}
}

func TestResolve_Reject(t *testing.T) {
	t.Parallel()

	resolvedAt := time.Date(2026, 3, 24, 12, 30, 0, 0, time.UTC)
	base := &stubHookReviewStore{
		resolved: make(map[string]stubResolvedReview),
	}
	base.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey string) error {
		base.resolved[hookCallID] = stubResolvedReview{
			decision:   decision,
			resolvedAt: resolvedAt,
		}
		return nil
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	got, err := NewHookResolver(store).Resolve(context.Background(), mcp.LeaseKey{}, mcp.HookResolveRequest{
		HookCallID:     "call-reject",
		Decision:       mcp.HookDecisionReject,
		Reason:         "policy denied",
		IdempotencyKey: "idem-reject",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.CanonicalDecision != mcp.HookDecisionReject {
		t.Fatalf("CanonicalDecision = %q, want %q", got.CanonicalDecision, mcp.HookDecisionReject)
	}
	if got.ResolvedAt != resolvedAt.Format(time.RFC3339Nano) {
		t.Fatalf("ResolvedAt = %q, want %q", got.ResolvedAt, resolvedAt.Format(time.RFC3339Nano))
	}
	if len(base.resolveCalls) != 1 {
		t.Fatalf("resolve calls = %d, want 1", len(base.resolveCalls))
	}
}

func TestResolve_InvalidDecision(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}

	_, err := NewHookResolver(store).Resolve(context.Background(), mcp.LeaseKey{}, mcp.HookResolveRequest{
		HookCallID:     "call-invalid",
		Decision:       mcp.HookDecisionEscalate,
		IdempotencyKey: "idem-invalid",
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want invalid decision error")
	}
	if !strings.Contains(err.Error(), "approve or reject") {
		t.Fatalf("Resolve() error = %v, want approve or reject error", err)
	}
	if len(store.resolveCalls) != 0 {
		t.Fatalf("resolve calls = %d, want 0", len(store.resolveCalls))
	}
}

func TestResolve_Idempotent(t *testing.T) {
	t.Parallel()

	resolvedAt := time.Date(2026, 3, 24, 13, 0, 0, 0, time.UTC)
	base := &stubHookReviewStore{
		resolved: map[string]stubResolvedReview{
			"call-idempotent": {
				decision:   mcp.HookDecisionApprove,
				resolvedAt: resolvedAt,
			},
		},
	}
	base.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey string) error {
		if hookCallID != "call-idempotent" {
			t.Fatalf("hookCallID = %q, want %q", hookCallID, "call-idempotent")
		}
		if idempotencyKey != "idem-same" {
			t.Fatalf("idempotencyKey = %q, want %q", idempotencyKey, "idem-same")
		}
		return nil
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	got, err := NewHookResolver(store).Resolve(context.Background(), mcp.LeaseKey{}, mcp.HookResolveRequest{
		HookCallID:     "call-idempotent",
		Decision:       mcp.HookDecisionReject,
		Reason:         "ignored",
		IdempotencyKey: "idem-same",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.CanonicalDecision != mcp.HookDecisionApprove {
		t.Fatalf("CanonicalDecision = %q, want %q", got.CanonicalDecision, mcp.HookDecisionApprove)
	}
	if got.ResolvedAt != resolvedAt.Format(time.RFC3339Nano) {
		t.Fatalf("ResolvedAt = %q, want %q", got.ResolvedAt, resolvedAt.Format(time.RFC3339Nano))
	}
}

func TestResolve_ReadbackFallback(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{}

	got, err := NewHookResolver(store).Resolve(nil, mcp.LeaseKey{}, mcp.HookResolveRequest{
		HookCallID:     "call-fallback",
		Decision:       mcp.HookDecisionReject,
		Reason:         "missing reader",
		IdempotencyKey: "idem-fallback",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !got.Accepted {
		t.Fatal("Resolve() Accepted = false, want true")
	}
	if got.CanonicalDecision != mcp.HookDecisionReject {
		t.Fatalf("CanonicalDecision = %q, want %q", got.CanonicalDecision, mcp.HookDecisionReject)
	}
	if got.PendingState != pendingStateResolved {
		t.Fatalf("PendingState = %q, want %q", got.PendingState, pendingStateResolved)
	}
	resolvedAt, err := time.Parse(time.RFC3339Nano, got.ResolvedAt)
	if err != nil {
		t.Fatalf("ResolvedAt parse error = %v", err)
	}
	if resolvedAt.IsZero() {
		t.Fatal("ResolvedAt = zero, want fallback timestamp")
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

	got, err := NewHookResolver(store).SweepExpired(nil)
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

	got, err := NewHookResolver(store).CancelByLease(nil, mcp.LeaseKey{InstanceID: " lease-a ", Generation: 2})
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

	got, err := NewHookResolver(store).CancelByAgent(nil, " agent-1 ")
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

	got, err := NewHookResolver(store).RecoverOnStartup(nil)
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

	_, err := NewHookResolver(store).ListPendingReviews(context.Background(), "   ")
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
