package hooks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	logger     *slog.Logger
	defaultTTL time.Duration // pending review 默认 TTL，默认 5 分钟
}

type ResolverOption func(*HookResolver)

type resolvedReviewReader interface {
	GetResolvedReview(ctx context.Context, hookCallID string) (string, time.Time, error)
}

func WithResolverTTL(ttl time.Duration) ResolverOption {
	return func(r *HookResolver) {
		if r != nil && ttl > 0 {
			r.defaultTTL = ttl
		}
	}
}

func WithResolverLogger(logger *slog.Logger) ResolverOption {
	return func(r *HookResolver) {
		if r != nil && logger != nil {
			r.logger = logger
		}
	}
}

func NewHookResolver(store contract.HookReviewStore, opts ...ResolverOption) *HookResolver {
	if store == nil {
		panic(errNilHookReviewStore)
	}

	resolver := &HookResolver{
		store:      store,
		logger:     slog.Default(),
		defaultTTL: defaultHookReviewTTL,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(resolver)
		}
	}
	return resolver
}

// Escalate converts an after-phase escalate result into a durable pending review.
func (r *HookResolver) Escalate(ctx context.Context, hookCallID string, payload mcp.HookPayload, subscriberLease mcp.LeaseKey) (mcp.PendingHookReview, error) {
	if err := r.validate(); err != nil {
		return mcp.PendingHookReview{}, err
	}

	ctx = contextOrBackground(ctx)
	review, err := r.newPendingReview(time.Now().UTC(), hookCallID, payload, subscriberLease)
	if err != nil {
		return mcp.PendingHookReview{}, err
	}
	if err := r.store.SavePendingReview(ctx, review); err != nil {
		return mcp.PendingHookReview{}, err
	}
	return r.store.GetPendingReview(ctx, review.HookCallID)
}

// Resolve handles ctl/hook/resolve and converges the pending review idempotently.
// TODO(T1-5): validate callerLease owns this hook_call_id once subscriber_lease is persisted in pending review.
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
	if err := r.store.ResolvePendingReview(ctx, hookCallID, decision, strings.TrimSpace(req.Reason), idempotencyKey); err != nil {
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

// SweepExpired scans timed-out pending reviews and applies the default reject action.
func (r *HookResolver) SweepExpired(ctx context.Context) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}
	return r.store.CancelExpiredReviews(contextOrBackground(ctx))
}

// CancelByLease cancels all pending reviews for a specific subscriber lease during shutdown races.
func (r *HookResolver) CancelByLease(ctx context.Context, subscriberLease mcp.LeaseKey) (int, error) {
	if err := r.validate(); err != nil {
		return 0, err
	}

	resolvedSubscriberLease, err := formatSubscriberLease(subscriberLease)
	if err != nil {
		return 0, err
	}
	return r.store.CancelPendingReviewsByLease(contextOrBackground(ctx), resolvedSubscriberLease)
}

// CancelByAgent cancels all pending reviews for a given agent during shutdown races.
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

func (r *HookResolver) newPendingReview(now time.Time, hookCallID string, payload mcp.HookPayload, subscriberLease mcp.LeaseKey) (mcp.PendingHookReview, error) {
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
	resolvedSubscriberLease, err := formatSubscriberLease(subscriberLease)
	if err != nil {
		return mcp.PendingHookReview{}, err
	}

	return mcp.PendingHookReview{
		HookCallID:      resolvedHookCallID,
		Topic:           topic,
		AgentID:         agentID,
		SubscriberLease: resolvedSubscriberLease,
		CreatedAt:       now,
		DeadlineAt:      r.resolveDeadline(now, payload.DeadlineMs),
		DefaultAction:   pendingDefaultDecision,
	}, nil
}

func (r *HookResolver) resolveDeadline(now time.Time, deadlineMs int64) time.Time {
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

func formatSubscriberLease(lease mcp.LeaseKey) (string, error) {
	instanceID := strings.TrimSpace(lease.InstanceID)
	if instanceID == "" {
		return "", fmt.Errorf("hook escalate requires subscriber lease instance_id")
	}
	if lease.Generation == 0 {
		return "", fmt.Errorf("hook escalate requires subscriber lease generation")
	}
	return fmt.Sprintf("%s/%d", instanceID, lease.Generation), nil
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
	reader, ok := r.store.(resolvedReviewReader)
	if !ok {
		return "", time.Time{}
	}

	decision, resolvedAt, err := reader.GetResolvedReview(ctx, hookCallID)
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

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
