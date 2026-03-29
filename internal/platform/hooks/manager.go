package hooks

import (
	"context"
	"errors"
	"fmt"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const defaultMaxHookDepth = 3

var (
	errNilManagerRegistry   = errors.New("hooks manager registry is nil")
	errNilManagerDispatcher = errors.New("hooks manager dispatcher is nil")
	errNilManagerResolver   = errors.New("hooks manager resolver is nil")
)

// Manager implements contract.HookManager by composing registry, dispatcher, and resolver.
type Manager struct {
	registry     *HookRegistry
	dispatcher   *HookDispatcher
	resolver     *HookResolver
	logger       *pkglogger.Logger
	maxHookDepth int
}

type ManagerOption func(*Manager)

func WithMaxHookDepth(n int) ManagerOption {
	return func(m *Manager) {
		if m != nil && n > 0 {
			m.maxHookDepth = n
		}
	}
}

func WithManagerLogger(logger *pkglogger.Logger) ManagerOption {
	return func(m *Manager) {
		if m != nil && logger != nil {
			m.logger = logger
		}
	}
}

// Compile-time interface check.
var _ contract.HookManager = (*Manager)(nil)
var _ contract.HookLifecycle = (*Manager)(nil)

func NewManager(registry *HookRegistry, dispatcher *HookDispatcher, resolver *HookResolver, opts ...ManagerOption) (*Manager, error) {
	manager := &Manager{
		registry:     registry,
		dispatcher:   dispatcher,
		resolver:     resolver,
		logger:       pkglogger.Get(),
		maxHookDepth: defaultMaxHookDepth,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	if err := manager.validate(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Subscribe(_ context.Context, lease mcp.LeaseKey, req mcp.HookSubscribeRequest) (mcp.HookSubscribeResponse, error) {
	if err := m.validate(); err != nil {
		return mcp.HookSubscribeResponse{}, err
	}
	return m.registry.Subscribe(lease, req)
}

func (m *Manager) DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
	if err := m.validate(); err != nil {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, err
	}
	if m.depthExceeded(payload) {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, nil
	}
	decisions, err := m.dispatcher.dispatchBeforeBySelector(ctx, dispatchSelector(topic, payload), payload)
	if err != nil {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, err
	}
	result := MergeBefore(decisions)
	m.handleLostLeases(ctx, result.LostLeases)
	if result.PartialFailure {
		m.logger.Warn("hooks: partial before hook failure, denying request",
			"failed_leases", result.FailedLeases,
		)
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, nil
	}
	return result.Decision, nil
}

func (m *Manager) DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.CheckDecision, error) {
	if err := m.validate(); err != nil {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, err
	}
	if m.depthExceeded(payload) {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, nil
	}
	decisions, err := m.dispatcher.dispatchCheckBySelector(ctx, dispatchSelector(topic, payload), payload)
	if err != nil {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, err
	}
	result := MergeDuring(decisions)
	m.handleLostLeases(ctx, result.LostLeases)
	return result.Decision, nil
}

func (m *Manager) DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error) {
	if err := m.validate(); err != nil {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, err
	}
	if m.depthExceeded(payload) {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, nil
	}

	leases, prepared := m.dispatcher.prepareDispatchBySelector(dispatchSelector(topic, payload), payload)
	decisions, err := m.dispatcher.dispatchPreparedAfter(ctx, leases, prepared)
	if err != nil {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, err
	}

	result := MergeAfter(decisions)
	m.handleLostLeases(ctx, result.LostLeases)
	if result.PartialFailure {
		m.logger.Warn("hooks: partial after hook failure, keeping successful decision",
			"failed_leases", result.FailedLeases,
			"decision", result.Decision.Decision,
		)
	}
	if result.Decision.Decision == mcp.HookDecisionEscalate {
		subscriberLease, ok := firstLeaseByAfterDecision(decisions, mcp.HookDecisionEscalate)
		if !ok {
			err := fmt.Errorf("hooks: escalate decision for %s missing subscriber lease", prepared.HookCallID)
			m.logger.Error("hooks: escalate missing subscriber lease",
				"hook_call_id", prepared.HookCallID,
				"error", err,
			)
			return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, err
		} else if _, escalateErr := m.resolver.Escalate(ctx, prepared.HookCallID, prepared, subscriberLease, result.Decision.TTLMs); escalateErr != nil {
			err := fmt.Errorf("hooks: persist escalated review %s: %w", prepared.HookCallID, escalateErr)
			m.logger.Error("hooks: escalate failed",
				"hook_call_id", prepared.HookCallID,
				"error", err,
			)
			return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, err
		}
	}
	return result.Decision, nil
}

func (m *Manager) Resolve(ctx context.Context, callerLease mcp.LeaseKey, req mcp.HookResolveRequest) (mcp.HookResolveResponse, error) {
	if err := m.validate(); err != nil {
		return mcp.HookResolveResponse{}, err
	}
	return m.resolver.Resolve(ctx, callerLease, req)
}

func (m *Manager) GetPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m.resolver.ListPendingReviews(ctx, agentID)
}

func (m *Manager) ShutdownHooks(ctx context.Context, lease mcp.LeaseKey) error {
	if err := m.validate(); err != nil {
		return err
	}
	m.registry.Unsubscribe(lease)
	m.dispatcher.ForgetLease(lease)
	_, err := m.resolver.CancelByLease(ctx, lease)
	return err
}

func (m *Manager) handleLostLeases(ctx context.Context, leases []mcp.LeaseKey) {
	for _, lease := range leases {
		m.registry.Unsubscribe(lease)
		m.dispatcher.ForgetLease(lease)
		if _, err := m.resolver.CancelByLease(ctx, lease); err != nil {
			m.logger.Warn("hooks: cancel pending reviews for lost subscriber failed",
				"lease", lease,
				"error", err,
			)
		}
		m.logger.Info(
			"hooks: subscriber_lost, unsubscribed and cancelled pending reviews",
			"lease", lease,
		)
	}
}

func (m *Manager) depthExceeded(payload mcp.HookPayload) bool {
	return payload.Depth >= m.maxHookDepthOrDefault()
}

func (m *Manager) maxHookDepthOrDefault() int {
	if m == nil || m.maxHookDepth <= 0 {
		return defaultMaxHookDepth
	}
	return m.maxHookDepth
}

func (m *Manager) validate() error {
	if m == nil {
		return errNilManagerDispatcher
	}
	if m.registry == nil {
		return errNilManagerRegistry
	}
	if m.dispatcher == nil {
		return errNilManagerDispatcher
	}
	if m.resolver == nil {
		return errNilManagerResolver
	}
	return nil
}

func firstLeaseByAfterDecision(decisions []peerDecision[mcp.AfterDecision], want string) (mcp.LeaseKey, bool) {
	normalizedWant := normalizeAfterDecision(want)
	for _, item := range decisions {
		if item.Err != nil || item.Lease == (mcp.LeaseKey{}) {
			continue
		}
		if normalizeAfterDecision(item.Decision.Decision) == normalizedWant {
			return item.Lease, true
		}
	}
	return mcp.LeaseKey{}, false
}

func dispatchSelector(topic string, payload mcp.HookPayload) mcp.Selector {
	sel := mcp.Selector{Subscription: topic}
	scope := mcp.SelectorScope{
		AgentID:  strings.TrimSpace(payload.AgentID),
		ThreadID: strings.TrimSpace(payload.ThreadID),
	}
	if scope != (mcp.SelectorScope{}) {
		sel.Scope = &scope
	}
	return sel
}
