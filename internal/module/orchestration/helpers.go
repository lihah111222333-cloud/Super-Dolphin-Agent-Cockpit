package orchestration

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformstatemachine "github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

func buildStatesFromDefinitions(defs []agentdto.TransitionDefinition) []platformstatemachine.StateConfig {
	permits := make(map[string][]platformstatemachine.Permit, len(agentdto.StateDefinitions))
	for _, def := range defs {
		permits[def.From] = append(permits[def.From], platformstatemachine.Permit{
			Trigger: def.Trigger,
			Dest:    def.To,
		})
	}
	states := make([]platformstatemachine.StateConfig, 0, len(agentdto.StateDefinitions))
	for _, def := range agentdto.StateDefinitions {
		states = append(states, platformstatemachine.StateConfig{
			Name:    def.Name,
			Permits: permits[def.Name],
		})
	}
	return states
}

func (s *service) agentForLaunchLocked(req LaunchRequest) *agentRuntime {
	agent, ok := s.agents[req.AgentID]
	if !ok {
		agent = s.newAgentLocked(req.AgentID)
		s.agents[req.AgentID] = agent
	}
	agent.name = req.Name
	agent.parentID = req.ParentID
	agent.cwd = req.Cwd
	agent.command = append([]string(nil), req.Command...)
	agent.env = append([]string(nil), req.Env...)
	agent.lastError = ""
	agent.stopRequested = false
	return agent
}

func (s *service) prepareLaunchStateLocked(agent *agentRuntime) {
	agent.lastError = ""
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.threadID = ""
	agent.exitedAt = nil
	agent.updatedAt = time.Now()
	if agent.state != agentdto.StateStopped {
		agent.state = agentdto.StateProvisioning
	}
}

func (s *service) newAgentLocked(agentID string) *agentRuntime {
	agent := &agentRuntime{
		id:        agentID,
		state:     agentdto.StateProvisioning,
		updatedAt: time.Now(),
		queue:     &SubmissionQueue{},
	}
	agent.sm = platformstatemachine.New(s.machineCfg, func() string {
		return agent.state
	}, func(next string) {
		agent.state = next
	})
	return agent
}

func (s *service) claimMonitorTargetsLocked() []monitorTarget {
	s.mu.Lock()
	defer s.mu.Unlock()

	targets := make([]monitorTarget, 0, len(s.agents))
	for _, agent := range s.agents {
		if agent.cmd == nil || agent.monitoredSeq == agent.launchSeq {
			continue
		}
		agent.monitoredSeq = agent.launchSeq
		targets = append(targets, monitorTarget{
			agentID:   agent.id,
			launchSeq: agent.launchSeq,
			cmd:       agent.cmd,
		})
	}
	return targets
}

func (s *service) reconcileReadyStateLocked(ctx context.Context, agent *agentRuntime) {
	if agent.cmd == nil || agent.stopRequested {
		return
	}
	if agent.activeTurnID == "" && agent.state == agentdto.StateIdle && agent.queue.Len() > 0 {
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued, agentdto.StateTurnQueued)
		return
	}
	if agent.activeTurnID != "" {
		return
	}
	switch agent.state {
	case agentdto.StateTurnStarting, agentdto.StateTurnRunning, agentdto.StateAwaitingUserInput:
		s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnCompleted, agentdto.StateIdle)
	}
}

func (s *service) startTurnExecution(ctx context.Context, work turnWork) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(work.agentID)
	if err != nil || agent.activeTurnID != work.turnID {
		return
	}
	s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted, agentdto.StateTurnRunning)
}

func (s *service) listAgents() []agentRuntime {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]agentRuntime, 0, len(s.agents))
	for _, agent := range s.agents {
		snapshot := *agent
		snapshot.cmd = nil
		snapshot.queue = nil
		snapshot.sm = nil
		snapshot.exitedAt = cloneTime(agent.exitedAt)
		agents = append(agents, snapshot)
	}
	return agents
}

func (s *service) lookupAgentLocked(agentID string) (*agentRuntime, error) {
	agent, ok := s.agents[agentID]
	if ok {
		return agent, nil
	}
	return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

func (s *service) turnIDFor(sub TurnSubmission) string {
	if turnID := strings.TrimSpace(sub.ExpectedTurnID); turnID != "" {
		return turnID
	}
	s.nextTurnSeq++
	return fmt.Sprintf("%s-turn-%d", sub.ThreadID, s.nextTurnSeq)
}

func validateLaunchRequest(req LaunchRequest) error {
	if strings.TrimSpace(req.AgentID) == "" {
		return errors.New("agent id is required")
	}
	if len(req.Command) == 0 {
		return errors.New("command is required")
	}
	return nil
}

func stopProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
