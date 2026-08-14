package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
)

// orchestrationTurnStarter 是 mcp-orch 启动 turn 的适配器，隔离编排层与 turn service 细节。
type orchestrationTurnStarter struct {
	turns         Service
	sessions      SessionProvider
	runtimeReader ThreadStateConfigReader
}

// sessionReadyPollInterval 控制编排等待 agent session ready 的轮询间隔。
const sessionReadyPollInterval = 50 * time.Millisecond

// NewOrchestrationTurnStarter 创建编排侧 turn starter，缺失依赖会在调用时 fail-fast。
func NewOrchestrationTurnStarter(turns Service, sessions SessionProvider, runtimeReader ThreadStateConfigReader) contract.OrchestrationTurnStarter {
	return orchestrationTurnStarter{turns: turns, sessions: sessions, runtimeReader: runtimeReader}
}

// WaitForSessionReady 等待指定 agent 的 session 可用，超时或缺配置返回明确错误。
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

// StartTurn 从编排提交构造 PrepareInput 并启动 turn，ExpectedTurnID 可覆盖本地 ID。
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
	if threadID != "" {
		req.ThreadID = threadID
	}
	handle, err := s.turns.StartTurn(ctx, session, req)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(handle.LocalID()), nil
}

// sessionLookupError 把底层 session-not-found 转成编排调用方更容易理解的错误。
func sessionLookupError(err error) error {
	if errors.Is(err, contract.ErrSessionNotFound) {
		return errors.New("agent session not ready, ensure agent/launch completed")
	}
	return err
}

// readQueuedThreadRuntimeConfig 读取排队线程运行时配置，并强制校验 cwd 已存在。
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

// prepareQueuedTurnInput 把编排提交转换成 turn service 的 PrepareInput。
func prepareQueuedTurnInput(session sessionCaps, submission contract.TurnSubmission, threadRuntimeConfig map[string]any) PrepareInput {
	return buildPrepareInput(prepareInputSpec{
		LocalTurnID:           strings.TrimSpace(submission.ExpectedTurnID),
		Inputs:               submission.Inputs,
		ManualSkillSelection: submission.ManualSkillSelection,
		OutputSchema:         append(json.RawMessage(nil), submission.OutputSchema...),
		AgentID:              strings.TrimSpace(submission.AgentID),
		ThreadRuntimeConfig:  threadRuntimeConfig,
	}, prepareSkillSpec{
		Selected: submission.SelectedSkills,
	}, session)
}

// sessionCaps 是编排 starter 需要从 session 读取的最小能力集合。
type sessionCaps interface {
	Capabilities() dto.CapabilitySet
	ThreadID() string
}

// queuedThreadID 解析提交目标线程，兼容旧调用把 agentID 误放在 ThreadID 的形态。
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
