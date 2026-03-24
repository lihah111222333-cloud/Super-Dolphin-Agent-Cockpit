package hooks

import (
	"context"
	"errors"
	"log"

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

// Compile-time interface check.
var _ contract.HookManager = (*Manager)(nil)
var _ contract.HookLifecycle = (*Manager)(nil)

func NewManager(registry *HookRegistry, dispatcher *HookDispatcher, resolver *HookResolver, opts ...ManagerOption) *Manager {
	manager := &Manager{
		registry:     registry,
		dispatcher:   dispatcher,
		resolver:     resolver,
		maxHookDepth: defaultMaxHookDepth,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	if err := manager.validate(); err != nil {
		panic(err)
	}
	return manager
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
	decisions, err := m.dispatcher.DispatchBefore(ctx, topic, payload)
	if err != nil {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, err
	}
	result := MergeBefore(decisions)
	m.handleLostLeases(ctx, result.LostLeases)
	return result.Decision, nil
}

func (m *Manager) DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.CheckDecision, error) {
	if err := m.validate(); err != nil {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, err
	}
	if m.depthExceeded(payload) {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, nil
	}
	decisions, err := m.dispatcher.DispatchCheck(ctx, topic, payload)
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

	leases, prepared := m.dispatcher.prepareDispatch(topic, payload)
	decisions, err := m.dispatcher.dispatchPreparedAfter(ctx, leases, prepared)
	if err != nil {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, err
	}

	result := MergeAfter(decisions)
	m.handleLostLeases(ctx, result.LostLeases)
	if result.Decision.Decision == mcp.HookDecisionEscalate {
		subscriberLease, ok := firstLeaseByAfterDecision(decisions, mcp.HookDecisionEscalate)
		if !ok {
			log.Printf("hooks manager: escalate %s missing subscriber lease", prepared.HookCallID)
		} else if _, escalateErr := m.resolver.Escalate(ctx, prepared.HookCallID, prepared, subscriberLease); escalateErr != nil {
			log.Printf("hooks manager: escalate %s failed: %v", prepared.HookCallID, escalateErr)
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
			log.Printf(
				"hooks manager: cancel pending reviews for lost subscriber failed lease=%s/%d err=%v",
				lease.InstanceID,
				lease.Generation,
				err,
			)
		}
		log.Printf(
			"hooks manager: subscriber_lost lease=%s/%d unsubscribed and cancelled pending reviews",
			lease.InstanceID,
			lease.Generation,
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
