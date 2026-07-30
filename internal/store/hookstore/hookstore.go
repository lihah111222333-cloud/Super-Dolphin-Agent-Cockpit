package hookstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

// 编译期确认分页包装 store 完整实现 HookReviewStore 和有界分页能力。
var _ contract.HookReviewStore = (*pagedStore)(nil)
var _ contract.HookPendingReviewPager = (*pagedStore)(nil)

type querier interface {
	SaveHookPendingReview(ctx context.Context, arg sqlc.SaveHookPendingReviewParams) (int64, error)
	GetHookPendingReviewForSave(ctx context.Context, arg sqlc.GetHookPendingReviewForSaveParams) (sqlc.GetHookPendingReviewForSaveRow, error)
	GetHookPendingReview(ctx context.Context, arg sqlc.GetHookPendingReviewParams) (sqlc.GetHookPendingReviewRow, error)
	ResolveHookPendingReview(ctx context.Context, arg sqlc.ResolveHookPendingReviewParams) (int64, error)
	GetHookResolvedReview(ctx context.Context, arg sqlc.GetHookResolvedReviewParams) (sqlc.GetHookResolvedReviewRow, error)
	CancelHookPendingReviewsByLease(ctx context.Context, arg sqlc.CancelHookPendingReviewsByLeaseParams) (int64, error)
	CancelHookPendingReviewsByAgent(ctx context.Context, arg sqlc.CancelHookPendingReviewsByAgentParams) (int64, error)
	CancelExpiredHookReviews(ctx context.Context, arg sqlc.CancelExpiredHookReviewsParams) (int64, error)
	RecoverHookPendingReviews(ctx context.Context) ([]sqlc.RecoverHookPendingReviewsRow, error)
}

type pageQuerier interface {
	ListHookPendingReviewsByAgentPage(ctx context.Context, arg sqlc.ListHookPendingReviewsByAgentPageParams) ([]sqlc.ListHookPendingReviewsByAgentPageRow, error)
	CountHookPendingReviews(ctx context.Context) (int64, error)
}

// store 通过 hook_pending_reviews 表保存 MCP hook 人工审批状态。
type store struct {
	q querier
}

type pagedStore struct {
	*store
	pages pageQuerier
}

// NewStore 创建基于 sqlc 的 hook review 存储。
// q 为 nil 时保留空实现实例，测试可注入自定义 querier 验证错误路径。
func NewStore(q *sqlc.Queries) contract.HookReviewStore {
	if q == nil {
		return &pagedStore{store: &store{}}
	}
	return &pagedStore{store: &store{q: q}, pages: q}
}

func newStoreForTest(q querier, pages pageQuerier) *pagedStore {
	return &pagedStore{store: &store{q: q}, pages: pages}
}

// SavePendingReview 保存等待人工决策的 hook review。
// hookCallID 是幂等和后续恢复的主键，deadline 用于超时自动落到默认动作。
func (s *store) SavePendingReview(ctx context.Context, review mcp.PendingHookReview) error {
	payload, err := normalizeHookReviewPayload(review.Payload)
	if err != nil {
		return wrapErr(err, "save.validate")
	}
	rows, err := s.q.SaveHookPendingReview(ctx, sqlc.SaveHookPendingReviewParams{
		HookCallID:      review.HookCallID,
		Topic:           review.Topic,
		AgentID:         review.AgentID,
		ThreadID:        review.ThreadID,
		TurnID:          review.TurnID,
		SubscriberLease: review.SubscriberLease,
		Payload:         payload,
		DefaultAction:   review.DefaultAction,
		CreatedAt:       toMS(review.CreatedAt),
		DeadlineAt:      toMS(review.DeadlineAt),
	})
	if err != nil {
		return wrapErr(err, "save")
	}
	if rows > 0 {
		return nil
	}
	existing, err := s.q.GetHookPendingReviewForSave(ctx, sqlc.GetHookPendingReviewForSaveParams{HookCallID: review.HookCallID})
	if err != nil {
		return wrapErr(err, "save.conflict_read")
	}
	if hookPendingReviewSaveParamsMatch(existing, review, payload) {
		return nil
	}
	return wrapErr(fmt.Errorf("%w: hook_call_id=%s", contract.ErrHookReviewConflict, review.HookCallID), "save.conflict")
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
func (s *pagedStore) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	page, err := listPendingReviewsPage(ctx, s.pages, contract.HookPendingReviewPageParams{
		AgentID: agentID,
		Limit:   contract.HookPendingReviewMaxPageLimit,
	})
	if err != nil {
		return nil, err
	}
	return page.Reviews, nil
}

// ListPendingReviewsPage 按 agent 和 cursor 有界读取 pending hook review。
// limit 缺失会立即报错，超过生产上限时会在下沉 SQL 前裁剪。
func (s *pagedStore) ListPendingReviewsPage(ctx context.Context, params contract.HookPendingReviewPageParams) (contract.HookPendingReviewPage, error) {
	return listPendingReviewsPage(ctx, s.pages, params)
}

// CountPendingReviews 统计仍处于 pending 状态的 hook review 行数。
func (s *pagedStore) CountPendingReviews(ctx context.Context) (int64, error) {
	count, err := s.pages.CountHookPendingReviews(ctx)
	if err != nil {
		return 0, wrapErr(err, "count_pending")
	}
	return count, nil
}

// ResolvePendingReview 写入人工决策并关闭 pending review。
// pending 写入与同幂等键重试由同一条条件 UPDATE 原子裁决，避免并发预检竞态。
func (s *store) ResolvePendingReview(ctx context.Context, hookCallID, decision, reason, idempotencyKey, resolvedBy string) error {
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
		ThreadID:        row.ThreadID,
		TurnID:          row.TurnID,
		SubscriberLease: row.SubscriberLease,
		Payload:         row.Payload,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func pendingFromListPage(row sqlc.ListHookPendingReviewsByAgentPageRow) mcp.PendingHookReview {
	return mcp.PendingHookReview{
		HookCallID:      row.HookCallID,
		Topic:           row.Topic,
		AgentID:         row.AgentID,
		ThreadID:        row.ThreadID,
		TurnID:          row.TurnID,
		SubscriberLease: row.SubscriberLease,
		Payload:         row.Payload,
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
		ThreadID:        row.ThreadID,
		TurnID:          row.TurnID,
		SubscriberLease: row.SubscriberLease,
		Payload:         row.Payload,
		DefaultAction:   row.DefaultAction,
		CreatedAt:       fromMS(row.CreatedAt),
		DeadlineAt:      fromMS(row.DeadlineAt),
	}
}

func normalizeHookReviewPayload(payload json.RawMessage) (json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("hook pending review payload is required")
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, payload); err != nil {
		return nil, fmt.Errorf("hook pending review payload must be valid JSON: %w", err)
	}
	return json.RawMessage(compacted.Bytes()), nil
}

// hookPendingReviewSaveParamsMatch 校验重复保存是否真的是同参幂等。
// 任何会改变复核上下文、租约、默认动作或 payload 的冲突都不能被 ON CONFLICT 吞掉。
func hookPendingReviewSaveParamsMatch(row sqlc.GetHookPendingReviewForSaveRow, review mcp.PendingHookReview, payload json.RawMessage) bool {
	if row.Status != "pending" {
		return false
	}
	existingPayload, err := normalizeHookReviewPayload(row.Payload)
	if err != nil {
		return false
	}
	return row.HookCallID == review.HookCallID &&
		row.Topic == review.Topic &&
		row.AgentID == review.AgentID &&
		row.ThreadID == review.ThreadID &&
		row.TurnID == review.TurnID &&
		row.SubscriberLease == review.SubscriberLease &&
		row.DefaultAction == review.DefaultAction &&
		bytes.Equal(existingPayload, payload)
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

func listPendingReviewsPage(ctx context.Context, q pageQuerier, params contract.HookPendingReviewPageParams) (contract.HookPendingReviewPage, error) {
	params, err := normalizeHookPendingReviewPageParams(params)
	if err != nil {
		return contract.HookPendingReviewPage{}, wrapErr(err, "list_page.validate")
	}
	rows, err := q.ListHookPendingReviewsByAgentPage(ctx, sqlc.ListHookPendingReviewsByAgentPageParams{
		AgentID:          params.AgentID,
		CursorCreatedAt:  toMS(params.CursorCreatedAt),
		CursorHookCallID: params.CursorHookCallID,
		Limit:            params.Limit,
	})
	if err != nil {
		return contract.HookPendingReviewPage{}, wrapErr(err, "list_page")
	}
	return pendingPageFromRows(rows, params.Limit), nil
}

// normalizeHookPendingReviewPageParams 规范化分页参数并校验 agent、limit 与 cursor 的组合约束。
func normalizeHookPendingReviewPageParams(params contract.HookPendingReviewPageParams) (contract.HookPendingReviewPageParams, error) {
	params.AgentID = strings.TrimSpace(params.AgentID)
	params.CursorHookCallID = strings.TrimSpace(params.CursorHookCallID)
	if params.AgentID == "" {
		return params, fmt.Errorf("hook pending review agentID is required")
	}
	if params.Limit <= 0 {
		return params, fmt.Errorf("hook pending review limit is required")
	}
	if params.Limit > contract.HookPendingReviewMaxPageLimit {
		params.Limit = contract.HookPendingReviewMaxPageLimit
	}
	if params.CursorHookCallID == "" && !params.CursorCreatedAt.IsZero() {
		return params, fmt.Errorf("hook pending review cursor requires hook call ID")
	}
	return params, nil
}

func pendingPageFromRows(rows []sqlc.ListHookPendingReviewsByAgentPageRow, limit int) contract.HookPendingReviewPage {
	pageRows := rows
	hasMore := len(rows) > limit
	if hasMore {
		pageRows = rows[:limit]
	}
	page := contract.HookPendingReviewPage{
		Reviews:        make([]mcp.PendingHookReview, 0, len(pageRows)),
		HasMore:        hasMore,
		EffectiveLimit: limit,
	}
	for _, row := range pageRows {
		page.Reviews = append(page.Reviews, pendingFromListPage(row))
	}
	if hasMore && len(pageRows) > 0 {
		last := pageRows[len(pageRows)-1]
		page.NextCursorCreatedAt = fromMS(last.CreatedAt)
		page.NextCursorHookCallID = last.HookCallID
	}
	return page
}

func wrapErr(err error, op string) error {
	return platformdb.WrapStoreError(err, op, "hook_pending_review")
}
