package hooks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestResolve_Approve(t *testing.T) {
	t.Parallel()

	resolvedAt := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	base := &stubHookReviewStore{
		pending: map[string]mcp.PendingHookReview{
			"call-approve": {
				HookCallID:      "call-approve",
				SubscriberLease: "instance-approve/1",
			},
		},
	}
	base.resolvePendingReviewFunc = approveResolveFunc(t, base, resolvedAt)
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	got, err := mustNewHookResolver(t, store).Resolve(context.TODO(), mcp.LeaseKey{InstanceID: "instance-approve", Generation: 1}, mcp.HookResolveRequest{
		HookCallID:     " call-approve ",
		Decision:       " APPROVE ",
		Reason:         " looks good ",
		IdempotencyKey: " idem-approve ",
		ResolvedBy:     " reviewer-approve ",
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

func approveResolveFunc(t *testing.T, base *stubHookReviewStore, resolvedAt time.Time) func(context.Context, string, string, string, string, string) error {
	t.Helper()
	return func(_ context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
		assertApproveResolveArgs(t, hookCallID, decision, reason, idempotencyKey, resolvedBy)
		if base.resolved == nil {
			base.resolved = make(map[string]stubResolvedReview)
		}
		base.resolved[hookCallID] = stubResolvedReview{
			decision:   decision,
			resolvedAt: resolvedAt,
		}
		return nil
	}
}

func assertApproveResolveArgs(t *testing.T, hookCallID, decision, reason, idempotencyKey, resolvedBy string) {
	t.Helper()
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
	if resolvedBy != "reviewer-approve" {
		t.Fatalf("resolvedBy = %q, want %q", resolvedBy, "reviewer-approve")
	}
}

func TestResolve_Reject(t *testing.T) {
	t.Parallel()

	resolvedAt := time.Date(2026, 3, 24, 12, 30, 0, 0, time.UTC)
	base := &stubHookReviewStore{
		pending: map[string]mcp.PendingHookReview{
			"call-reject": {
				HookCallID:      "call-reject",
				SubscriberLease: "instance-reject/2",
			},
		},
		resolved: make(map[string]stubResolvedReview),
	}
	base.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
		if resolvedBy != "reviewer-reject" {
			t.Fatalf("resolvedBy = %q, want %q", resolvedBy, "reviewer-reject")
		}
		base.resolved[hookCallID] = stubResolvedReview{
			decision:   decision,
			resolvedAt: resolvedAt,
		}
		return nil
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	got, err := mustNewHookResolver(t, store).Resolve(context.Background(), mcp.LeaseKey{InstanceID: "instance-reject", Generation: 2}, mcp.HookResolveRequest{
		HookCallID:     "call-reject",
		Decision:       mcp.HookDecisionReject,
		Reason:         "policy denied",
		IdempotencyKey: "idem-reject",
		ResolvedBy:     "reviewer-reject",
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

	_, err := mustNewHookResolver(t, store).Resolve(context.Background(), mcp.LeaseKey{}, mcp.HookResolveRequest{
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
		pending: map[string]mcp.PendingHookReview{
			"call-idempotent": {
				HookCallID:      "call-idempotent",
				SubscriberLease: "instance-idempotent/3",
			},
		},
		resolved: map[string]stubResolvedReview{
			"call-idempotent": {
				decision:        mcp.HookDecisionApprove,
				resolvedAt:      resolvedAt,
				subscriberLease: "instance-idempotent/3",
			},
		},
	}
	base.resolvePendingReviewFunc = func(_ context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
		if hookCallID != "call-idempotent" {
			t.Fatalf("hookCallID = %q, want %q", hookCallID, "call-idempotent")
		}
		if idempotencyKey != "idem-same" {
			t.Fatalf("idempotencyKey = %q, want %q", idempotencyKey, "idem-same")
		}
		if resolvedBy != "reviewer-idempotent" {
			t.Fatalf("resolvedBy = %q, want %q", resolvedBy, "reviewer-idempotent")
		}
		return nil
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	got, err := mustNewHookResolver(t, store).Resolve(context.Background(), mcp.LeaseKey{InstanceID: "instance-idempotent", Generation: 3}, mcp.HookResolveRequest{
		HookCallID:     "call-idempotent",
		Decision:       mcp.HookDecisionReject,
		Reason:         "ignored",
		IdempotencyKey: "idem-same",
		ResolvedBy:     "reviewer-idempotent",
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

func TestResolve_ReadbackFailureReturnsError(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{
		pending: map[string]mcp.PendingHookReview{
			"call-fallback": {
				HookCallID:      "call-fallback",
				SubscriberLease: "instance-fallback/4",
			},
		},
	}

	got, err := mustNewHookResolver(t, store).Resolve(context.TODO(), mcp.LeaseKey{InstanceID: "instance-fallback", Generation: 4}, mcp.HookResolveRequest{
		HookCallID:     "call-fallback",
		Decision:       mcp.HookDecisionReject,
		Reason:         "missing reader",
		IdempotencyKey: "idem-fallback",
	})
	if err == nil {
		t.Fatalf("Resolve() = %+v nil error, want readback failure", got)
	}
	if got.Accepted {
		t.Fatalf("Resolve() Accepted = true on readback failure: %+v", got)
	}
}

func TestResolve_CallerLeaseMismatch(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{
		pending: map[string]mcp.PendingHookReview{
			"call-mismatch": {
				HookCallID:      "call-mismatch",
				SubscriberLease: "instance-owner/5",
			},
		},
	}

	_, err := mustNewHookResolver(t, store).Resolve(context.Background(), mcp.LeaseKey{InstanceID: "instance-other", Generation: 9}, mcp.HookResolveRequest{
		HookCallID:     "call-mismatch",
		Decision:       mcp.HookDecisionApprove,
		IdempotencyKey: "idem-mismatch",
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want permission denied")
	}
	if !strings.Contains(err.Error(), contract.ErrHookReviewPermissionDenied.Error()) {
		t.Fatalf("Resolve() error = %v, want permission denied sentinel message", err)
	}
	if !strings.Contains(err.Error(), "instance-owner/5") {
		t.Fatalf("Resolve() error = %v, want stored subscriber lease in message", err)
	}
	if !strings.Contains(err.Error(), "instance-other/9") {
		t.Fatalf("Resolve() error = %v, want caller lease in message", err)
	}
	if len(store.resolveCalls) != 0 {
		t.Fatalf("resolve calls = %d, want 0", len(store.resolveCalls))
	}
}

func TestResolve_AlreadyResolved_WrongLease_Denied(t *testing.T) {
	t.Parallel()

	base := &stubHookReviewStore{
		getPendingReviewFunc: func(context.Context, string) (mcp.PendingHookReview, error) {
			return mcp.PendingHookReview{}, contract.ErrHookReviewNotFound
		},
		resolved: map[string]stubResolvedReview{
			"call-resolved": {
				decision:        mcp.HookDecisionApprove,
				resolvedAt:      time.Date(2026, 3, 24, 14, 0, 0, 0, time.UTC),
				subscriberLease: "instance-owner/11",
			},
		},
	}
	store := &stubResolvedReviewStore{stubHookReviewStore: base}

	_, err := mustNewHookResolver(t, store).Resolve(context.Background(), mcp.LeaseKey{InstanceID: "instance-other", Generation: 12}, mcp.HookResolveRequest{
		HookCallID:     "call-resolved",
		Decision:       mcp.HookDecisionApprove,
		IdempotencyKey: "idem-resolved",
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want permission denied")
	}
	if !strings.Contains(err.Error(), contract.ErrHookReviewPermissionDenied.Error()) {
		t.Fatalf("Resolve() error = %v, want permission denied sentinel message", err)
	}
	if len(base.resolveCalls) != 0 {
		t.Fatalf("resolve calls = %d, want 0", len(base.resolveCalls))
	}
}
