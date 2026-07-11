package hooks

import (
	"context"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

type resolvePendingReviewCall struct {
	hookCallID     string
	decision       string
	reason         string
	idempotencyKey string
	resolvedBy     string
}

type stubResolvedReview struct {
	decision        string
	resolvedAt      time.Time
	subscriberLease string
	err             error
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
	resolvePendingReviewFunc        func(context.Context, string, string, string, string, string) error
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
	review, ok := s.pending[hookCallID]
	if !ok {
		return mcp.PendingHookReview{}, contract.ErrHookReviewNotFound
	}
	return review, nil
}

func (s *stubHookReviewStore) GetResolvedReview(_ context.Context, hookCallID string) (string, time.Time, string, error) {
	if s.resolved == nil {
		return "", time.Time{}, "", contract.ErrHookReviewNotFound
	}
	review, ok := s.resolved[hookCallID]
	if !ok {
		return "", time.Time{}, "", contract.ErrHookReviewNotFound
	}
	return review.decision, review.resolvedAt, review.subscriberLease, review.err
}

func (s *stubHookReviewStore) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	s.listAgentIDs = append(s.listAgentIDs, agentID)
	if s.listPendingReviewsFunc != nil {
		return s.listPendingReviewsFunc(ctx, agentID)
	}
	return s.listPendingReviewsResult, nil
}

func (s *stubHookReviewStore) ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
	s.resolveCalls = append(s.resolveCalls, resolvePendingReviewCall{
		hookCallID:     hookCallID,
		decision:       decision,
		reason:         reason,
		idempotencyKey: idempotencyKey,
		resolvedBy:     resolvedBy,
	})
	if s.resolvePendingReviewFunc != nil {
		return s.resolvePendingReviewFunc(ctx, hookCallID, decision, reason, idempotencyKey, resolvedBy)
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
