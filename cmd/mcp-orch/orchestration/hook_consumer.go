package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	hookTopicSessionStart = "agent.session.start"
	hookTopicStateChange  = "agent.state.change"
	hookTopicTurnAfter    = "agent.turn.after"
	hookTopicTurnFailed   = "agent.turn.failed"
	hookTopicTurnProgress = "agent.turn.progress"
	hookTopicProcessExit  = "agent.process.exit"

	hookSyncTrigger = "hook_state_sync"

	hookRelayKindThreadStarted     = "thread.started"
	hookRelayKindThreadStopped     = "thread.stopped"
	hookRelayKindStateChanged      = "agent.state_changed"
	hookRelayKindTurnCompleted     = "turn.completed"
	hookRelayKindTurnInterrupted   = "turn.interrupted"
	hookRelayKindTurnItemCompleted = "turn.item_completed"
)

// NotifyTap observes core-turn terminal events coming through the hook
// consumer chain. Implementations forward into the orch MessageNotifier
// (cmd/mcp-orch/notify) or any other side-channel. The tap is called
// synchronously on the hook-dispatch goroutine, so every method must
// be non-blocking — the expected implementation is a TryEnqueue onto
// a bounded channel and immediately return.
//
// A nil NotifyTap on the hookConsumer is treated as no-op. Handlers
// fire the tap after the existing report / state handling completes so
// an errant tap cannot disrupt the consumer's primary path.
type NotifyTap interface {
	OnTurnCompleted(ctx context.Context, ev turndto.TurnCompleted)
	OnTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted)
	OnThreadStopped(ctx context.Context, ev threaddto.Stopped)
}

type hookConsumer struct {
	svc       *service
	logger    *slog.Logger
	notifyTap NotifyTap

	// dagFallbackLookup / dagFallbackFlow 是 ADR-017 v1.2 §2.5 + §3.4 锁外
	// DAG fallback 分支的依赖。未装配（测试 / runtime-only mode）为 nil
	// 时 runThreadStoppedDAGFallback 直接 return，不影响现有 hook 语义。
	dagFallbackLookup taskdag.NodeSpawningThreadLookup
	dagFallbackFlow   taskdag.NodeFlowStore

	// dagTurnCompletedDeps lets hook-delivered turn.completed events reuse
	// the same DAG node completion path as the in-process bus subscriber.
	dagTurnCompletedDeps DAGSubscriberDeps
}

type hookContextEnvelope struct {
	Kind  string          `json:"kind"`
	Event json.RawMessage `json:"event"`
}

// newHookConsumer keeps the pre-existing 2-arg signature for in-package
// tests (notably the hook_consumer_test suite). Wire-level fx injection
// uses HookAfterHandlerParams + ProvideHookAfterHandler so the optional
// NotifyTap can be discovered without breaking these tests.
//
// P22 P4 S4c2: the previously exported HookConsumer interface,
// NewHookConsumer constructor, and ProvideHookConsumer provider formed
// a subpackage bootstrap-hook protocol shell. Root assembly now wires
// the handler via contract.BootstrapHookAfterHandler, so this package
// no longer exports the interface or constructor. The archtest in
// internal/archtest/orchestration_no_hookconsumer_export_guard_test.go
// locks this in place.
//
// ADR-017 v1.2 dag fallback ports default nil (test / runtime-only mode);
// fx wiring goes through ProvideHookAfterHandler + HookAfterHandlerParams.
func newHookConsumer(svc *service, logger *slog.Logger) *hookConsumer {
	return newHookConsumerInternal(svc, logger, nil, nil, nil)
}

// HookAfterHandlerParams is the fx.In bundle for ProvideHookAfterHandler.
// A nil NotifyTap is valid (treated as no-op), so downstream modules
// that do not register a tap — or unit tests that wire the orchestration
// providers without the orch notify module — boot cleanly.
//
// DAGFallbackLookup / DAGFallbackFlow are optional ADR-017 v1.2 §2.5
// thread.stopped DAG fallback ports. When the DAG store module is not
// wired (e.g. agent-runtime-only deployment, unit-test harness) the
// fallback short-circuits at runThreadStoppedDAGFallback's nil check.
type HookAfterHandlerParams struct {
	fx.In

	Service           *service
	Logger            *slog.Logger                     `optional:"true"`
	NotifyTap         NotifyTap                        `optional:"true"`
	DAGFallbackLookup taskdag.NodeSpawningThreadLookup `optional:"true"`
	DAGFallbackFlow   taskdag.NodeFlowStore            `optional:"true"`
	AgentThreads      AgentThreadLookup                `optional:"true"`
	SvcStopper        StopAgentService                 `optional:"true"`
	SharedFileReader  nodeexec.SharedFileReader        `optional:"true"`
	SharedFileWriter  nodeexec.SharedFileWriter        `optional:"true"`
	NodeRouter        *NodeExecutorRouter              `optional:"true"`
}

// ProvideHookAfterHandler is the fx-facing constructor. It returns the
// bootstrap-side after-hook as a plain function (contract.BootstrapHookAfterHandler)
// so the root assembly plumbs it straight into bootstrap.HookConfig.OnAfter
// without typing on any orchestration subpackage interface.
func ProvideHookAfterHandler(p HookAfterHandlerParams) contract.BootstrapHookAfterHandler {
	return newHookConsumerInternal(
		p.Service,
		p.Logger,
		p.NotifyTap,
		p.DAGFallbackLookup,
		p.DAGFallbackFlow,
		withHookTurnCompletedDAGDeps(DAGSubscriberDeps{
			LookupStore:      p.DAGFallbackLookup,
			FlowStore:        p.DAGFallbackFlow,
			AgentThreads:     p.AgentThreads,
			SvcStopper:       p.SvcStopper,
			SharedFileReader: p.SharedFileReader,
			SharedFileWriter: p.SharedFileWriter,
			NodeRouter:       p.NodeRouter,
		}),
	).After
}

type hookConsumerOption func(*hookConsumer)

func withHookTurnCompletedDAGDeps(deps DAGSubscriberDeps) hookConsumerOption {
	return func(c *hookConsumer) {
		c.dagTurnCompletedDeps = deps
	}
}

func newHookConsumerInternal(
	svc *service,
	logger *slog.Logger,
	tap NotifyTap,
	fallbackLookup taskdag.NodeSpawningThreadLookup,
	fallbackFlow taskdag.NodeFlowStore,
	opts ...hookConsumerOption,
) *hookConsumer {
	c := &hookConsumer{
		svc:               svc,
		logger:            loggerOrDefault(logger),
		notifyTap:         tap,
		dagFallbackLookup: fallbackLookup,
		dagFallbackFlow:   fallbackFlow,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

func (c *hookConsumer) After(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error) {
	decision := mcp.AfterDecision{Decision: mcp.HookDecisionApprove}
	if c == nil || c.svc == nil {
		return decision, nil
	}
	envelope, ok := decodeHookContextEnvelope(c.logger, payload.Context)
	if !ok {
		return decision, nil
	}

	c.dispatchAfterTopic(ctx, strings.TrimSpace(payload.Topic), envelope)
	return decision, nil
}

func (c *hookConsumer) dispatchAfterTopic(ctx context.Context, topic string, envelope hookContextEnvelope) {
	switch topic {
	case hookTopicSessionStart:
		c.handleSessionStartTopic(ctx, envelope)
	case hookTopicStateChange:
		c.handleStateChangeTopic(ctx, envelope)
	case hookTopicTurnAfter:
		c.handleTurnAfterTopic(ctx, envelope)
	case hookTopicTurnFailed:
		c.handleTurnFailedTopic(ctx, envelope)
	case hookTopicTurnProgress:
		c.handleTurnProgressTopic(ctx, envelope)
	case hookTopicProcessExit:
		c.handleProcessExitTopic(ctx, envelope)
	}
}

func (c *hookConsumer) handleSessionStartTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[threaddto.Started](c.logger, envelope, hookRelayKindThreadStarted); ok {
		c.handleThreadStarted(ctx, ev)
	}
}

func (c *hookConsumer) handleStateChangeTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[agentdto.StateChanged](c.logger, envelope, hookRelayKindStateChanged); ok {
		c.handleStateChanged(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnAfterTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[turndto.TurnCompleted](c.logger, envelope, hookRelayKindTurnCompleted); ok {
		c.handleTurnCompleted(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnFailedTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[turndto.TurnInterrupted](c.logger, envelope, hookRelayKindTurnInterrupted); ok {
		c.handleTurnInterrupted(ctx, ev)
	}
}

func (c *hookConsumer) handleTurnProgressTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[turndto.ItemCompleted](c.logger, envelope, hookRelayKindTurnItemCompleted); ok {
		c.handleItemCompleted(ctx, ev)
	}
}

func (c *hookConsumer) handleProcessExitTopic(ctx context.Context, envelope hookContextEnvelope) {
	if ev, ok := decodeHookEvent[threaddto.Stopped](c.logger, envelope, hookRelayKindThreadStopped); ok {
		c.handleThreadStopped(ctx, ev)
	}
}

func (c *hookConsumer) handleThreadStarted(ctx context.Context, ev threaddto.Started) {
	provider := normalizeRuntimeProvider(ev.Provider)
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			agent.threadID = threadID
			agent.remoteThreadID = threadID
		}
		beforeProvider, beforeProviderSource := snapshotProvider(agent)
		applyRuntimeReportLocked(agent, 0, provider)
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		afterProvider, afterProviderSource := snapshotProvider(agent)
		if beforeProvider != afterProvider || beforeProviderSource != afterProviderSource {
			c.svc.publishAgentRuntimeReported(agent)
		}
		return nil
	})
	c.logUnexpectedHookError("thread started", ev.AgentID, ev.ThreadID, err)
}

// shouldDeferIdleHook implements the single-writer guard against
// launch_succeeded double-fire. While agent.state is provisioning or
// recovering, commitLaunchSuccessLocked is the authoritative writer of the
// state->idle transition; an idle event arriving via the hook channel must
// be dropped so the state machine fires launch_succeeded exactly once.
// All other (state, nextState) pairs flow through the normal path.
func shouldDeferIdleHook(nextState string, agentState agentdto.AgentState) bool {
	if nextState != string(agentdto.StateIdle) {
		return false
	}
	return agentState == agentdto.StateProvisioning || agentState == agentdto.StateRecovering
}

func (c *hookConsumer) handleStateChanged(ctx context.Context, ev agentdto.StateChanged) {
	nextState := strings.TrimSpace(ev.NewState)
	if !isKnownMirroredState(nextState) {
		c.logger.Warn("orchestration: ignoring unknown mirrored agent state",
			"agent_id", ev.AgentID,
			"thread_id", ev.ThreadID,
			"state", nextState,
		)
		return
	}
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		// P22 P4 §121/§282: enforce session-identity fence. Hook events
		// stamped with a stale launchSeq (i.e. emitted before the agent
		// was re-launched) must not be allowed to mutate the current
		// session's state. Empty SessionID is treated as legacy input.
		if !agentSessionFenceOK(agent, ev.SessionID) {
			c.logger.Warn("orchestration: dropping stale state-change event (session fence)",
				"agent_id", ev.AgentID,
				"thread_id", ev.ThreadID,
				"event_session_id", ev.SessionID,
				"current_session_id", agentSessionID(agent),
				"state", nextState,
			)
			return nil
		}
		// Launch/recover owns the provisioning->idle and recovering->idle
		// transitions via commitLaunchSuccessLocked. If the launcher subprocess
		// reports idle while we're still mid-launch, defer to the main flow to
		// avoid a double-fire of launch_succeeded (single-writer principle).
		if shouldDeferIdleHook(nextState, agent.state) {
			c.logger.Info("orchestration: deferring hook idle event to launch/recover commit",
				"agent_id", agent.id,
				"current_state", agent.state,
				"thread_id", ev.ThreadID,
			)
			return nil
		}
		before := string(agent.state)
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			agent.threadID = threadID
			agent.remoteThreadID = threadID
		}
		if nextState == string(agentdto.StateIdle) || nextState == string(agentdto.StateStopped) || nextState == string(agentdto.StateFailed) {
			agent.activeTurnID = ""
		}
		if before != nextState {
			if err := c.svc.hookSyncFireLocked(ctx, agent, nextState); err != nil {
				c.logger.Warn("orchestration: hook state sync fire failed",
					"agent_id", agent.id,
					"from", before,
					"to", nextState,
					"error", err,
				)
			}
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		return nil
	})
	c.logUnexpectedHookError("state change", ev.AgentID, ev.ThreadID, err)
}

func (c *hookConsumer) handleThreadStopped(ctx context.Context, ev threaddto.Stopped) {
	// 锁内：现有 agent runtime 推进（保留，只做 in-memory state + 事件投递）。
	// ADR-017 v1.2 §2.5：DAG 分支重量级 PG 事务 + 级联 update，重点携出锁外避免
	// 同 agent 高频 hook 路径序列化阻塞。
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		before := string(agent.state)
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" {
			agent.threadID = threadID
			agent.remoteThreadID = threadID
		}
		agent.activeTurnID = ""
		agent.stopReason = strings.TrimSpace(ev.Reason)
		if err := c.svc.hookSyncForceStoppedLocked(ctx, agent); err != nil {
			c.logger.Warn("orchestration: hook sync force stopped failed",
				"agent_id", agent.id,
				"from", before,
				"error", err,
			)
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		c.svc.publishAgentStopped(agent, ev.Reason)
		return nil
	})
	c.logUnexpectedHookError("thread stopped", ev.AgentID, ev.ThreadID, err)

	// ADR-017 v1.2 §2.5 + §3.4 锁外修正：DAG fallback 分支。锁释放后同步调，
	// 与 agent runtime 推进解耦；推进失败 log warn 不抛。
	c.runThreadStoppedDAGFallback(ctx, ev.ThreadID)

	if c.notifyTap != nil {
		c.notifyTap.OnThreadStopped(ctx, ev)
	}
}

// runThreadStoppedDAGFallback 是 ADR-017 v1.2 §2.5 + §3.4 的锁外 DAG 反应
// 分支：当 subscriber 主路未推进节点终态时（spawned agent crash / launch 后
// 未出 first_turn / hook 以外原因被外部杀止），thread.stopped 作为兜底把
// 节点推到 failed。与主路双路并发很常见，靠 isTerminalNodeStatus + SQL
// 白名单 + pgx.ErrNoRows 三层幂等保证不重复推进。
//
// dagFallbackLookup / dagFallbackFlow 默认 nil — 未装配 DAG store 的部署路径
// （测试 / runtime-only mode）直接跳过该分支，不影响现有 hook 语义。
func (c *hookConsumer) runThreadStoppedDAGFallback(ctx context.Context, threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	lookup := c.dagFallbackLookup
	flow := c.dagFallbackFlow
	if lookup == nil || flow == nil {
		return // 未装配 DAG store 的部署跳过
	}
	nodes, err := lookup.LookupNodesBySpawningThread(ctx, threadID)
	if err != nil {
		dagFallbackMetrics.IncLookupFailed()
		c.logger.Warn("thread stopped fallback: lookup nodes failed",
			"thread_id", threadID, "error", err)
		return
	}
	if len(nodes) == 0 {
		dagFallbackMetrics.IncNoNode()
		return
	}
	for i := range nodes {
		if ctx.Err() != nil {
			return
		}
		c.failThreadStoppedFallbackNode(ctx, flow, nodes[i])
	}
}

func (c *hookConsumer) failThreadStoppedFallbackNode(ctx context.Context, flow taskdag.NodeFlowStore, n taskdag.Node) {
	// 应用层幂等：终态和 awaiting_verify 都不再抢占。awaiting_verify
	// 表示 TurnCompleted 已 claim，sharedfile 物化可能仍在收尾。
	if !isDAGFallbackFailEligibleStatus(n.Status) {
		dagFallbackMetrics.IncIdempotentSkipped()
		return
	}
	res, failErr := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{
		DagKey:   n.DagKey,
		NodeKey:  n.NodeKey,
		RunID:    taskNodeRunID(&n),
		Reason:   "thread_stopped_fallback",
		FailFast: false,
	})
	if failErr != nil {
		dagFallbackMetrics.IncFailNodeErr()
		c.logger.Warn("thread stopped fallback: fail node failed",
			"dag_key", n.DagKey, "node_key", n.NodeKey, "error", failErr)
		return
	}
	dagFallbackMetrics.IncFailed()
	c.invokeThreadStoppedFallbackLifecycleHook(ctx, n, res)
}

func (c *hookConsumer) invokeThreadStoppedFallbackLifecycleHook(ctx context.Context, n taskdag.Node, res *taskdag.FailNodeResult) {
	if router := c.dagTurnCompletedDeps.NodeRouter; router != nil {
		router.invokeTerminalFailureHooksForTaskNode(ctx, failedNodeForLifecycle(n, res), nodeexec.NodeOutcome{
			Status:       nodeexec.NodeStatusFailed,
			ErrorSummary: "thread_stopped_fallback",
		})
	}
}

func failedNodeForLifecycle(original taskdag.Node, result *taskdag.FailNodeResult) *taskdag.Node {
	node := original
	if result != nil && result.Node != nil {
		node = *result.Node
		if node.NodeType == "" {
			node.NodeType = original.NodeType
		}
		if node.Title == "" {
			node.Title = original.Title
		}
		if len(node.Config) == 0 {
			node.Config = append(node.Config[:0], original.Config...)
		}
	}
	if node.Status == "" {
		node.Status = string(nodeexec.NodeStatusFailed)
	}
	return &node
}

func isDAGFallbackFailEligibleStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "done", "failed", "cancelled", "skipped", "awaiting_verify":
		return false
	default:
		return true
	}
}

func (c *hookConsumer) handleTurnCompleted(ctx context.Context, ev turndto.TurnCompleted) {
	if c == nil || c.svc == nil {
		return
	}
	report := turnCompletedReportText(ev)
	_, err := c.svc.HandleReportEvent(withEventTime(ctx, ev.Timestamp), ReportEvent{
		AgentID:   strings.TrimSpace(ev.AgentID),
		Report:    report,
		EventType: "turn/completed",
		EventData: mustMarshalHookReportEvent(ev),
	})
	c.logUnexpectedHookError("turn completed report", ev.AgentID, ev.ThreadID, err)
	handleTurnCompletedEvent(c.svc, c.logger, ev)
	c.handleDAGTurnCompletedFromHook(ctx, ev)
	if c.notifyTap != nil {
		c.notifyTap.OnTurnCompleted(ctx, ev)
	}
}

func (c *hookConsumer) handleDAGTurnCompletedFromHook(ctx context.Context, ev turndto.TurnCompleted) {
	if c == nil {
		return
	}
	deps := c.dagTurnCompletedDeps
	if deps.LookupStore == nil || deps.FlowStore == nil {
		return
	}
	handleDAGTurnCompleted(ctx, deps, c.logger, ev)
}

func (c *hookConsumer) handleTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) {
	handleTurnInterruptedEvent(c.svc, c.logger, ev)
	c.handleDAGTurnInterruptedFromHook(ctx, ev)
	if c.notifyTap != nil {
		c.notifyTap.OnTurnInterrupted(ctx, ev)
	}
}

func (c *hookConsumer) handleDAGTurnInterruptedFromHook(ctx context.Context, ev turndto.TurnInterrupted) {
	if c == nil {
		return
	}
	deps := c.dagTurnCompletedDeps
	if deps.LookupStore == nil || deps.FlowStore == nil {
		return
	}
	reason := strings.TrimSpace(ev.Reason)
	if reason == "" {
		reason = "turn_interrupted"
	}
	handleDAGTurnCompleted(ctx, deps, c.logger, turndto.TurnCompleted{
		TurnHeader: ev.TurnHeader,
		Success:    false,
		Reason:     reason,
	})
}

func (c *hookConsumer) handleItemCompleted(ctx context.Context, ev turndto.ItemCompleted) {
	if c == nil || c.svc == nil || !isFinalAnswerItem(ev) {
		return
	}
	_, err := c.svc.HandleReportEvent(withEventTime(ctx, ev.Timestamp), ReportEvent{
		AgentID:   strings.TrimSpace(ev.AgentID),
		EventType: strings.TrimSpace(platformshared.FirstTrimmed(ev.RawType, "item/completed")),
		EventData: append(json.RawMessage(nil), ev.Payload...),
	})
	c.logUnexpectedHookError("turn item completed", ev.AgentID, ev.ThreadID, err)
}

func (c *hookConsumer) logUnexpectedHookError(action, agentID, threadID string, err error) {
	if err == nil || errors.Is(err, errAgentNotFound) {
		return
	}
	c.logger.Warn("orchestration: hook consumer failed",
		"action", action,
		"agent_id", strings.TrimSpace(agentID),
		"thread_id", strings.TrimSpace(threadID),
		"error", err,
	)
}

func decodeHookContextEnvelope(logger *slog.Logger, raw json.RawMessage) (hookContextEnvelope, bool) {
	if len(raw) == 0 {
		return hookContextEnvelope{}, false
	}
	var envelope hookContextEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		loggerOrDefault(logger).Warn("orchestration: failed to decode hook context", "error", err)
		return hookContextEnvelope{}, false
	}
	if strings.TrimSpace(envelope.Kind) == "" || len(envelope.Event) == 0 {
		return hookContextEnvelope{}, false
	}
	return envelope, true
}

func decodeHookEvent[T any](logger *slog.Logger, envelope hookContextEnvelope, wantKind string) (T, bool) {
	var zero T
	if strings.TrimSpace(envelope.Kind) != strings.TrimSpace(wantKind) {
		return zero, false
	}
	var event T
	if err := json.Unmarshal(envelope.Event, &event); err != nil {
		loggerOrDefault(logger).Warn("orchestration: failed to decode hook event",
			"kind", envelope.Kind,
			"error", err,
		)
		return zero, false
	}
	return event, true
}

func mustMarshalHookReportEvent(event any) json.RawMessage {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return raw
}

func isFinalAnswerItem(ev turndto.ItemCompleted) bool {
	if !strings.EqualFold(strings.TrimSpace(ev.ItemType), "agentMessage") || len(ev.Payload) == 0 {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		return false
	}
	item, _ := payload["item"].(map[string]any)
	phase := firstPayloadString(item, "phase")
	if phase == "" {
		phase = firstPayloadString(payload, "phase")
	}
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "final_answer", "final-answer", "finalanswer", "final":
		return true
	default:
		return false
	}
}

func firstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if payload == nil {
			return ""
		}
		value, ok := payload[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isKnownMirroredState(state string) bool {
	switch agentdto.AgentState(strings.TrimSpace(state)) {
	case agentdto.StateProvisioning,
		agentdto.StateIdle,
		agentdto.StateTurnQueued,
		agentdto.StateTurnStarting,
		agentdto.StateTurnRunning,
		agentdto.StateAwaitingUserInput,
		agentdto.StateRecovering,
		agentdto.StateStopping,
		agentdto.StateStopped,
		agentdto.StateFailed:
		return true
	default:
		return false
	}
}

// resolveTransitionPath performs a BFS over TransitionDefinitions to find
// the shortest sequence of triggers that drives the state machine from
// `from` to `to`. Returns nil if no path exists. The returned slice
// contains trigger names in firing order.
func resolveTransitionPath(from, to string) []string {
	if from == to {
		return nil
	}
	// Build adjacency: state -> [{trigger, dest}, ...]
	type edge struct {
		trigger string
		dest    string
	}
	adj := make(map[string][]edge, len(agentdto.StateDefinitions))
	for _, td := range agentdto.TransitionDefinitions {
		adj[string(td.From)] = append(adj[string(td.From)], edge{trigger: string(td.Trigger), dest: string(td.To)})
	}
	// BFS parent map: state -> (trigger that led here, previous state)
	type step struct {
		trigger string
		prev    string
	}
	visited := map[string]step{from: {trigger: "", prev: ""}}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range adj[cur] {
			if _, seen := visited[e.dest]; seen {
				continue
			}
			visited[e.dest] = step{trigger: e.trigger, prev: cur}
			if e.dest == to {
				// Reconstruct path.
				var path []string
				for s := to; s != from; s = visited[s].prev {
					path = append(path, visited[s].trigger)
				}
				// Reverse to get firing order.
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				return path
			}
			queue = append(queue, e.dest)
		}
	}
	return nil
}

// hookSyncFireLocked drives the agent state machine from its current state
// to targetState using the transition table. It resolves the shortest
// trigger path for the (current -> target) transition and fires each
// trigger through the state machine so that sm.Fire owns every step.
//
// Intermediate transitions are fired silently (via sm.FireCtx) so that
// only the final before/after pair is published as a single state-change
// event with the hookSyncTrigger label.
//
// If no path exists in the transition table, the helper returns an error
// — callers must not fall back to direct assignment.
func (s *service) hookSyncFireLocked(ctx context.Context, agent *agentRuntime, targetState string) error {
	if agent == nil || agent.sm == nil {
		return errors.New("state machine is not initialized")
	}
	before := string(agent.state)
	if before == targetState {
		return nil
	}
	path := resolveTransitionPath(before, targetState)
	if len(path) == 0 {
		return fmt.Errorf(
			"no transition path in table for %s -> %s (agent %s)",
			before, targetState, agent.id,
		)
	}
	// Fire each trigger through the state machine. The SM's external-
	// storage mutator updates agent.state on each step.
	for _, trigger := range path {
		if err := agent.sm.FireCtx(ctx, stateless.Trigger(trigger)); err != nil {
			return fmt.Errorf(
				"hook sync fire %s (step in %s -> %s) for agent %s: %w",
				trigger, before, targetState, agent.id, err,
			)
		}
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
	s.publishStateChanged(agent, before, hookSyncTrigger)
	return nil
}

// hookSyncForceStoppedLocked drives the agent state machine to StateStopped
// regardless of the current state. It delegates to hookSyncFireLocked
// which resolves the shortest BFS path through the transition table.
// For states that cannot reach StateStopped (e.g. provisioning,
// recovering), the helper falls back to StateFailed since the hook
// represents an actual process exit.
func (s *service) hookSyncForceStoppedLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.sm == nil {
		return errors.New("state machine is not initialized")
	}
	if agent.state == agentdto.StateStopped {
		return nil
	}
	// Try the primary path to StateStopped.
	if err := s.hookSyncFireLocked(ctx, agent, string(agentdto.StateStopped)); err == nil {
		return nil
	}
	// Fallback: states like provisioning/recovering cannot reach stopped
	// but can reach failed via launch_failed. Accept failed as the
	// terminal state rather than bypassing the state machine.
	return s.hookSyncFireLocked(ctx, agent, string(agentdto.StateFailed))
}
