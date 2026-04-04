package orchestration

import "time"

func (s *service) publishStateChanged(agent *agentRuntime, before, trigger string) {
	if agent != nil && before == agent.state {
		return
	}
	emitEvent(s.eventBus, eventTypeStateChanged, eventAgentID(agent), agent, before, trigger)
}

func (s *service) publishAgentLaunched(agent *agentRuntime) {
	emitEvent(s.eventBus, eventTypeAgentLaunched, eventAgentID(agent), agent, agent.cwd)
}

func (s *service) publishAgentStopped(agent *agentRuntime, reason string) {
	emitEvent(s.eventBus, eventTypeAgentStopped, eventAgentID(agent), agent, reason)
}

func (s *service) publishAgentRecovering(agent *agentRuntime, reason string) {
	emitEvent(s.eventBus, eventTypeAgentRecovering, eventAgentID(agent), agent, reason)
}

func (s *service) publishAgentFailed(agent *agentRuntime, err string, recoverable bool) {
	emitEvent(s.eventBus, eventTypeAgentFailed, eventAgentID(agent), agent, err, recoverable)
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

func (s *service) publishTurnResumed(agent *agentRuntime, threadID, turnID, reason string, timestamp time.Time) {
	agentID := ""
	if agent != nil {
		agentID = agent.id
	}
	emitEvent(s.eventBus, eventTypeTurnResumed, agentID, agent, threadID, turnID, reason, timestamp)
}

func eventAgentID(agent *agentRuntime) string {
	if agent == nil {
		return ""
	}
	return agent.id
}
