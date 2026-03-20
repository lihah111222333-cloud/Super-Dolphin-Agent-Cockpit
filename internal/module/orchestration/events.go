package orchestration

import (
	"os/exec"
	"strconv"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
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

func (s *service) agentSessionHeader(agent *agentRuntime) shared.AgentSessionHeader {
	return shared.AgentSessionHeader{
		AgentHeader: agentHeader(agent.id, agent.threadID),
		SessionID:   agentSessionID(agent),
	}
}

func agentHeader(agentID, threadID string) shared.AgentHeader {
	return shared.AgentHeader{
		ThreadHeader: shared.ThreadHeader{
			EventHeader: shared.EventHeader{Timestamp: time.Now()},
			ThreadID:    threadID,
		},
		AgentID: agentID,
	}
}

func agentSessionID(agent *agentRuntime) string {
	if agent.launchSeq == 0 {
		return ""
	}
	return strconv.FormatUint(agent.launchSeq, 10)
}

func processID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
