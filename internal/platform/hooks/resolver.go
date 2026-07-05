package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const (
	defaultHookReviewTTL   = 5 * time.Minute
	pendingStateResolved   = "resolved"
	pendingDefaultDecision = mcp.HookDecisionReject
)

var (
	errNilHookResolver    = errors.New("hooks resolver is nil")
	errNilHookReviewStore = errors.New("hooks review store is nil")
)

// HookResolver 管理 after escalate 到 pending review 再到 resolve 的持久化流程。
// 所有权限检查都以 subscriber lease 为准，避免非触发方确认或拒绝审批。
type HookResolver struct {
	store      contract.HookReviewStore
	logger     *pkglogger.Logger
	defaultTTL time.Duration // pending review 默认 TTL，默认 5 分钟
}

// ResolverOption 调整 HookResolver 的 TTL 和日志依赖。
type ResolverOption func(*HookResolver)

// WithResolverTTL 设置 pending review 默认过期时间。
func WithResolverTTL(ttl time.Duration) ResolverOption {
	return func(r *HookResolver) {
		if r != nil && ttl > 0 {
			r.defaultTTL = ttl
		}
	}
}

// WithResolverLogger 注入 resolver 使用的结构化 logger。
func WithResolverLogger(logger *pkglogger.Logger) ResolverOption {
	return func(r *HookResolver) {
		if r != nil && logger != nil {
			r.logger = logger
		}
	}
}

// NewHookResolver 创建 HookResolver 并校验持久化 store。
func NewHookResolver(store contract.HookReviewStore, opts ...ResolverOption) (*HookResolver, error) {
	if store == nil {
		return nil, errNilHookReviewStore
	}

	resolver := &HookResolver{
		store:      store,
		logger:     pkglogger.Get(),
		defaultTTL: defaultHookReviewTTL,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(resolver)
		}
	}
	return resolver, nil
}

// Escalate 将 after 阶段的 escalate 决策保存为 pending review。
// topic、agentID、hook_call_id 和 subscriber lease 都必须完整，缺失时立即报错而不是创建不可处理的审批。
func (r *HookResolver) Escalate(ctx context.Context, hookCallID string, payload mcp.HookPayload, subscriberLease mcp.LeaseKey, ttlMs int64) (mcp.PendingHookReview, error) {
	if err := r.validate(); err != nil {
		return mcp.PendingHookReview{}, err
	}

	ctx = contextOrBackground(ctx)
	review, err := r.newPendingReview(time.Now().UTC(), hookCallID, payload, subscriberLease, ttlMs)
	if err != nil {
		return mcp.PendingHookReview{}, err
	}
	if err := r.store.SavePendingReview(ctx, review); err != nil {
		return mcp.PendingHookReview{}, err
	}
	return r.store.GetPendingReview(ctx, review.HookCallID)
}

// Resolve 处理 ctl/hook/resolve 并保证幂等收敛。
// 调用方 lease 必须匹配原 subscriber lease；已解析记录会回读 canonical decision 作为响应。
func (r *HookResolver) Resolve(ctx context.Context, callerLease mcp.LeaseKey, req mcp.HookResolveRequest) (mcp.HookResolveResponse, error) {
	if err := r.validate(); err != nil {
		return mcp.HookResolveResponse{}, err
	}

	hookCallID := strings.TrimSpace(req.HookCallID)
	if hookCallID == "" {
		return mcp.HookResolveResponse{}, fmt.Errorf("hook resolve requires hook_call_id")
	}
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if idempotencyKey == "" {
		return mcp.HookResolveResponse{}, fmt.Errorf("hook resolve requires idempotency_key")
	}
	decision, err := normalizeResolveDecision(req.Decision)
	if err != nil {
		return mcp.HookResolveResponse{}, err
	}

	ctx = contextOrBackground(ctx)
	if err := r.validateResolveLease(ctx, hookCallID, callerLease); err != nil {
		return mcp.HookResolveResponse{}, err
	}
	if err := r.store.ResolvePendingReview(
		ctx,
		hookCallID,
		decision,
		strings.TrimSpace(req.Reason),
		idempotencyKey,
		strings.TrimSpace(req.ResolvedBy),
	); err != nil {
		return mcp.HookResolveResponse{}, err
	}
	canonicalDecision, resolvedAt, _, err := r.readResolvedReview(ctx, hookCallID)
	if err != nil {
		return mcp.HookResolveResponse{}, fmt.Errorf("hook resolve readback failed for %q: %w", hookCallID, err)
	}
	if strings.TrimSpace(canonicalDecision) == "" {
		return mcp.HookResolveResponse{}, fmt.Errorf("hook resolve readback returned empty decision for %q", hookCallID)
	}
	if resolvedAt.IsZero() {
		return mcp.HookResolveResponse{}, fmt.Errorf("hook resolve readback returned empty resolved_at for %q", hookCallID)
	}

	return mcp.HookResolveResponse{
		Accepted:          true,
		ResolvedAt:        resolvedAt.Format(time.RFC3339Nano),
		CanonicalDecision: canonicalDecision,
		PendingState:      pendingStateResolved,
	}, nil
}

func (r *HookResolver) validateResolveLease(ctx context.Context, hookCallID string, callerLease mcp.LeaseKey) error {
	review, err := r.store.GetPendingReview(ctx, hookCallID)
	if err != nil {
		if errors.Is(err, contract.ErrHookReviewNotFound) {
			return r.validateResolvedReviewLease(ctx, hookCallID, callerLease)
		}
		return err
	}

	return validateSubscriberLeaseOwner(hookCallID, review.SubscriberLease, callerLease)
}

func (r *HookResolver) validateResolvedReviewLease(ctx context.Context, hookCallID string, callerLease mcp.LeaseKey) error {
	_, _, subscriberLease, err := r.readResolvedReview(ctx, hookCallID)
	if err != nil {
		if errors.Is(err, contract.ErrHookReviewNotFound) {
			return fmt.Errorf("hook review %q not found while validating resolved lease: %w", hookCallID, err)
		}
		return err
	}
	return validateSubscriberLeaseOwner(hookCallID, subscriberLease, callerLease)
}

func validateSubscriberLeaseOwner(hookCallID, storedSubscriberLease string, callerLease mcp.LeaseKey) error {
	callerSubscriberLease, err := formatLease(callerLease, hookSubscriberLeaseValidation)
	if err != nil {
		return err
	}
	storedSubscriberLease = strings.TrimSpace(storedSubscriberLease)
	if storedSubscriberLease == callerSubscriberLease {
		return nil
	}
	return fmt.Errorf(
		"%w: hook_call_id=%s caller=%s subscriber=%s",
		contract.ErrHookReviewPermissionDenied,
		hookCallID,
		callerSubscriberLease,
		storedSubscriberLease,
	)
}

// SweepExpired 扫描过期 pending review 并按默认 reject 收敛。
func (r *HookResolver) SweepExpired(ctx context.Context) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	return r.store.CancelExpiredReviews(contextOrBackground(ctx))
}

// CancelByLease 在 subscriber lease 关闭时取消它名下的 pending review。
// lease 会先格式化校验，避免错误 lease 字符串误删其他订阅的审批。
func (r *HookResolver) CancelByLease(ctx context.Context, subscriberLease mcp.LeaseKey) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}

	resolvedSubscriberLease, err := formatLease(subscriberLease, hookSubscriberLeaseValidation)
	if err != nil {
		return 0, err
	}
	return r.store.CancelPendingReviewsByLease(contextOrBackground(ctx), resolvedSubscriberLease)
}

// CancelByAgent 在 agent 关闭或恢复竞态中取消该 agent 的 pending review。
func (r *HookResolver) CancelByAgent(ctx context.Context, agentID string) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return 0, fmt.Errorf("hook cancel requires agent_id")
	}
	return r.store.CancelPendingReviewsByAgent(contextOrBackground(ctx), agentID)
}

// ListPendingReviews 按 store 顺序读取指定 agent 的 pending review。
func (r *HookResolver) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	page, err := r.ListPendingReviewsPage(ctx, contract.HookPendingReviewPageParams{
		AgentID: agentID,
		Limit:   contract.HookPendingReviewMaxPageLimit,
	})
	if err != nil {
		return nil, err
	}
	return page.Reviews, nil
}

// ListPendingReviewsPage 按 agent 显式分页读取 pending review。
func (r *HookResolver) ListPendingReviewsPage(ctx context.Context, params contract.HookPendingReviewPageParams) (contract.HookPendingReviewPage, error) {
	if err := r.validate(); err != nil {
		return contract.HookPendingReviewPage{}, err
	}
	params.AgentID = strings.TrimSpace(params.AgentID)
	params.CursorHookCallID = strings.TrimSpace(params.CursorHookCallID)
	if params.AgentID == "" {
		return contract.HookPendingReviewPage{}, fmt.Errorf("hooks resolver: agentID is required")
	}
	if params.Limit <= 0 {
		return contract.HookPendingReviewPage{}, fmt.Errorf("hooks resolver: limit is required")
	}
	if params.Limit > contract.HookPendingReviewMaxPageLimit {
		return contract.HookPendingReviewPage{}, fmt.Errorf("hooks resolver: limit exceeds maximum: %d > %d", params.Limit, contract.HookPendingReviewMaxPageLimit)
	}
	pager, err := r.pendingReviewPager()
	if err != nil {
		return contract.HookPendingReviewPage{}, err
	}
	return pager.ListPendingReviewsPage(contextOrBackground(ctx), params)
}

// CountPendingReviews 统计启动恢复时仍待处理的 hook review 数量。
func (r *HookResolver) CountPendingReviews(ctx context.Context) (int64, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	pager, err := r.pendingReviewPager()
	if err != nil {
		return 0, err
	}
	return pager.CountPendingReviews(contextOrBackground(ctx))
}

// RecoverOnStartup 在进程启动时重载未完成的 pending review。
func (r *HookResolver) RecoverOnStartup(ctx context.Context) ([]mcp.PendingHookReview, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r.store.RecoverOnStartup(contextOrBackground(ctx))
}

func (r *HookResolver) validate() error {
	if r == nil {
		return errNilHookResolver
	}
	if r.store == nil {
		return errNilHookReviewStore
	}
	return nil
}

func (r *HookResolver) pendingReviewPager() (contract.HookPendingReviewPager, error) {
	pager, ok := r.store.(contract.HookPendingReviewPager)
	if !ok {
		return nil, fmt.Errorf("hooks resolver: pending review pager is required")
	}
	return pager, nil
}

// newPendingReview 构造需要人工复核的 hook 记录，并把线程、轮次和原始 payload 一起落库。
// 这些上下文字段缺失时直接报错，避免依赖数据库默认值吞掉复核来源。
func (r *HookResolver) newPendingReview(now time.Time, hookCallID string, payload mcp.HookPayload, subscriberLease mcp.LeaseKey, ttlMs int64) (mcp.PendingHookReview, error) {
	resolvedHookCallID, err := resolveHookCallID(hookCallID, payload.HookCallID)
	if err != nil {
		return mcp.PendingHookReview{}, err
	}

	topic := strings.TrimSpace(payload.Topic)
	if topic == "" {
		return mcp.PendingHookReview{}, fmt.Errorf("hook escalate requires topic")
	}
	agentID := strings.TrimSpace(payload.AgentID)
	if agentID == "" {
		return mcp.PendingHookReview{}, fmt.Errorf("hook escalate requires agent_id")
	}
	threadID := strings.TrimSpace(payload.ThreadID)
	if threadID == "" {
		return mcp.PendingHookReview{}, fmt.Errorf("hook escalate requires thread_id")
	}
	turnID := strings.TrimSpace(payload.TurnID)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return mcp.PendingHookReview{}, fmt.Errorf("marshal hook pending review payload: %w", err)
	}
	resolvedSubscriberLease, err := formatLease(subscriberLease, hookSubscriberLeaseValidation)
	if err != nil {
		return mcp.PendingHookReview{}, err
	}

	return mcp.PendingHookReview{
		HookCallID:      resolvedHookCallID,
		Topic:           topic,
		AgentID:         agentID,
		ThreadID:        threadID,
		TurnID:          turnID,
		SubscriberLease: resolvedSubscriberLease,
		Payload:         payloadJSON,
		CreatedAt:       now,
		DeadlineAt:      r.resolveDeadline(now, payload.DeadlineMs, ttlMs),
		DefaultAction:   pendingDefaultDecision,
	}, nil
}

func (r *HookResolver) resolveDeadline(now time.Time, deadlineMs int64, ttlMs int64) time.Time {
	if ttlMs > 0 {
		return now.Add(time.Duration(ttlMs) * time.Millisecond)
	}
	if deadlineMs > 0 {
		return time.UnixMilli(deadlineMs).UTC()
	}
	return now.Add(r.reviewTTL())
}

func (r *HookResolver) reviewTTL() time.Duration {
	if r == nil || r.defaultTTL <= 0 {
		return defaultHookReviewTTL
	}
	return r.defaultTTL
}

// resolveHookCallID 在显式参数和 payload 字段之间收敛唯一 hook_call_id。
// 两边同时提供且不一致时直接报错，避免审批记录和回调载荷使用不同主键。
func resolveHookCallID(explicitHookCallID, payloadHookCallID string) (string, error) {
	explicitHookCallID = strings.TrimSpace(explicitHookCallID)
	payloadHookCallID = strings.TrimSpace(payloadHookCallID)

	switch {
	case explicitHookCallID == "" && payloadHookCallID == "":
		return "", fmt.Errorf("hook escalate requires hook_call_id")
	case explicitHookCallID == "":
		return payloadHookCallID, nil
	case payloadHookCallID == "":
		return explicitHookCallID, nil
	case explicitHookCallID != payloadHookCallID:
		return "", fmt.Errorf("hook escalate hook_call_id mismatch: %q != %q", explicitHookCallID, payloadHookCallID)
	default:
		return explicitHookCallID, nil
	}
}

func normalizeResolveDecision(decision string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(decision)); normalized {
	case mcp.HookDecisionApprove, mcp.HookDecisionReject:
		return normalized, nil
	default:
		return "", fmt.Errorf("hook resolve decision must be approve or reject")
	}
}

func (r *HookResolver) readResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, string, error) {
	return r.store.GetResolvedReview(ctx, hookCallID)
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
