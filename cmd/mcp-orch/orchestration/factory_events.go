package orchestration

import (
	"context"
	"strings"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// EventBus 是 orchestration 状态事件发布边界的本包别名。
type EventBus = *event.Dispatcher
type agentState = agentRuntime
type eventPublisher func(EventBus, *agentState, []any)

type activeTurnFinalizationKind struct {
	trigger    agentdto.AgentTrigger
	errorText  string
	clearError bool
}

type turnRecoveryOwner interface {
	recoverTurnCompletionStateLocked(context.Context, *agentRuntime, bool) error
	recoverTurnInterruptionStateLocked(context.Context, *agentRuntime) error
}

type activeTurnRecoveryKind struct {
	recoveredTrigger string
	errorText        string
	clearError       bool
	recover          func(context.Context, turnRecoveryOwner, *agentRuntime) error
}

const (
	eventTypeStateChanged         = "state_changed"
	eventTypeAgentLaunched        = "agent_launched"
	eventTypeAgentStopped         = "agent_stopped"
	eventTypeAgentRecovering      = "agent_recovering"
	eventTypeAgentFailed          = "agent_failed"
	eventTypeAgentRuntimeReported = "agent_runtime_reported"
	eventTypeTurnStalled          = "turn_stalled"
	eventTypeTurnResumed          = "turn_resumed"
)

var eventPublishers = map[string]eventPublisher{
	eventTypeStateChanged:         publishStateChangedEvent,
	eventTypeAgentLaunched:        publishAgentLaunchedEvent,
	eventTypeAgentStopped:         publishAgentStoppedEvent,
	eventTypeAgentRecovering:      publishAgentRecoveringEvent,
	eventTypeAgentFailed:          publishAgentFailedEvent,
	eventTypeAgentRuntimeReported: publishAgentRuntimeReportedEvent,
	eventTypeTurnStalled:          publishTurnStalledEvent,
	eventTypeTurnResumed:          publishTurnResumedEvent,
}

func emitEvent(bus EventBus, eventType string, agentID string, fields ...any) {
	if bus == nil {
		return
	}
	agent, values := eventAgent(agentID, fields)
	publish, ok := eventPublishers[eventType]
	if !ok {
		return
	}
	publish(bus, agent, values)
}

func publishStateChangedEvent(bus EventBus, agent *agentState, values []any) {
	if len(values) < 2 {
		return
	}
	event.Publish(bus, agentdto.StateChanged{
		AgentSessionHeader: agentSessionHeader(agent),
		OldState:           eventString(values, 0),
		NewState:           agentStateValue(agent),
		Trigger:            eventString(values, 1),
		Board:              agentBoardView(agent),
	})
}

func publishAgentLaunchedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	provider, _ := snapshotProvider(agent)
	event.Publish(bus, agentdto.AgentLaunched{
		AgentSessionHeader: agentSessionHeader(agent),
		CWD:                eventString(values, 0),
		Name:               agent.name,
		Provider:           provider,
		Board:              agentBoardView(agent),
	})
}

func publishAgentStoppedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	reason := strings.TrimSpace(eventString(values, 0))
	outcome := &agentdto.Outcome{Kind: agentdto.OutcomeKindStopped, Reason: reason, Code: reason, CompletedAt: agentEventTime(agent)}
	agent.outcome = nil
	if outcome.Validate() == nil {
		agent.outcome = outcome
	}
	event.Publish(bus, agentdto.AgentStopped{
		AgentSessionHeader: agentSessionHeader(agent),
		Reason:             reason,
		Board:              agentBoardView(agent),
	})
}

func publishAgentRecoveringEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentRecovering{
		AgentSessionHeader: agentSessionHeader(agent),
		Reason:             eventString(values, 0),
		Board:              agentBoardView(agent),
	})
}

func publishAgentFailedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	reason := strings.TrimSpace(eventString(values, 0))
	recoverable := eventBool(values, 1)
	outcome := &agentdto.Outcome{Kind: agentdto.OutcomeKindFailure, Reason: reason, Code: "agent_failed", Recoverable: &recoverable, CompletedAt: agentEventTime(agent)}
	agent.outcome = nil
	if outcome.Validate() == nil {
		agent.outcome = outcome
	}
	event.Publish(bus, agentdto.AgentFailed{
		AgentSessionHeader: agentSessionHeader(agent),
		Error:              reason,
		Recoverable:        recoverable,
		Board:              agentBoardView(agent),
	})
}

func publishAgentRuntimeReportedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentRuntimeReported{
		AgentSessionHeader: agentSessionHeader(agent),
		Port:               eventInt(values, 0),
		Provider:           eventString(values, 1),
	})
}

func publishTurnStalledEvent(bus EventBus, agent *agentState, values []any) {
	turnID := eventString(values, 1)
	if turnID == "" {
		return
	}
	event.Publish(bus, turndto.TurnStalled{
		TurnHeader: turnHeader(agent, eventString(values, 0), turnID, eventTime(values, 4)),
		Reason:     eventString(values, 2),
		StalledMS:  stalledMilliseconds(eventDuration(values, 3)),
	})
}

func publishTurnResumedEvent(bus EventBus, agent *agentState, values []any) {
	turnID := eventString(values, 1)
	if turnID == "" {
		return
	}
	event.Publish(bus, turndto.TurnResumed{
		TurnHeader: turnHeader(agent, eventString(values, 0), turnID, eventTime(values, 3)),
		Reason:     eventString(values, 2),
	})
}

func agentSessionHeader(agent *agentState) shareddto.AgentSessionHeader {
	return shareddto.AgentSessionHeader{
		AgentHeader: agentHeader(agent),
		SessionID:   agentSessionID(agent),
	}
}

func agentHeader(agent *agentState) shareddto.AgentHeader {
	agentID := ""
	threadID := ""
	if agent != nil {
		agentID = agent.id
		threadID = agent.threadID
	}
	return shareddto.AgentHeader{
		ThreadHeader: shareddto.ThreadHeader{
			EventHeader: shareddto.EventHeader{Timestamp: agentEventTime(agent)},
			ThreadID:    threadID,
		},
		AgentID: agentID,
	}
}

// agentEventTime 为事件选择最接近当前状态变化的时间。
// updatedAt 优先，其次启动/退出时间；都缺失时返回平台统一的初始时间。
func agentEventTime(agent *agentState) time.Time {
	if agent == nil {
		return shared.FirstEventTime()
	}
	if !agent.updatedAt.IsZero() {
		return agent.updatedAt
	}
	if !agent.startedAt.IsZero() {
		return agent.startedAt
	}
	if agent.exitedAt != nil && !agent.exitedAt.IsZero() {
		return *agent.exitedAt
	}
	return shared.FirstEventTime()
}

func turnHeader(agent *agentState, threadID, turnID string, timestamp time.Time) shareddto.TurnHeader {
	header := agentHeader(agent)
	if threadID != "" {
		header.ThreadID = threadID
	}
	if !timestamp.IsZero() {
		header.Timestamp = timestamp
	}
	return shareddto.TurnHeader{
		AgentHeader:  header,
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}
}

func stalledMilliseconds(stalled time.Duration) int64 {
	if stalled <= 0 {
		return 0
	}
	return stalled.Milliseconds()
}

func eventAgent(agentID string, fields []any) (*agentState, []any) {
	if len(fields) == 0 {
		return fallbackAgent(agentID), fields
	}
	agent, ok := fields[0].(*agentState)
	if !ok {
		return fallbackAgent(agentID), fields
	}
	if agent == nil {
		return fallbackAgent(agentID), fields[1:]
	}
	return agent, fields[1:]
}

func fallbackAgent(agentID string) *agentState {
	if agentID == "" {
		return nil
	}
	return &agentState{id: agentID}
}

func agentStateValue(agent *agentState) string {
	if agent == nil {
		return ""
	}
	return string(agent.state)
}

func eventString(fields []any, index int) string {
	if index >= len(fields) {
		return ""
	}
	value, _ := fields[index].(string)
	return value
}

func eventBool(fields []any, index int) bool {
	if index >= len(fields) {
		return false
	}
	value, _ := fields[index].(bool)
	return value
}

func eventInt(fields []any, index int) int {
	if index >= len(fields) {
		return 0
	}
	value, _ := fields[index].(int)
	return value
}

func eventDuration(fields []any, index int) time.Duration {
	if index >= len(fields) {
		return 0
	}
	value, _ := fields[index].(time.Duration)
	return value
}

func eventTime(fields []any, index int) time.Time {
	if index >= len(fields) {
		return time.Time{}
	}
	value, _ := fields[index].(time.Time)
	return value
}

func (s *service) publishStateChanged(agent *agentRuntime, before, trigger string) {
	if agent != nil && before == string(agent.state) {
		return
	}
	emitEvent(s.eventBus, eventTypeStateChanged, eventAgentID(agent), agent, before, trigger)
}

func (s *service) publishAgentRuntimeReported(agent *agentRuntime) {
	port, _ := snapshotPort(agent)
	provider, _ := snapshotProvider(agent)
	emitEvent(s.eventBus, eventTypeAgentRuntimeReported, eventAgentID(agent), agent, port, provider)
}

func (s *service) publishTurnStalled(
	agent *agentRuntime,
	threadID string,
	turnID string,
	reason string,
	stalled time.Duration,
	timestamp time.Time,
) {
	agentID := ""
	if agent != nil {
		agentID = agent.id
	}
	emitEvent(s.eventBus, eventTypeTurnStalled, agentID, agent, threadID, turnID, reason, stalled, timestamp)
}

func eventAgentID(agent *agentRuntime) string {
	if agent == nil {
		return ""
	}
	return agent.id
}
