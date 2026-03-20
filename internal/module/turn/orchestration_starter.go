package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
)

type orchestrationTurnStarter struct {
	turns    Service
	sessions SessionProvider
}

func NewOrchestrationTurnStarter(turns Service, sessions SessionProvider) orchestration.TurnStarter {
	return orchestrationTurnStarter{turns: turns, sessions: sessions}
}

func (s orchestrationTurnStarter) StartTurn(ctx context.Context, submission orchestration.TurnSubmission) (string, error) {
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
		return "", err
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

func prepareQueuedTurnInput(session sessionCaps, submission orchestration.TurnSubmission) PrepareInput {
	return PrepareInput{
		Inputs:               append([]InputItem(nil), submission.Inputs...),
		Skills:               selectedSkillRefs(submission.SelectedSkills),
		ManualSkillSelection: submission.ManualSkillSelection,
		OutputSchema:         append(json.RawMessage(nil), submission.OutputSchema...),
		AgentID:              strings.TrimSpace(submission.AgentID),
		ThreadCaps:           session.Capabilities(),
	}
}

type sessionCaps interface {
	Capabilities() dto.CapabilitySet
	ThreadID() string
}

func queuedThreadID(session sessionCaps, submission orchestration.TurnSubmission) string {
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

func selectedSkillRefs(names []string) []dto.SkillRef {
	refs := make([]dto.SkillRef, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		refs = append(refs, dto.SkillRef{Name: name})
	}
	return refs
}
