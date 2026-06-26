package turn

import (
	"context"
	"strings"
	"sync"
	"time"

	buscontract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// maxDrainedEntries 限制已输出 turn 的去重表大小。
// 超限后清空去重表，避免长期进程内存无界增长；极晚重复终态会被当作新事件处理。
const maxDrainedEntries = 10_000

// Trajectory 是收集器产出的单 turn 事实聚合，仅驻留内存，供 evaluator 和 extractor 消费。
type Trajectory struct {
	TurnID      string
	LocalTurnID string
	ThreadID    string
	AgentID     string
	SessionID   string
	// 收集器看不到 agent 工作目录，因此 Cwd 由后续消费者按 ThreadID 补齐。
	Cwd            string
	StartedAt      time.Time
	EndedAt        time.Time
	TerminalState  string
	Success        *bool
	SkillsSelected []string
	ToolCalls      []ToolCall
	TokenUsage     *TokenSnapshot
}

// ToolCall 是轨迹中的单次工具调用快照。
type ToolCall struct {
	CallID    string
	Name      string
	Args      string
	Result    string
	Failed    bool
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
	// DiffCount 统计通过 observation 归因到此 call 的 ToolDiffUpdated 事件数。
	DiffCount int
}

// TokenSnapshot 是下游消费使用的 int 版本 token 快照，来源 observation 中的 int64 事实。
type TokenSnapshot struct {
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
	ContextWindowTokens int
}

// Collector 聚合每个 turn 的生命周期事实，并从 observation Contract 读取终态、token、skill 和 call 归因。
type Collector struct {
	mu       sync.Mutex
	contract observation.ObservationReader
	logger   *pkglogger.Logger

	// partials 按 turnID 暂存尚未进入终态的轨迹片段。
	partials map[string]*partialTrajectory

	// completed 保存已到终态但尚未被 Drain 返回的轨迹。
	completed []Trajectory

	// drained 记录已输出的 turn，防止 late terminal 让同一轨迹重复进入 completed。
	// 表大小受 maxDrainedEntries 限制，超限时整体清空。
	drained map[string]struct{}
}

type partialTrajectory struct {
	turnID    string
	threadID  string
	agentID   string
	sessionID string
	startedAt time.Time
	toolCalls map[string]*ToolCall
	callOrder []string
}

// NewTrajectoryCollector 创建空轨迹收集器；未注入 observation 时仍接收事件但缺少终态/token/skill 快照。
func NewTrajectoryCollector(contract observation.ObservationReader, logger *pkglogger.Logger) *Collector {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Collector{
		contract: contract,
		logger:   logger,
		partials: make(map[string]*partialTrajectory),
		drained:  make(map[string]struct{}),
	}
}

// Snapshot 返回指定 turn 的当前未完成轨迹，已经 Drain 过或从未见过的 turn 返回 ok=false。
func (c *Collector) Snapshot(turnID string) (Trajectory, bool) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return Trajectory{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.partials[turnID]
	if !ok {
		return Trajectory{}, false
	}
	return c.materializeLocked(p), true
}

// Drain 取出自上次调用以来进入终态的轨迹；同一个 turn 在收集器生命周期内最多返回一次。
func (c *Collector) Drain() []Trajectory {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.completed) == 0 {
		return nil
	}
	out := c.completed
	c.completed = nil
	return out
}

// onTurnStarted 初始化 partial trajectory，并保存可从 TurnStarted 直接获得的线程/agent 信息。
func (c *Collector) onTurnStarted(ev turndto.TurnStarted) {
	turnID := strings.TrimSpace(ev.TurnID)
	if turnID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.drained[turnID]; done {
		return
	}
	p := c.ensurePartialLocked(turnID)
	if tid := strings.TrimSpace(ev.ThreadID); tid != "" {
		p.threadID = tid
	}
	if aid := strings.TrimSpace(ev.AgentID); aid != "" {
		p.agentID = aid
	}
	// TurnStarted 不携带 SessionID；该字段由后续消费者根据线程或 agent 状态补齐。
	if p.startedAt.IsZero() && !ev.Timestamp.IsZero() {
		p.startedAt = ev.Timestamp
	}
}

// onTurnCompleted 把 completed 事件归入统一终态 drain 路径。
func (c *Collector) onTurnCompleted(ev turndto.TurnCompleted) {
	c.onTurnTerminal(ev.TurnID)
}

// onTurnInterrupted 把 interrupted 事件归入统一终态 drain 路径。
func (c *Collector) onTurnInterrupted(ev turndto.TurnInterrupted) {
	c.onTurnTerminal(ev.TurnID)
}

// onTurnStalled 把 stalled 事件归入统一终态 drain 路径。
func (c *Collector) onTurnStalled(ev turndto.TurnStalled) {
	c.onTurnTerminal(ev.TurnID)
}

// onTurnTerminal 将所有终态事件归并到同一 drain 路径。
// 同一 turn 多次触发终态时由 drained 保证幂等，避免中断后又完成导致重复输出。
func (c *Collector) onTurnTerminal(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.drained[turnID]; done {
		return
	}
	p, ok := c.partials[turnID]
	if !ok {
		// 终态可能先于 TurnStarted 到达；仍创建最小轨迹，避免 observation 中的终态/token 丢给消费者。
		p = c.ensurePartialLocked(turnID)
	}
	traj := c.materializeLocked(p)
	c.completed = append(c.completed, traj)
	delete(c.partials, turnID)
	if len(c.drained) >= maxDrainedEntries {
		c.logger.Warn("trajectory_collector: drained map exceeded limit, clearing",
			"limit", maxDrainedEntries, "size", len(c.drained))
		c.drained = make(map[string]struct{}, 64)
	}
	c.drained[turnID] = struct{}{}
}

// onToolCallBegin 按 callID 记录工具调用开始信息，并保留 begin 到达顺序。
func (c *Collector) onToolCallBegin(ev tooldto.ToolCallBegin) {
	callID := strings.TrimSpace(ev.CallID)
	turnID := strings.TrimSpace(ev.TurnID)
	if callID == "" || turnID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.drained[turnID]; done {
		return
	}
	p := c.ensurePartialLocked(turnID)
	if _, exists := p.toolCalls[callID]; exists {
		return
	}
	p.toolCalls[callID] = &ToolCall{
		CallID:    callID,
		Name:      strings.TrimSpace(ev.ToolName),
		Args:      ev.ArgumentsPreview,
		StartedAt: ev.Timestamp,
	}
	p.callOrder = append(p.callOrder, callID)
}

// onToolCallEnd 补齐工具结果和失败信息，允许 End 先于 Begin 到达时创建最小记录。
func (c *Collector) onToolCallEnd(ev tooldto.ToolCallEnd) {
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" {
		return
	}
	turnID := c.toolCallEndTurnID(callID, ev.TurnID)
	if turnID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.drained[turnID]; done {
		return
	}
	p := c.ensurePartialLocked(turnID)
	tc, ok := p.toolCalls[callID]
	if !ok {
		// End 可能先于 Begin 到达；创建最小记录，避免结果和失败信息丢失。
		tc = &ToolCall{CallID: callID}
		p.toolCalls[callID] = tc
		p.callOrder = append(p.callOrder, callID)
	}
	tc.Result = ev.Result
	tc.Failed = !ev.Success
	tc.Error = ev.Error
	if !ev.Timestamp.IsZero() {
		tc.EndedAt = ev.Timestamp
	}
	if tc.Name == "" {
		tc.Name = strings.TrimSpace(ev.ToolName)
	}
}

// toolCallEndTurnID 优先使用事件自带 turnID，缺失时通过 observation 归因表回查。
func (c *Collector) toolCallEndTurnID(callID, rawTurnID string) string {
	turnID := strings.TrimSpace(rawTurnID)
	if turnID != "" {
		return turnID
	}
	if c.contract == nil {
		return ""
	}
	// ToolCallEnd 缺 turnID 时只能依赖 observation 归因表；未命中则不猜测归属。
	if owner, ok := c.contract.LookupCall(callID); ok {
		return owner
	}
	return ""
}

// onToolDiffUpdated 只给已归因且已开始的工具调用累加 diff 次数，孤儿 diff 会被丢弃。
func (c *Collector) onToolDiffUpdated(ev tooldto.ToolDiffUpdated) {
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" || c.contract == nil {
		return
	}
	owner, ok := c.contract.LookupCall(callID)
	if !ok {
		// 无法归因的 diff 直接丢弃，不能挂到任意 turn 上制造假调用。
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, done := c.drained[owner]; done {
		return
	}
	p, ok := c.partials[owner]
	if !ok {
		return
	}
	tc, ok := p.toolCalls[callID]
	if !ok {
		// 没有 Begin 就没有可挂载的 ToolCall；不能为孤立 diff 合成假调用。
		return
	}
	tc.DiffCount++
}

// materializeLocked 将 partial trajectory 复制为对外 Trajectory 值，调用方必须持有 c.mu。
// 终态、token、skill 和时间戳优先来自 observation Contract；工具调用按 Begin 到达顺序复制。
func (c *Collector) materializeLocked(p *partialTrajectory) Trajectory {
	tj := Trajectory{
		TurnID:      p.turnID,
		LocalTurnID: p.turnID,
		ThreadID:    p.threadID,
		AgentID:     p.agentID,
		SessionID:   p.sessionID,
		StartedAt:   p.startedAt,
	}
	if c.contract != nil {
		c.applyContractSnapshot(&tj, p.turnID)
	}
	tj.ToolCalls = materializeToolCalls(p)
	return tj
}

// applyContractSnapshot 从 observation Contract 补齐终态、时间戳、token 和 skill 事实。
func (c *Collector) applyContractSnapshot(tj *Trajectory, turnID string) {
	if t, ok := c.contract.Terminal(turnID); ok {
		tj.TerminalState = string(t.Kind)
		if t.Success != nil {
			v := *t.Success
			tj.Success = &v
		}
	}
	if ts, ok := c.contract.Timestamps(turnID); ok {
		if !ts.StartedAt.IsZero() {
			tj.StartedAt = ts.StartedAt
		}
		tj.EndedAt = ts.CompletedAt
	}
	if snap, ok := c.contract.Tokens(turnID); ok && snap.Observed {
		tj.TokenUsage = &TokenSnapshot{
			InputTokens:         int(snap.Input),
			OutputTokens:        int(snap.Output),
			TotalTokens:         int(snap.Total),
			ContextWindowTokens: int(snap.ContextWindowTokens),
		}
	}
	tj.SkillsSelected = c.contract.SkillsSelected(turnID)
}

// materializeToolCalls 按 Begin 到达顺序复制工具调用，避免 map 遍历导致输出抖动。
func materializeToolCalls(p *partialTrajectory) []ToolCall {
	toolCalls := make([]ToolCall, 0, len(p.callOrder))
	for _, id := range p.callOrder {
		if tc, ok := p.toolCalls[id]; ok && tc != nil {
			toolCalls = append(toolCalls, *tc)
		}
	}
	return toolCalls
}

// ensurePartialLocked 返回或创建指定 turn 的 partial trajectory，调用方必须持有 c.mu。
func (c *Collector) ensurePartialLocked(turnID string) *partialTrajectory {
	if p, ok := c.partials[turnID]; ok {
		return p
	}
	p := &partialTrajectory{
		turnID:    turnID,
		toolCalls: make(map[string]*ToolCall),
	}
	c.partials[turnID] = p
	return p
}

// SubscribeTrajectory 把 collector 事件处理器挂到 dispatcher，并返回统一 cancel。
func SubscribeTrajectory(dispatcher *event.Dispatcher, c *Collector, contract observation.ObservationReader, logger *pkglogger.Logger) context.CancelFunc {
	if dispatcher == nil || c == nil {
		return func() {}
	}
	cancels := []context.CancelFunc{
		buscontract.ResilientSubscribe(dispatcher, c.onTurnStarted, logger),
		buscontract.ResilientSubscribe(dispatcher, c.onTurnCompleted, logger),
		buscontract.ResilientSubscribe(dispatcher, c.onTurnInterrupted, logger),
		buscontract.ResilientSubscribe(dispatcher, c.onTurnStalled, logger),
		buscontract.ResilientSubscribe(dispatcher, c.onToolCallBegin, logger),
		buscontract.ResilientSubscribe(dispatcher, c.onToolCallEnd, logger),
		buscontract.ResilientSubscribe(dispatcher, c.onToolDiffUpdated, logger),
	}
	return func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	}
}

// NewTrajectorySubscribers 声明轨迹收集器的 bus 订阅规格，生命周期由 BusModule 管理。
func NewTrajectorySubscribers(c *Collector, contract observation.ObservationReader, logger *pkglogger.Logger) platformbus.SubscriberResult {
	return platformbus.SubscriberResult{
		Spec: buscontract.SubscriberSpec{
			EventType:     "turn.trajectory",
			HandlerSymbol: "turn.SubscribeTrajectory",
			OwnerModule:   "turn.trajectory_collector",
			CancelOwner:   "bus.SubscriberGroup",
			ShutdownClass: "bus-subscriber",
			TestFixtureID: "trajectory-subscribers",
			Register: func(dispatcher *event.Dispatcher) context.CancelFunc {
				cancel := SubscribeTrajectory(dispatcher, c, contract, logger)
				var once sync.Once
				return func() {
					once.Do(func() {
						if cancel != nil {
							cancel()
						}
					})
				}
			},
		},
	}
}
