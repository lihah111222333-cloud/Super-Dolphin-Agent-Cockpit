package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/turncompletionretry"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/qmuntal/stateless"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/kelindar/event"
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

// NotifyTap 是 hook consumer 旁路通知层的窄端口。
// 它只观察 turn/thread 事件，不参与 agent 状态机或 DAG fallback 的写入决策。
type NotifyTap interface {
	OnTurnCompleted(ctx context.Context, ev turndto.TurnCompleted)
	OnTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted)
	OnThreadStopped(ctx context.Context, ev threaddto.Stopped)
}

type hookConsumer struct {
	svc       *service
	logger    *slog.Logger
	notifyTap NotifyTap

	dagFallbackLookup taskdag.NodeSpawningThreadLookup
	dagFallbackFlow   taskdag.NodeFlowStore

	dagTurnCompletedDeps DAGSubscriberDeps
}

type hookContextEnvelope struct {
	Kind  string
	Event json.RawMessage
}

func newHookConsumer(svc *service, logger *slog.Logger) *hookConsumer {
	return newHookConsumerInternal(svc, logger, nil, nil, nil)
}

// HookAfterHandlerParams 汇总 provider hook 后处理所需的 fx 依赖。
// 可选端口只开启对应桥接能力，缺失时不得静默改写核心 agent 状态流。
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
	ArtifactImporter  sharedfilestore.Importer         `optional:"true"`
	NodeRouter        *NodeExecutorRouter              `optional:"true"`
	EventBus          *event.Dispatcher                `optional:"true"`
}

// ProvideHookAfterHandler 把 provider hook 事件接入 orchestration 状态同步。
// 可选 DAG 端口只影响 DAG fallback 和 TurnCompleted 桥接，不影响普通 agent hook。
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
			ArtifactImporter: p.ArtifactImporter,
			NodeRouter:       p.NodeRouter,
			EventBus:         p.EventBus,
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

// After 解码 provider hook payload 并分派到对应 topic。
// 解码失败只批准并跳过，hook 不能阻断 provider 主流程。
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

// dispatchAfterTopic 按 hook topic 选择具体事件解码器。
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

// handleThreadStarted 绑定 provider thread_id 并更新 runtime provider 快照。
// provisioning/recovering 期间的线程归属由 pending launch 逻辑接管，避免旧线程覆盖新会话。
func (c *hookConsumer) handleThreadStarted(ctx context.Context, ev threaddto.Started) {
	provider := normalizeRuntimeProvider(ev.Provider)
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		threadID := strings.TrimSpace(ev.ThreadID)
		if threadID != "" && launchOwnsHookThreadBinding(agent.state) {
			recordPendingLaunchThreadLocked(agent, threadID, ev.Timestamp)
		} else if threadID != "" {
			agent.threadID, agent.remoteThreadID = threadID, threadID
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

// shouldDeferIdleHook 让 launch/recover 提交流程独占 provisioning/recovering 到 idle 的写入。
// 其他状态仍按普通 hook 同步，避免启动中 idle hook 与 commitLaunchSuccessLocked 重复 fire。
func shouldDeferIdleHook(nextState string, agentState agentdto.AgentState) bool {
	if nextState != string(agentdto.StateIdle) {
		return false
	}
	return agentState == agentdto.StateProvisioning || agentState == agentdto.StateRecovering
}

// handleStateChanged 根据 provider 状态事件同步本地状态机。
// session fence 会丢弃旧会话事件，空 SessionID 保留兼容但不能覆盖已换代的状态。
func (c *hookConsumer) handleStateChanged(ctx context.Context, ev agentdto.StateChanged) {
	nextState := strings.TrimSpace(ev.NewState)
	if !isKnownMirroredState(nextState) {
		c.logger.Warn("orchestration: ignoring unknown mirrored agent state", "agent_id", ev.AgentID, "thread_id", ev.ThreadID, "state", nextState)
		return
	}
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		// session fence 防止旧进程/旧线程的状态事件写入当前会话。
		// 空 SessionID 来自早期 provider 事件，仍按兼容输入处理。
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
		// launch/recover 提交流程负责把 provisioning/recovering 推到 idle。
		// 启动过程中收到 idle hook 时延后处理，避免 launch_succeeded 被重复触发。
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
		if !bindStateChangedHookThreadLocked(agent, threadID, nextState) {
			return nil
		}
		clearTerminalActiveTurnLocked(agent, nextState)
		if before != nextState {
			if err := c.svc.syncStateChangedHookLocked(ctx, agent, nextState); err != nil {
				c.logger.Warn("orchestration: hook state sync fire failed",
					"agent_id", agent.id,
					"from", before,
					"to", nextState,
					"error", err,
				)
			}
		}
		c.svc.setStateChangedFallbackReportLocked(ctx, agent, nextState)
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		return nil
	})
	c.logUnexpectedHookError("state change", ev.AgentID, ev.ThreadID, err)
}

// handleThreadStopped 把 provider stopped 事件同步到本地 runtime，并触发 DAG 兜底失败推进。
// 被抑制或不属于当前会话的 stopped 事件只记录为跳过，不再推进状态。
func (c *hookConsumer) handleThreadStopped(ctx context.Context, ev threaddto.Stopped) {
	stoppedAccepted := true
	err := c.svc.withAgentLocked(ev.AgentID, func(agent *agentRuntime) error {
		before := string(agent.state)
		if threadID := strings.TrimSpace(ev.ThreadID); c.svc.stoppedHookThreadSuppressed(threadID, ev.Timestamp) ||
			recoveringOldThreadHook(agent, threadID) || !bindStoppedHookThreadLocked(agent, threadID) {
			stoppedAccepted = false
			return nil
		}
		agent.activeTurnID, agent.stopReason = "", strings.TrimSpace(ev.Reason)
		if err := c.svc.hookSyncForceStoppedLocked(ctx, agent); err != nil {
			c.logger.Warn("orchestration: hook sync force stopped failed", "agent_id", agent.id, "from", before, "error", err)
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt)
		c.svc.setStoppedFallbackReportLocked(ctx, agent)
		emitEvent(c.svc.eventBus, eventTypeAgentStopped, eventAgentID(agent), agent, ev.Reason)
		return nil
	})
	if errors.Is(err, errAgentNotFound) && c.svc.stoppedHookThreadSuppressed(ev.ThreadID, ev.Timestamp) {
		return
	}
	c.logUnexpectedHookError("thread stopped", ev.AgentID, ev.ThreadID, err)
	if err == nil && !stoppedAccepted {
		return
	}

	// DAG 兜底失败推进在锁外执行，避免节点状态写回反向占用 agent runtime 锁。
	// 推进失败只记录日志和指标，不影响线程 stopped 的本地状态收敛。
	c.runThreadStoppedDAGFallback(ctx, ev.ThreadID)

	if c.notifyTap != nil {
		c.notifyTap.OnThreadStopped(ctx, ev)
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
	if c == nil || c.dagTurnCompletedDeps.LookupStore == nil || c.dagTurnCompletedDeps.FlowStore == nil {
		return
	}
	handleDAGTurnCompleted(ctx, c.dagTurnCompletedDeps, c.logger, ev)
}

func (c *hookConsumer) handleTurnInterrupted(ctx context.Context, ev turndto.TurnInterrupted) {
	handleTurnInterruptedEvent(c.svc, c.logger, ev)
	c.handleDAGTurnInterruptedFromHook(ctx, ev)
	if c.notifyTap != nil {
		c.notifyTap.OnTurnInterrupted(ctx, ev)
	}
}

func (c *hookConsumer) handleDAGTurnInterruptedFromHook(ctx context.Context, ev turndto.TurnInterrupted) {
	if c == nil || c.dagTurnCompletedDeps.LookupStore == nil || c.dagTurnCompletedDeps.FlowStore == nil {
		return
	}
	reason := strings.TrimSpace(ev.Reason)
	if reason == "" {
		reason = "turn_interrupted"
	}
	handleDAGTurnCompleted(ctx, c.dagTurnCompletedDeps, c.logger, turndto.TurnCompleted{
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

// isFinalAnswerItem 判断 turn item 是否代表最终回答。
// 支持多种 phase 写法，避免 provider 版本差异导致 final report 漏写。
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

// resolveTransitionPath 在状态定义表上找 from 到 to 的最短 trigger 序列。
// 找不到路径时返回 nil，调用方必须报错，不能直接改状态字段。
func resolveTransitionPath(from, to string) []string {
	if from == to {
		return nil
	}
	// 以状态为 key 建邻接表，value 记录触发器和目标状态。
	type edge struct {
		trigger string
		dest    string
	}
	adj := make(map[string][]edge, len(agentdto.StateDefinitions))
	for _, td := range agentdto.TransitionDefinitions {
		adj[string(td.From)] = append(adj[string(td.From)], edge{trigger: string(td.Trigger), dest: string(td.To)})
	}
	// BFS parent map 保留抵达每个状态的触发器和上一个状态。
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
				// 回溯触发器路径。
				var path []string
				for s := to; s != from; s = visited[s].prev {
					path = append(path, visited[s].trigger)
				}
				// 反转为实际 firing 顺序。
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

// hookSyncFireLocked 用状态表把当前状态推进到 targetState。
// 中间状态只走 sm.FireCtx，最终仅发布一次 hook_state_sync 事件；找不到路径时返回错误而不是直接赋值。
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
	// 每个 trigger 都必须经状态机执行；外部存储 mutator 会在每一步同步 agent.state。
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

// hookSyncForceStoppedLocked 将状态机推进到 stopped。
// 如果当前状态无法到达 stopped（例如 provisioning/recovering），则改走 failed；
// stopped hook 代表真实进程退出，不能绕过状态机直接写字段。
func (s *service) hookSyncForceStoppedLocked(ctx context.Context, agent *agentRuntime) error {
	if agent == nil || agent.sm == nil {
		return errors.New("state machine is not initialized")
	}
	if agent.state == agentdto.StateStopped {
		return nil
	}
	// 优先尝试正常到达 stopped 的转换路径。
	if err := s.hookSyncFireLocked(ctx, agent, string(agentdto.StateStopped)); err == nil {
		return nil
	}
	// provisioning/recovering 等状态不能到达 stopped，但能经 launch_failed 到达 failed。
	// 接受 failed 作为终态，避免绕过状态机。
	return s.hookSyncFireLocked(ctx, agent, string(agentdto.StateFailed))
}

// runThreadStoppedDAGFallback 在线程 stopped 但未收到 turn.completed 时失败关联 DAG 节点。
// 它在 agent 锁外运行，避免节点失败推进与 agent 状态同步互相等待。
func (c *hookConsumer) runThreadStoppedDAGFallback(ctx context.Context, threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	lookup := c.dagFallbackLookup
	flow := c.dagFallbackFlow
	if lookup == nil || flow == nil {
		return
	}
	nodes, err := lookup.LookupNodesBySpawningThread(ctx, threadID)
	if err != nil {
		orchmetrics.IncDAGFallbackLookupFailed()
		c.logger.Warn("thread stopped fallback: lookup nodes failed",
			"thread_id", threadID, "error", err)
		return
	}
	if len(nodes) == 0 {
		orchmetrics.IncDAGFallbackNoNode()
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
	if !isDAGFallbackFailEligibleStatus(n.Status) {
		orchmetrics.IncDAGFallbackIdempotentSkipped()
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
		orchmetrics.IncDAGFallbackFailNodeErr()
		c.logger.Warn("thread stopped fallback: fail node failed",
			"dag_key", n.DagKey, "node_key", n.NodeKey, "error", failErr)
		turncompletionretry.EnqueueTerminalFailureCompensation(ctx, flow, c.logger, &n, "thread_stopped_fallback", failErr, false)
		return
	}
	orchmetrics.IncDAGFallbackFailed()
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

// failedNodeForLifecycle 合成 lifecycle hook 需要的失败节点快照。
// store 返回的节点可能缺少展示字段，缺失时从原节点补齐，避免 hook 收到半截上下文。
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
