package contract

import (
	"context"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// HookManager defines the core-layer hook infrastructure interface.
// Phase 1 uses a topic string to simplify signatures; Phase 2 extends to Selector struct.
type HookManager interface {
	Subscribe(ctx context.Context, lease mcp.LeaseKey, req mcp.HookSubscribeRequest) (mcp.HookSubscribeResponse, error)
	DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.BeforeDecision, error)
	DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.CheckDecision, error)
	DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error)
	Resolve(ctx context.Context, callerLease mcp.LeaseKey, req mcp.HookResolveRequest) (mcp.HookResolveResponse, error)
	GetPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error)
}

// HookLifecycle manages hook shutdown and cleanup events.
// Kept separate from HookManager to avoid inflating the protocol interface.
type HookLifecycle interface {
	// ShutdownHooks clears hook state for a lease during shutdown.
	// Order: Unsubscribe first to stop new callback fanout, then cancel pending reviews.
	ShutdownHooks(ctx context.Context, lease mcp.LeaseKey) error
}

// HookReviewStore defines the persistence interface for pending_hook_review.
// Implementations must reside in store/hookstore/; platform/hooks must not reference sqlc directly.
type HookReviewStore interface {
	SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error
	GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error)
	ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error)
	ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey string) error
	CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error)
	CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error)
	CancelExpiredReviews(ctx context.Context) (int, error)
	RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error)
}
