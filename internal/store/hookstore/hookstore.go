package hookstore

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// 编译期确认 store 完整实现 HookReviewStore。
var _ contract.HookReviewStore = (*store)(nil)

type querier interface {
	SaveHookPendingReview(ctx context.Context, arg sqlc.SaveHookPendingReviewParams) error
	GetHookPendingReview(ctx context.Context, arg sqlc.GetHookPendingReviewParams) (sqlc.GetHookPendingReviewRow, error)
	ListHookPendingReviewsByAgent(ctx context.Context, arg sqlc.ListHookPendingReviewsByAgentParams) ([]sqlc.ListHookPendingReviewsByAgentRow, error)
	CheckHookReviewIdempotency(ctx context.Context, arg sqlc.CheckHookReviewIdempotencyParams) (int64, error)
	ResolveHookPendingReview(ctx context.Context, arg sqlc.ResolveHookPendingReviewParams) (int64, error)
	GetHookResolvedReview(ctx context.Context, arg sqlc.GetHookResolvedReviewParams) (sqlc.GetHookResolvedReviewRow, error)
	CancelHookPendingReviewsByLease(ctx context.Context, arg sqlc.CancelHookPendingReviewsByLeaseParams) (int64, error)
	CancelHookPendingReviewsByAgent(ctx context.Context, arg sqlc.CancelHookPendingReviewsByAgentParams) (int64, error)
	CancelExpiredHookReviews(ctx context.Context, arg sqlc.CancelExpiredHookReviewsParams) (int64, error)
	RecoverHookPendingReviews(ctx context.Context) ([]sqlc.RecoverHookPendingReviewsRow, error)
}

// store 通过 hook_pending_reviews 表保存 MCP hook 人工审批状态。
type store struct {
	q querier
}

// NewStore 创建基于 sqlc 的 hook review 存储。
// q 为 nil 时保留空实现实例，测试可注入自定义 querier 验证错误路径。
func NewStore(q *sqlc.Queries) contract.HookReviewStore {
	if q == nil {
		return &store{}
	}
	return &store{q: q}
}

func newStoreForTest(q querier) *store {
	return &store{q: q}
}

// SavePendingReview 保存等待人工决策的 hook review。
// hookCallID 是幂等和后续恢复的主键，deadline 用于超时自动落到默认动作。
func (s *store) SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error {
	err := s.q.SaveHookPendingReview(ctx, sqlc.SaveHookPendingReviewParams{
		HookCallID:      review.HookCallID,
		Topic:           review.Topic,
		AgentID:         review.AgentID,
		SubscriberLease: review.SubscriberLease,
		DefaultAction:   review.DefaultAction,
		CreatedAt:       toMS(review.CreatedAt),
		DeadlineAt:      toMS(review.DeadlineAt),
	})
	return wrapErr(err, "save")
}

// GetPendingReview 按 hookCallID 读取仍处于 pending 状态的 review。
// 未命中时转换为 contract.ErrHookReviewNotFound，避免上层依赖 sqlc 错误形态。
func (s *store) GetPendingReview(ctx context.Context, hookCallID string) (mcp.PendingHookReview, error) {
	row, err := s.q.GetHookPendingReview(ctx, sqlc.GetHookPendingReviewParams{HookCallID: hookCallID})
	if err != nil {
		if platformdb.IsNotFound(err) {
			err = contract.ErrHookReviewNotFound
		}
		return mcp.PendingHookReview{}, wrapErr(err, "get")
	}
	return pendingFromGet(row), nil
}

// ListPendingReviews 列出指定 agent 仍需处理的 hook review。
// 该列表只返回 pending 行，已取消和已解析记录不会重新推给订阅者。
func (s *store) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	rows, err := s.q.ListHookPendingReviewsByAgent(ctx, sqlc.ListHookPendingReviewsByAgentParams{AgentID: agentID})
	if err != nil {
		return nil, wrapErr(err, "list")
	}
	result := make([]mcp.PendingHookReview, 0, len(rows))
	for _, row := range rows {
		result = append(result, pendingFromList(row))
	}
	return result, nil
}

// ResolvePendingReview 写入人工决策并关闭 pending review。
// idempotencyKey 已存在时直接成功，确保重试不会覆盖第一次决策。
func (s *store) ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
	if _, err := s.q.CheckHookReviewIdempotency(ctx, sqlc.CheckHookReviewIdempotencyParams{
		HookCallID:     hookCallID,
		IdempotencyKey: idempotencyKey,
	}); err == nil {
		return nil
	} else if !platformdb.IsNotFound(err) {
		return wrapErr(err, "resolve.idempotency_check")
	}

	rows, err := s.q.ResolveHookPendingReview(ctx, sqlc.ResolveHookPendingReviewParams{
		HookCallID:     hookCallID,
		Decision:       decision,
		Reason:         reason,
		IdempotencyKey: idempotencyKey,
		ResolvedBy:     resolvedBy,
		ResolvedAt:     toMSPtr(time.Now().UTC()),
	})
	if err != nil {
		return wrapErr(err, "resolve")
	}
	if rows == 0 {
		return wrapErr(contract.ErrHookReviewNotFound, "resolve")
	}
	return nil
}

// GetResolvedReview 读取已解析 review 的最终决策和订阅租约。
// 返回值供 hook 调用方确认决策来源，并在缺失时统一映射为 not found。
func (s *store) GetResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, string, error) {
	row, err := s.q.GetHookResolvedReview(ctx, sqlc.GetHookResolvedReviewParams{HookCallID: hookCallID})
	if err != nil {
		if platformdb.IsNotFound(err) {
			err = contract.ErrHookReviewNotFound
		}
		return "", time.Time{}, "", wrapErr(err, "get_resolved")
	}
	return row.Decision, fromMSPtr(row.ResolvedAt), row.SubscriberLease, nil
}

// CancelPendingReviewsByLease 取消同一订阅租约下的 pending review。
// 订阅者断开时调用，避免旧租约的审批请求在恢复后继续阻塞。
func (s *store) CancelPendingReviewsByLease(ctx context.Context, subscriberLease string) (int, error) {
	rows, err := s.q.CancelHookPendingReviewsByLease(ctx, sqlc.CancelHookPendingReviewsByLeaseParams{
		SubscriberLease: subscriberLease,
		ResolvedAt:      toMSPtr(time.Now().UTC()),
	})
	if err != nil {
		return 0, wrapErr(err, "cancel_by_lease")
	}
	return int(rows), nil
}

// CancelPendingReviewsByAgent 取消指定 agent 的 pending review。
// agent 结束或重启时调用，返回实际关闭的行数供上层记录清理结果。
func (s *store) CancelPendingReviewsByAgent(ctx context.Context, agentID string) (int, error) {
	rows, err := s.q.CancelHookPendingReviewsByAgent(ctx, sqlc.CancelHookPendingReviewsByAgentParams{
		AgentID:    agentID,
		ResolvedAt: toMSPtr(time.Now().UTC()),
	})
	if err != nil {
		return 0, wrapErr(err, "cancel_by_agent")
	}
	return int(rows), nil
}

// CancelExpiredReviews 将已过 deadline 的 pending review 解析为默认动作。
// 返回值是本轮超时关闭的数量，调用方可用于启动恢复和定时清理指标。
func (s *store) CancelExpiredReviews(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	rows, err := s.q.CancelExpiredHookReviews(ctx, sqlc.CancelExpiredHookReviewsParams{
		ResolvedAt: toMSPtr(now),
		DeadlineAt: toMS(now),
	})
	if err != nil {
		return 0, wrapErr(err, "cancel_expired")
	}
	return int(rows), nil
}

// RecoverOnStartup 读取进程启动时仍处于 pending 的 review。
// 上层用它重新挂接订阅者可见状态，不会修改数据库中的 review 生命周期。
func (s *store) RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error) {
	rows, err := s.q.RecoverHookPendingReviews(ctx)
	if err != nil {
		return nil, wrapErr(err, "recover")
	}
	result := make([]mcp.PendingHookReview, 0, len(rows))
	for _, row := range rows {
		result = append(result, pendingFromRecover(row))
	}
	return result, nil
}

func pendingFromGet(row sqlc.GetHookPendingReviewRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		SubscriberLease: row.SubscriberLease,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func pendingFromList(row sqlc.ListHookPendingReviewsByAgentRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		SubscriberLease: row.SubscriberLease,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func pendingFromRecover(row sqlc.RecoverHookPendingReviewsRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		SubscriberLease: row.SubscriberLease,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func toMS(t time.Time) int64 {
	return platformdb.Millis(t)
}

func toMSPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := platformdb.Millis(t)
	return &v
}

func fromMS(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return platformdb.TimeFromMillis(ms)
}

func fromMSPtr(ms *int64) time.Time {
	if ms == nil {
		return time.Time{}
	}
	return fromMS(*ms)
}

func wrapErr(err error, op string) error {
	return platformdb.WrapStoreError(err, op, "hook_pending_review")
}
