package orchestration

import (
	"strconv"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

type EventBus = *event.Dispatcher
type agentState = agentRuntime
type eventPublisher func(EventBus, *agentState, []any)

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
	})
}

func publishAgentLaunchedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentLaunched{
		AgentSessionHeader: agentSessionHeader(agent),
		CWD:                eventString(values, 0),
	})
}

func publishAgentStoppedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentStopped{
		AgentSessionHeader: agentSessionHeader(agent),
		Reason:             eventString(values, 0),
	})
}

func publishAgentRecoveringEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentRecovering{
		AgentSessionHeader: agentSessionHeader(agent),
		Reason:             eventString(values, 0),
	})
}

func publishAgentFailedEvent(bus EventBus, agent *agentState, values []any) {
	if agent == nil {
		return
	}
	event.Publish(bus, agentdto.AgentFailed{
		AgentSessionHeader: agentSessionHeader(agent),
		Error:              eventString(values, 0),
		Recoverable:        eventBool(values, 1),
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

func resetLaunchState(agent *agentState) {
	if agent == nil {
		return
	}
	agent.cmd = nil
	agent.monitoredSeq = 0
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.remoteThreadID = ""
	agent.remoteAgentID = ""
	agent.startedAt = time.Time{}
	agent.updatedAt = time.Time{}
	agent.exitedAt = nil
}

func cleanupAgentState(agent *agentState) {
	if agent == nil {
		return
	}
	if agent.queue != nil {
		agent.queue.Clear()
	}
	agent.activeTurnID = ""
	agent.threadID = ""
}

func agentSessionHeader(agent *agentState) shared.AgentSessionHeader {
	return shared.AgentSessionHeader{
		AgentHeader: agentHeader(agent),
		SessionID:   agentSessionID(agent),
	}
}

func agentHeader(agent *agentState) shared.AgentHeader {
	agentID := ""
	threadID := ""
	if agent != nil {
		agentID = agent.id
		threadID = agent.threadID
	}
	return shared.AgentHeader{
		ThreadHeader: shared.ThreadHeader{
			EventHeader: shared.EventHeader{Timestamp: agentEventTime(agent)},
			ThreadID:    threadID,
		},
		AgentID: agentID,
	}
}

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

func agentSessionID(agent *agentState) string {
	if agent == nil || agent.launchSeq == 0 {
		return ""
	}
	return strconv.FormatUint(agent.launchSeq, 10)
}

func turnHeader(agent *agentState, threadID, turnID string, timestamp time.Time) shared.TurnHeader {
	header := agentHeader(agent)
	if threadID != "" {
		header.ThreadID = threadID
	}
	if !timestamp.IsZero() {
		header.ThreadHeader.EventHeader.Timestamp = timestamp
	}
	return shared.TurnHeader{
		AgentHeader:  header,
		TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
	}
}

func stalledMilliseconds(stalled time.Duration) int64 {
	if stalled <= 0 {
		return 0
	}
	return stalled.Milliseconds()
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
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
	return agent.state
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
