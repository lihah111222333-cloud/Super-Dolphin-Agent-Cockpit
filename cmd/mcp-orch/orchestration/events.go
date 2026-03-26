package orchestration

import (
	"strconv"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func (s *service) publishStateChanged(agent *agentRuntime, before, trigger string) {
	if s.eventBus == nil || before == agent.state {
		return
	}
	event.Publish(s.eventBus, agentdto.StateChanged{
		AgentSessionHeader: s.agentSessionHeader(agent),
		OldState:           before,
		NewState:           agent.state,
		Trigger:            trigger,
	})
}

func (s *service) publishAgentLaunched(agent *agentRuntime) {
	if s.eventBus == nil {
		return
	}
	event.Publish(s.eventBus, agentdto.AgentLaunched{
		AgentSessionHeader: s.agentSessionHeader(agent),
		CWD:                agent.cwd,
	})
}

func (s *service) publishAgentStopped(agent *agentRuntime, reason string) {
	if s.eventBus == nil {
		return
	}
	event.Publish(s.eventBus, agentdto.AgentStopped{
		AgentSessionHeader: s.agentSessionHeader(agent),
		Reason:             reason,
	})
}

func (s *service) publishAgentRecovering(agent *agentRuntime, reason string) {
	if s.eventBus == nil {
		return
	}
	event.Publish(s.eventBus, agentdto.AgentRecovering{
		AgentSessionHeader: s.agentSessionHeader(agent),
		Reason:             reason,
	})
}

func (s *service) publishAgentFailed(agent *agentRuntime, err string, recoverable bool) {
	if s.eventBus == nil {
		return
	}
	event.Publish(s.eventBus, agentdto.AgentFailed{
		AgentSessionHeader: s.agentSessionHeader(agent),
		Error:              err,
		Recoverable:        recoverable,
	})
}

func (s *service) publishAgentRuntimeReported(agent *agentRuntime) {
	if s.eventBus == nil {
		return
	}
	port, _ := snapshotPort(agent)
	provider, _ := snapshotProvider(agent)
	event.Publish(s.eventBus, agentdto.AgentRuntimeReported{
		AgentSessionHeader: s.agentSessionHeader(agent),
		Port:               port,
		Provider:           provider,
	})
}

func (s *service) publishTurnStalled(
	agent *agentRuntime,
	threadID string,
	turnID string,
	reason string,
	stalled time.Duration,
	timestamp time.Time,
) {
	if s.eventBus == nil || turnID == "" {
		return
	}
	event.Publish(s.eventBus, turndto.TurnStalled{
		TurnHeader: turnHeader(agent, threadID, turnID, timestamp),
		Reason:     reason,
		StalledMS:  stalledMilliseconds(stalled),
	})
}

func (s *service) publishTurnResumed(agent *agentRuntime, threadID, turnID, reason string, timestamp time.Time) {
	if s.eventBus == nil || turnID == "" {
		return
	}
	event.Publish(s.eventBus, turndto.TurnResumed{
		TurnHeader: turnHeader(agent, threadID, turnID, timestamp),
		Reason:     reason,
	})
}

func (s *service) agentSessionHeader(agent *agentRuntime) shared.AgentSessionHeader {
	return shared.AgentSessionHeader{
		AgentHeader: agentHeader(agent),
		SessionID:   agentSessionID(agent),
	}
}

func agentHeader(agent *agentRuntime) shared.AgentHeader {
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

func agentEventTime(agent *agentRuntime) time.Time {
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

func agentSessionID(agent *agentRuntime) string {
	if agent.launchSeq == 0 {
		return ""
	}
	return strconv.FormatUint(agent.launchSeq, 10)
}

func turnHeader(agent *agentRuntime, threadID, turnID string, timestamp time.Time) shared.TurnHeader {
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
