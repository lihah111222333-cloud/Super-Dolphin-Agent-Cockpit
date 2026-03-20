package orchestration

import (
	"context"
	"log/slog"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

type StallDetector struct {
	threshold time.Duration
	logger    *slog.Logger
}

func (d *StallDetector) CheckStall(agent *agentRuntime) bool {
	if agent.state != agentdto.StateTurnRunning {
		return false
	}
	stalled := time.Since(agent.updatedAt) > d.threshold
	if stalled && d.logger != nil {
		d.logger.Warn("orchestration: stalled agent detected", "agent_id", agent.id)
	}
	return stalled
}

func (s *service) Recover(ctx context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, err := s.lookupAgentLocked(agentID)
	if err != nil {
		return err
	}
	s.publishAgentRecovering(agent, "manual")
	if err := recoverAgent(ctx, s, agent); err != nil {
		return err
	}
	s.logger.Info("orchestration: agent recovered", "agent_id", agent.id, "pid", processID(agent.cmd))
	return nil
}

func recoverAgent(ctx context.Context, s *service, agent *agentRuntime) error {
	if err := stopProcess(agent.cmd); err != nil {
		return err
	}
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.monitoredSeq = 0
	if err := normalizeRecoveryState(ctx, s, agent); err != nil {
		return err
	}
	return s.startProcessLocked(ctx, agent)
}

func normalizeRecoveryState(ctx context.Context, s *service, agent *agentRuntime) error {
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerRecoverRequested)
}
