package hooks

import (
	"context"
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

// HookResolver manages the escalate -> pending -> resolve lifecycle.
type HookResolver struct {
	store      contract.HookReviewStore
	logger     *pkglogger.Logger
	defaultTTL time.Duration // pending review 默认 TTL，默认 5 分钟
}

type ResolverOption func(*HookResolver)

// WithResolverTTL 设置解析器TTL。
func WithResolverTTL(ttl time.Duration) ResolverOption {
	return func(r *HookResolver) {
		if r != nil && ttl > 0 {
			r.defaultTTL = ttl
		}
	}
}

// WithResolverLogger 设置解析器日志器。
func WithResolverLogger(logger *pkglogger.Logger) ResolverOption {
	return func(r *HookResolver) {
		if r != nil && logger != nil {
			r.logger = logger
		}
	}
}

// NewHookResolver 创建hook解析器。
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

// Escalate converts an after-phase escalate result into a durable pending review.
// Escalate 处理escalate。
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

// Resolve handles ctl/hook/resolve and converges the pending review idempotently.
// Resolve 解析平台hooks。
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
	canonicalDecision, resolvedAt := r.loadResolvedReview(ctx, hookCallID, decision)
	if resolvedAt.IsZero() {
		r.logger.Warn("hooks resolver: readback failed, using fallback", "hook_call_id", hookCallID)
		canonicalDecision = decision
		resolvedAt = time.Now().UTC()
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

// SweepExpired scans timed-out pending reviews and applies the default reject action.
// SweepExpired 处理sweepexpired。
func (r *HookResolver) SweepExpired(ctx context.Context) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	return r.store.CancelExpiredReviews(contextOrBackground(ctx))
}

// CancelByLease cancels all pending reviews for a specific subscriber lease during shutdown races.
// CancelByLease 按租约处理cancel。
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

// CancelByAgent cancels all pending reviews for a given agent during shutdown races.
// CancelByAgent 按代理处理cancel。
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

// ListPendingReviews returns pending reviews for a specific agent in store order.
// ListPendingReviews 列出待处理reviews。
func (r *HookResolver) ListPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("hooks resolver: agentID is required")
	}
	return r.store.ListPendingReviews(contextOrBackground(ctx), agentID)
}

// RecoverOnStartup reloads pending reviews during process startup.
// RecoverOnStartup 恢复onstartup。
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
	resolvedSubscriberLease, err := formatLease(subscriberLease, hookSubscriberLeaseValidation)
	if err != nil {
		return mcp.PendingHookReview{}, err
	}

	return mcp.PendingHookReview{
		HookCallID:      resolvedHookCallID,
		Topic:           topic,
		AgentID:         agentID,
		SubscriberLease: resolvedSubscriberLease,
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

// resolveHookCallID 解析hookcallID。
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

func (r *HookResolver) loadResolvedReview(ctx context.Context, hookCallID, _ string) (string, time.Time) {
	decision, resolvedAt, _, err := r.readResolvedReview(ctx, hookCallID)
	if err != nil {
		return "", time.Time{}
	}
	if strings.TrimSpace(decision) == "" {
		return "", time.Time{}
	}
	if resolvedAt.IsZero() {
		return decision, time.Time{}
	}
	return decision, resolvedAt
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
