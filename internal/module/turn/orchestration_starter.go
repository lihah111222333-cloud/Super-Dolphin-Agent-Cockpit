package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

type orchestrationTurnStarter struct {
	turns         Service
	sessions      SessionProvider
	runtimeReader ThreadStateConfigReader
}

const sessionReadyPollInterval = 50 * time.Millisecond

// NewOrchestrationTurnStarter 创建orchestrationturnstarter。
func NewOrchestrationTurnStarter(turns Service, sessions SessionProvider, runtimeReader ThreadStateConfigReader) contract.OrchestrationTurnStarter {
	return orchestrationTurnStarter{turns: turns, sessions: sessions, runtimeReader: runtimeReader}
}

// WaitForSessionReady 为会话ready等待turn。
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
		ctx, cancel = ctxutil.WithTimeout(ctx, timeout)
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

// StartTurn 启动turn。
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
	threadID := queuedThreadID(session, submission)
	threadRuntimeConfig, err := readQueuedThreadRuntimeConfig(ctx, s.runtimeReader, threadID)
	if err != nil {
		return "", err
	}
	req, err := s.turns.PrepareTurn(ctx, session, prepareQueuedTurnInput(session, submission, threadRuntimeConfig))
	if err != nil {
		return "", err
	}
	if turnID := strings.TrimSpace(submission.ExpectedTurnID); turnID != "" {
		req.LocalID = turnID
	}
	if threadID != "" {
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
		return errors.New("agent session not ready, ensure agent/launch completed")
	}
	return err
}

func readQueuedThreadRuntimeConfig(ctx context.Context, reader ThreadStateConfigReader, threadID string) (map[string]any, error) {
	cfg, err := readThreadRuntimeConfig(ctx, reader, threadID)
	if err != nil {
		return nil, err
	}
	if _, err := resolveTurnRPCCWD("", cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func prepareQueuedTurnInput(session sessionCaps, submission contract.TurnSubmission, threadRuntimeConfig map[string]any) PrepareInput {
	return buildPrepareInput(prepareInputSpec{
		Inputs:               submission.Inputs,
		ManualSkillSelection: submission.ManualSkillSelection,
		OutputSchema:         append(json.RawMessage(nil), submission.OutputSchema...),
		AgentID:              strings.TrimSpace(submission.AgentID),
		ThreadRuntimeConfig:  threadRuntimeConfig,
	}, prepareSkillSpec{
		Selected: submission.SelectedSkills,
	}, session)
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
