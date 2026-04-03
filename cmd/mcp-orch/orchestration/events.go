package orchestration

import "time"

func (s *service) publishStateChanged(agent *agentRuntime, before, trigger string) {
	if agent == nil || before == agent.state {
		return
	}
	emitEvent(s.eventBus, eventTypeStateChanged, agent.id, agent, before, trigger)
}

func (s *service) publishAgentLaunched(agent *agentRuntime) {
	if agent == nil {
		return
	}
	emitEvent(s.eventBus, eventTypeAgentLaunched, agent.id, agent, agent.cwd)
}

func (s *service) publishAgentStopped(agent *agentRuntime, reason string) {
	if agent == nil {
		return
	}
	emitEvent(s.eventBus, eventTypeAgentStopped, agent.id, agent, reason)
}

func (s *service) publishAgentRecovering(agent *agentRuntime, reason string) {
	if agent == nil {
		return
	}
	emitEvent(s.eventBus, eventTypeAgentRecovering, agent.id, agent, reason)
}

func (s *service) publishAgentFailed(agent *agentRuntime, err string, recoverable bool) {
	if agent == nil {
		return
	}
	emitEvent(s.eventBus, eventTypeAgentFailed, agent.id, agent, err, recoverable)
}

func (s *service) publishAgentRuntimeReported(agent *agentRuntime) {
	if agent == nil {
		return
	}
	port, _ := snapshotPort(agent)
	provider, _ := snapshotProvider(agent)
	emitEvent(s.eventBus, eventTypeAgentRuntimeReported, agent.id, agent, port, provider)
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
