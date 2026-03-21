package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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

// Recover currently implements stop-and-restart semantics only. It does not
// replay active turns or hydrate in-memory state from persisted runtime state.
// TODO(P8): add turn replay and state hydration semantics.
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
	replay, shouldReplay, err := loadRecoveredTurnSubmission(ctx, s, agent)
	if err != nil {
		return err
	}
	if err := stopProcess(agent.cmd); err != nil {
		return err
	}
	agent.stopRequested = false
	agent.activeTurnID = ""
	agent.monitoredSeq = 0
	if err := normalizeRecoveryState(ctx, s, agent); err != nil {
		return err
	}
	if err := s.startProcessLocked(ctx, agent); err != nil {
		return err
	}
	if !shouldReplay {
		return nil
	}
	if err := replayRecoveredTurn(ctx, s, agent, replay); err != nil {
		return err
	}
	s.logger.Info(
		"orchestration: queued recovered active turn replay",
		"agent_id", agent.id,
		"turn_id", replay.ExpectedTurnID,
	)
	return nil
}

func normalizeRecoveryState(ctx context.Context, s *service, agent *agentRuntime) error {
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerRecoverRequested)
}

// Replay is currently supported only when the active turn can be reconstructed
// from persisted DAG wakeup payloads.
func loadRecoveredTurnSubmission(ctx context.Context, s *service, agent *agentRuntime) (TurnSubmission, bool, error) {
	activeTurnID, ok := validateRecoveryContext(s, agent)
	if !ok {
		return TurnSubmission{}, false, nil
	}
	nodes, err := s.recoveryStore.ListRunningNodesByAssignee(ctx, agent.id)
	if err != nil {
		return TurnSubmission{}, false, fmt.Errorf("recover replay: list running nodes for %q: %w", agent.id, err)
	}
	for _, node := range nodes {
		if node.ActiveTurnID == nil || strings.TrimSpace(*node.ActiveTurnID) != activeTurnID {
			continue
		}
		if node.ActiveWakeupID == nil || *node.ActiveWakeupID <= 0 {
			return TurnSubmission{}, false, fmt.Errorf("recover replay: node %s/%s missing active wakeup for turn %q", node.DagKey, node.NodeKey, activeTurnID)
		}
		wakeup, err := s.recoveryStore.GetWakeup(ctx, *node.ActiveWakeupID)
		if err != nil {
			return TurnSubmission{}, false, fmt.Errorf("recover replay: load wakeup %d for turn %q: %w", *node.ActiveWakeupID, activeTurnID, err)
		}
		submission, err := decodeRecoveredTurnSubmission(wakeup.PromptPayload, agent, activeTurnID)
		if err != nil {
			return TurnSubmission{}, false, fmt.Errorf("recover replay: decode wakeup %d for turn %q: %w", wakeup.ID, activeTurnID, err)
		}
		return submission, true, nil
	}
	return TurnSubmission{}, false, nil
}

func validateRecoveryContext(s *service, agent *agentRuntime) (string, bool) {
	if s == nil || agent == nil {
		return "", false
	}
	activeTurnID := strings.TrimSpace(agent.activeTurnID)
	if activeTurnID == "" || s.recoveryStore == nil {
		return "", false
	}
	return activeTurnID, true
}

func decodeRecoveredTurnSubmission(raw json.RawMessage, agent *agentRuntime, activeTurnID string) (TurnSubmission, error) {
	raw = append(json.RawMessage(nil), raw...)
	var direct TurnSubmission
	if err := json.Unmarshal(raw, &direct); err == nil && len(direct.Inputs) > 0 {
		return normalizeRecoveredTurnSubmission(agent, activeTurnID, direct), nil
	}
	var params submitParams
	if err := json.Unmarshal(raw, &params); err == nil {
		items, decodeErr := inputItemsFromSubmitParams(params)
		if decodeErr != nil {
			return TurnSubmission{}, decodeErr
		}
		return normalizeRecoveredTurnSubmission(agent, activeTurnID, TurnSubmission{
			AgentID:              strings.TrimSpace(params.AgentID),
			Inputs:               items,
			SelectedSkills:       append([]string(nil), params.SelectedSkills...),
			ManualSkillSelection: params.ManualSkillSelection,
			OutputSchema:         append(json.RawMessage(nil), params.OutputSchema...),
		}), nil
	}
	return TurnSubmission{}, fmt.Errorf("unsupported prompt payload shape")
}

func normalizeRecoveredTurnSubmission(agent *agentRuntime, activeTurnID string, submission TurnSubmission) TurnSubmission {
	normalized := cloneTurnSubmission(submission)
	if agent != nil {
		normalized.AgentID = firstTrimmed(normalized.AgentID, agent.id)
		normalized.ThreadID = firstTrimmed(normalized.ThreadID, agent.threadID, agent.id)
	} else {
		normalized.AgentID = firstTrimmed(normalized.AgentID)
		normalized.ThreadID = firstTrimmed(normalized.ThreadID)
	}
	normalized.ExpectedTurnID = firstTrimmed(normalized.ExpectedTurnID, activeTurnID)
	return normalized
}

func replayRecoveredTurn(ctx context.Context, s *service, agent *agentRuntime, submission TurnSubmission) error {
	if agent == nil {
		return nil
	}
	if agent.queue == nil {
		agent.queue = &SubmissionQueue{}
	}
	agent.queue.Prepend(submission)
	return s.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued)
}
