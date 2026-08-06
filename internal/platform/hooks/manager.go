package hooks

import (
	"context"
	"errors"
	"fmt"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const defaultMaxHookDepth = 3

var (
	errNilManagerRegistry   = errors.New("hooks manager registry is nil")
	errNilManagerDispatcher = errors.New("hooks manager dispatcher is nil")
	errNilManagerResolver   = errors.New("hooks manager resolver is nil")
)

// Manager 组合 registry、dispatcher 和 resolver 实现 hooks 对外入口。
// before/check/after 三阶段都从这里统一做深度保护、决策合并和失联 lease 清理。
type Manager struct {
	registry     *HookRegistry
	dispatcher   *HookDispatcher
	resolver     *HookResolver
	logger       *pkglogger.Logger
	maxHookDepth int
}

// ManagerOption 调整 Manager 的深度限制和日志依赖。
type ManagerOption func(*Manager)

// WithMaxHookDepth 设置 hook 递归派发最大深度。
func WithMaxHookDepth(n int) ManagerOption {
	return func(m *Manager) {
		if m != nil && n > 0 {
			m.maxHookDepth = n
		}
	}
}

// WithManagerLogger 注入 Manager 使用的结构化 logger。
func WithManagerLogger(logger *pkglogger.Logger) ManagerOption {
	return func(m *Manager) {
		if m != nil && logger != nil {
			m.logger = logger
		}
	}
}

// Manager 必须同时满足 hook RPC 入口和生命周期清理接口。
var _ contract.HookManager = (*Manager)(nil)
var _ contract.HookLifecycle = (*Manager)(nil)

// NewManager 创建 hooks Manager 并校验三项核心依赖。
// registry、dispatcher、resolver 任一缺失都会立即失败，避免订阅、fanout 或审批持久化只在部分路径可用。
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

// Subscribe 注册或更新 peer 的 hook 订阅。
func (m *Manager) Subscribe(_ context.Context, lease mcp.LeaseKey, req mcp.HookSubscribeRequest) (mcp.HookSubscribeResponse, error) {
	if err := m.validate(); err != nil {
		return mcp.HookSubscribeResponse{}, err
	}
	return m.registry.Subscribe(lease, req)
}

// DispatchBefore 执行 before 阶段决策，部分 peer 失败时按 fail-closed 拒绝请求。
func (m *Manager) DispatchBefore(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.BeforeDecision, error) {
	return runPhase(ctx, m, topic, payload, phaseSpec[mcp.BeforeDecision]{
		defaultDecision: mcp.BeforeDecision{Decision: mcp.HookDecisionDeny},
		dispatch: func(ctx context.Context, topic string, payload mcp.HookPayload) (phaseDispatchResult[mcp.BeforeDecision], error) {
			decisions, err := m.dispatcher.dispatchBeforeBySelector(ctx, buildDispatchSelector(topic, payload), payload)
			return phaseDispatchResult[mcp.BeforeDecision]{decisions: decisions}, err
		},
		merge: MergeBefore,
		finalize: func(_ context.Context, _ phaseDispatchResult[mcp.BeforeDecision], result MergeResult[mcp.BeforeDecision]) (mcp.BeforeDecision, error) {
			if result.PartialFailure {
				m.logger.Warn("hooks: partial before hook failure, denying request",
					"failed_leases", result.FailedLeases,
				)
				return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, nil
			}
			return result.Decision, nil
		},
	})
}

// DispatchCheck 执行 check 阶段决策，默认允许继续，peer 可升级为 warn/abort。
func (m *Manager) DispatchCheck(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.CheckDecision, error) {
	return runPhase(ctx, m, topic, payload, phaseSpec[mcp.CheckDecision]{
		defaultDecision: mcp.CheckDecision{Decision: mcp.HookDecisionContinue},
		dispatch: func(ctx context.Context, topic string, payload mcp.HookPayload) (phaseDispatchResult[mcp.CheckDecision], error) {
			decisions, err := m.dispatcher.dispatchCheckBySelector(ctx, buildDispatchSelector(topic, payload), payload)
			return phaseDispatchResult[mcp.CheckDecision]{decisions: decisions}, err
		},
		merge: MergeDuring,
	})
}

// DispatchAfter 执行 after 阶段决策，并在 escalate 时持久化待审批记录。
// 无法定位触发 escalate 的订阅 lease 时按 reject 返回，避免产生无人可解的 pending review。
func (m *Manager) DispatchAfter(ctx context.Context, topic string, payload mcp.HookPayload) (mcp.AfterDecision, error) {
	return runPhase(ctx, m, topic, payload, phaseSpec[mcp.AfterDecision]{
		defaultDecision: mcp.AfterDecision{Decision: mcp.HookDecisionReject},
		dispatch: func(ctx context.Context, topic string, payload mcp.HookPayload) (phaseDispatchResult[mcp.AfterDecision], error) {
			leases, prepared := m.dispatcher.prepareDispatchBySelector(buildDispatchSelector(topic, payload), payload)
			decisions, err := m.dispatcher.dispatchPreparedAfter(ctx, leases, prepared)
			return phaseDispatchResult[mcp.AfterDecision]{
				decisions: decisions,
				payload:   prepared,
			}, err
		},
		merge: MergeAfter,
		finalize: func(ctx context.Context, dispatched phaseDispatchResult[mcp.AfterDecision], result MergeResult[mcp.AfterDecision]) (mcp.AfterDecision, error) {
			if result.PartialFailure {
				m.logger.Warn("hooks: partial after hook failure, keeping successful decision",
					"failed_leases", result.FailedLeases,
					"decision", result.Decision.Decision,
				)
			}
			if result.Decision.Decision != mcp.HookDecisionEscalate {
				return result.Decision, nil
			}

			afterConfig := afterDecisionConfig()
			subscriber, ok := firstMatching(dispatched.decisions, func(item peerDecision[mcp.AfterDecision]) bool {
				return item.Err == nil &&
					item.Lease != (mcp.LeaseKey{}) &&
					normalizeDecision(item.Decision.Decision, afterConfig) == mcp.HookDecisionEscalate
			})
			if !ok {
				err := fmt.Errorf("hooks: escalate decision for %s missing subscriber lease", dispatched.payload.HookCallID)
				m.logger.Error("hooks: escalate missing subscriber lease",
					"hook_call_id", dispatched.payload.HookCallID,
					"error", err,
				)
				return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, err
			}
			if _, err := m.resolver.Escalate(ctx, dispatched.payload.HookCallID, dispatched.payload, subscriber.Lease, result.Decision.TTLMs); err != nil {
				wrapped := fmt.Errorf("hooks: persist escalated review %s: %w", dispatched.payload.HookCallID, err)
				m.logger.Error("hooks: escalate failed",
					"hook_call_id", dispatched.payload.HookCallID,
					"error", wrapped,
				)
				return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, wrapped
			}
			return result.Decision, nil
		},
	})
}

// Resolve 处理 ctl/hook/resolve 请求，并把权限和幂等收敛交给 resolver。
func (m *Manager) Resolve(ctx context.Context, callerLease mcp.LeaseKey, req mcp.HookResolveRequest) (mcp.HookResolveResponse, error) {
	if err := m.validate(); err != nil {
		return mcp.HookResolveResponse{}, err
	}
	return m.resolver.Resolve(ctx, callerLease, req)
}

// GetPendingReviews 读取指定 agent 当前待处理的 hook 审批。
func (m *Manager) GetPendingReviews(ctx context.Context, agentID string) ([]mcp.PendingHookReview, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	return m.resolver.ListPendingReviews(ctx, agentID)
}

// GetPendingReviewsPage 读取指定 agent 的有界 hook 审批页。
func (m *Manager) GetPendingReviewsPage(ctx context.Context, params contract.HookPendingReviewPageParams) (contract.HookPendingReviewPage, error) {
	if err := m.validate(); err != nil {
		return contract.HookPendingReviewPage{}, err
	}
	return m.resolver.ListPendingReviewsPage(ctx, params)
}

// ShutdownHooks 在 peer lease 关闭时取消订阅并清理其 pending review。
func (m *Manager) ShutdownHooks(ctx context.Context, lease mcp.LeaseKey) error {
	if err := m.validate(); err != nil {
		return err
	}
	return cleanupSubscriberLease(ctx, m, lease)
}

func (m *Manager) handleLostLeases(ctx context.Context, leases []mcp.LeaseKey) {
	for _, lease := range leases {
		if err := cleanupSubscriberLease(ctx, m, lease); err != nil {
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
