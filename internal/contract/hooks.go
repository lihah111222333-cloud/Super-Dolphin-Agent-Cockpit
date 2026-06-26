package contract

import (
	"context"
	"errors"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

var (
	ErrHookReviewPermissionDenied = errors.New("hook review permission denied")
	// ErrHookReviewNotFound 表示请求的 hook review 不存在。
	ErrHookReviewNotFound = errors.New("hook review not found")
)

// HookManager 是 core 层 hook 调度和人工 review 的跨模块边界。
// topic 是当前 wire 选择器；实现负责 before/check/after fanout、review resolve 和待审查询。
type HookManager interface {
	Subscribe(ctx context.Context, lease mcp.LeaseKey, req mcp.HookSubscribeRequest) (mcp.HookSubscribeResponse, error)
	DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.BeforeDecision, error)
	DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.CheckDecision, error)
	DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error)
	Resolve(ctx context.Context, callerLease mcp.LeaseKey, req mcp.HookResolveRequest) (mcp.HookResolveResponse, error)
	GetPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error)
}

// HookLifecycle 管理 hook 租约关闭时的清理动作。
// 它与 HookManager 分离，避免普通调度接口承担 shutdown 语义。
type HookLifecycle interface {
	// ShutdownHooks 清理租约下的 hook 状态；实现必须先停止新 fanout，再取消待审 review。
	ShutdownHooks(ctx context.Context, lease mcp.LeaseKey) error
}

// HookReviewStore 是 pending_hook_review 的持久化边界。
// platform/hooks 只依赖该接口，具体实现负责 sqlc 访问、过期取消和启动恢复。
type HookReviewStore interface {
	SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error
	GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error)
	GetResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, string, error)
	ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error)
	ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error
	CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error)
	CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error)
	CancelExpiredReviews(ctx context.Context) (int, error)
	RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error)
}
