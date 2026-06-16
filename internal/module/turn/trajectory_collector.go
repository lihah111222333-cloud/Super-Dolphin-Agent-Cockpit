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
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// maxDrainedEntries caps the drained dedup map. When exceeded, the map is
// cleared. A duplicate terminal arriving after 10k+ turns is astronomically
// unlikely, so this is a safe trade-off vs unbounded memory growth.
const maxDrainedEntries = 10_000

// Trajectory is the per-turn fact aggregate produced by the collector. It is
// in-memory only and meant to be consumed by Step 3 evaluator / Step 4
// extractor; it is never persisted by this layer.
type Trajectory struct {
	TurnID      string
	LocalTurnID string
	ThreadID    string
	AgentID     string
	SessionID   string
	// Cwd is left empty by the collector: it has no view of the agent's
	// working directory. Step 4 extractor backfills this via ThreadID.
	Cwd            string
	StartedAt      time.Time
	EndedAt        time.Time
	TerminalState  string
	Success        *bool
	SkillsSelected []string
	ToolCalls      []ToolCall
	TokenUsage     *TokenSnapshot
}

// ToolCall is a per-call slice of the trajectory.
type ToolCall struct {
	CallID    string
	Name      string
	Args      string
	Result    string
	Failed    bool
	Error     string
	StartedAt time.Time
	EndedAt   time.Time
	// DiffCount counts how many ToolDiffUpdated events resolved to this
	// call via observation.LookupCall. Step 2 brief did not list this
	// field; it was added so TestAttributesToolDiffViaCallIDMap has an
	// observable side-effect to assert against.
	DiffCount int
}

// TokenSnapshot mirrors observation.TokenSnapshot but exposes int values to
// downstream consumers (Step 3/4 expect ints, observation stores int64).
type TokenSnapshot struct {
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
	ContextWindowTokens int
}

// Collector aggregates per-turn lifecycle facts. It subscribes to the same
// bus dispatcher as observation but does not duplicate observation's work:
// it reads observation Contract for terminal precedence, token merge,
// skills selection, and call-id attribution. The push direction stays
// observation -> collector -> downstream consumer.
type Collector struct {
	mu       sync.Mutex
	contract observation.ObservationReader
	logger   *pkglogger.Logger

	// turnID -> partial trajectory accumulated until terminal arrives.
	partials map[string]*partialTrajectory

	// Trajectories whose turn reached terminal but Drain has not yet
	// returned them.
	completed []Trajectory

	// drained tracks turns whose trajectory has already been emitted, so
	// a late-arriving second terminal event (e.g. TurnCompleted after
	// TurnInterrupted) does not re-emit. Bounded to maxDrainedEntries;
	// when the limit is reached, the map is cleared. This is acceptable
	// because a duplicate terminal arriving after 10k+ turns is
	// astronomically unlikely in practice.
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

// NewTrajectoryCollector builds an empty Collector. contract may be nil
// when observation is not wired in the deployment; in that case the
// collector still accepts events but materialized trajectories carry no
// terminal / token / skills data.
// NewTrajectoryCollector 创建trajectory收集器。
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

// Snapshot returns the current partial trajectory for a turn. terminal
// fields are pulled live from the contract. Returns ok=false when the turn
// is unknown to the collector (no events yet, or already drained).
// Snapshot 处理快照。
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

// Drain returns every trajectory whose turn has reached terminal since the
// previous Drain call. It is safe for concurrent use; a turn is returned
// at most once across the full lifetime of the collector.
// Drain 等待队列里已接收的任务收尾。
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

// onTurnStarted 处理onturnstarted。
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
	// TurnStarted carries no SessionID (shared.TurnHeader nests
	// AgentHeader, not AgentSessionHeader). Trajectory.SessionID is left
	// for Step 4 extractor to backfill via thread/agent state.
	if p.startedAt.IsZero() && !ev.Timestamp.IsZero() {
		p.startedAt = ev.Timestamp
	}
}

func (c *Collector) onTurnCompleted(ev turndto.TurnCompleted) {
	c.onTurnTerminal(ev.TurnID)
}

func (c *Collector) onTurnInterrupted(ev turndto.TurnInterrupted) {
	c.onTurnTerminal(ev.TurnID)
}

func (c *Collector) onTurnStalled(ev turndto.TurnStalled) {
	c.onTurnTerminal(ev.TurnID)
}

// onTurnTerminal funnels every terminal-class turn event through a single
// drain path. It is idempotent against the same turn firing terminal more
// than once (e.g. interrupted then late completed): the drained set guards
// the second call.
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
		// Terminal arrived before we ever saw TurnStarted - still emit
		// a minimal trajectory so observation facts (terminal, tokens)
		// are not lost to the consumer.
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

// onToolCallEnd 处理on工具callend。
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
		// End arrived before Begin (out-of-order). Create the entry so
		// the result/failure is not lost.
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

func (c *Collector) toolCallEndTurnID(callID, rawTurnID string) string {
	turnID := strings.TrimSpace(rawTurnID)
	if turnID != "" {
		return turnID
	}
	if c.contract == nil {
		return ""
	}
	// Defensive: ToolCallEnd without TurnID should resolve via the
	// observation attribution map.
	if owner, ok := c.contract.LookupCall(callID); ok {
		return owner
	}
	return ""
}

// onToolDiffUpdated 处理on工具diffupdated。
func (c *Collector) onToolDiffUpdated(ev tooldto.ToolDiffUpdated) {
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" || c.contract == nil {
		return
	}
	owner, ok := c.contract.LookupCall(callID)
	if !ok {
		// No attribution - drop. Step 2 explicitly forbids attaching
		// orphan diffs to an arbitrary turn.
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
		// Without a prior Begin we have no ToolCall to attach to. Do
		// not synthesize one here - that would let an isolated diff
		// fabricate a phantom call.
		return
	}
	tc.DiffCount++
}

// materializeLocked copies a partial trajectory into a Trajectory value.
// The caller must hold c.mu. Terminal / tokens / skills / timestamps are
// pulled from the observation Contract when present; ToolCalls are copied
// in the order Begin events arrived.
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

// applyContractSnapshot 应用contract快照。
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

func materializeToolCalls(p *partialTrajectory) []ToolCall {
	toolCalls := make([]ToolCall, 0, len(p.callOrder))
	for _, id := range p.callOrder {
		if tc, ok := p.toolCalls[id]; ok && tc != nil {
			toolCalls = append(toolCalls, *tc)
		}
	}
	return toolCalls
}

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

// SubscribeTrajectory mounts every collector handler onto dispatcher and
// returns a single cancel that tears them all down. dispatcher==nil or
// c==nil yields a no-op cancel.
// SubscribeTrajectory 处理subscribetrajectory。
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

// NewTrajectorySubscribers is the fx provider that exposes the collector's
// bus subscriptions through the platform SubscriberGroup. It mirrors
// observation.NewObservationSubscribers; BusModule owns lifecycle.
// NewTrajectorySubscribers 创建trajectorysubscribers。
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
