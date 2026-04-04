package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type orchestrationTurnStarter struct {
	turns    Service
	sessions SessionProvider
}

const sessionReadyPollInterval = 50 * time.Millisecond

func NewOrchestrationTurnStarter(turns Service, sessions SessionProvider) contract.OrchestrationTurnStarter {
	return orchestrationTurnStarter{turns: turns, sessions: sessions}
}

func (s orchestrationTurnStarter) WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error {
	if s.sessions == nil {
		return errors.New("turn session provider is not configured")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("agent id is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = platformconfig.WithTimeout(ctx, timeout)
		defer cancel()
	}
	ticker := time.NewTicker(sessionReadyPollInterval)
	defer ticker.Stop()
	for {
		_, err := s.sessions.GetSession(agentID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, contract.ErrSessionNotFound) {
			return err
		}
		select {
		case <-ctx.Done():
			return sessionLookupError(err)
		case <-ticker.C:
		}
	}
}

func (s orchestrationTurnStarter) StartTurn(ctx context.Context, submission contract.TurnSubmission) (string, error) {
	if s.turns == nil {
		return "", errors.New("turn service is not configured")
	}
	if s.sessions == nil {
		return "", errors.New("turn session provider is not configured")
	}
	agentID := strings.TrimSpace(submission.AgentID)
	if agentID == "" {
		return "", errors.New("agent id is required")
	}
	session, err := s.sessions.GetSession(agentID)
	if err != nil {
		return "", sessionLookupError(err)
	}
	req, err := s.turns.PrepareTurn(ctx, session, prepareQueuedTurnInput(session, submission))
	if err != nil {
		return "", err
	}
	if turnID := strings.TrimSpace(submission.ExpectedTurnID); turnID != "" {
		req.LocalID = turnID
	}
	if threadID := queuedThreadID(session, submission); threadID != "" {
		req.ThreadID = threadID
	}
	handle, err := s.turns.StartTurn(ctx, session, req)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(handle.LocalID()), nil
}

func sessionLookupError(err error) error {
	if errors.Is(err, contract.ErrSessionNotFound) {
		return errors.New("agent session not ready, ensure agent.launch completed")
	}
	return err
}

func prepareQueuedTurnInput(session sessionCaps, submission contract.TurnSubmission) PrepareInput {
	return buildPrepareInput(prepareInputSpec{
		Inputs:               submission.Inputs,
		ManualSkillSelection: submission.ManualSkillSelection,
		OutputSchema:         append(json.RawMessage(nil), submission.OutputSchema...),
		AgentID:              strings.TrimSpace(submission.AgentID),
	}, prepareSkillSpec{
		Selected: submission.SelectedSkills,
	}, session.Capabilities())
}

type sessionCaps interface {
	Capabilities() dto.CapabilitySet
	ThreadID() string
}

func queuedThreadID(session sessionCaps, submission contract.TurnSubmission) string {
	threadID := strings.TrimSpace(submission.ThreadID)
	if threadID == "" {
		return strings.TrimSpace(session.ThreadID())
	}
	sessionThreadID := strings.TrimSpace(session.ThreadID())
	if sessionThreadID != "" && threadID == strings.TrimSpace(submission.AgentID) {
		return sessionThreadID
	}
	return threadID
}
