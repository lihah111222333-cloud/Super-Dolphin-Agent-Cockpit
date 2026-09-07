package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

var errTerminalProjectionSuperseded = errors.New("terminal outcome projection superseded by a newer lifecycle")

// terminalProjectionStateMatches 接受 durable pre-stop head 与 stopping 中间态的安全组合。
func terminalProjectionStateMatches(agent *agentRuntime, identity contract.CanonicalTerminalIdentity, projectionKind string) bool {
	if agent == nil {
		return false
	}
	if string(agent.state) == identity.ExpectedActiveState {
		return true
	}
	return (projectionKind == "process_stopped" && agent.state == agentdto.StateStopping) ||
		(projectionKind == "process_failed" && agent.state == agentdto.StateRecovering)
}

// terminalProjectionTurnMatches 校验终态投影仍指向当前 turn 或 session terminal。
func terminalProjectionTurnMatches(agent *agentRuntime, identity contract.CanonicalTerminalIdentity) bool {
	if strings.TrimSpace(agent.activeTurnID) == "" {
		return identity.ProviderTurnID == "session-terminal:"+identity.SessionID
	}
	_, ok := canonicalProviderTurnID(agent, identity.ProviderTurnID)
	return ok
}

// terminalProjectionAlreadyApplied 判断相同 identity、状态和报告是否已投影完成。
func terminalProjectionAlreadyApplied(agent *agentRuntime, commit contract.TerminalOutcomeCommit) bool {
	if agent == nil || strings.TrimSpace(agent.activeTurnID) != "" || agent.outcome == nil {
		return false
	}
	expected := agentOutcomeFromPublic(commit.PublicOutcome)
	return terminalProjectionReplayFenceFromAgent(agent) == terminalProjectionReplayFenceFromIdentity(commit.Identity) &&
		terminalProjectionRuntimeStateMatches(agent.state, commit.ProjectionKind) &&
		strings.TrimSpace(agent.lastReport) == strings.TrimSpace(commit.PublicReport) &&
		terminalProjectionOutcomeMatches(agent.outcome, expected, commit.ProjectionKind)
}

// terminalProjectionRuntimeStateMatches 按唯一投影器的生命周期目标判定，而非public outcome展示状态。
// turn成功或中止均释放本轮并回到idle；只有agent/process终态结束runtime生命周期。
func terminalProjectionRuntimeStateMatches(state agentdto.AgentState, projectionKind string) bool {
	switch projectionKind {
	case "turn_completed":
		return state == agentdto.StateIdle
	case "agent_stopped", "process_stopped":
		return state == agentdto.StateStopped
	case "agent_failed", "process_failed":
		return state == agentdto.StateFailed
	default:
		return false
	}
}

// terminalProjectionOutcomeMatches 允许受控 process terminal 的旧 outcome 形状重放。
func terminalProjectionOutcomeMatches(actual, expected *agentdto.Outcome, projectionKind string) bool {
	if actual == nil || expected == nil {
		return false
	}
	if *actual == *expected {
		return true
	}
	return isProcessTerminalProjection(projectionKind) && actual.Kind == expected.Kind
}

type terminalProjectionReplayFence struct {
	agentID, publicThreadID, sessionID string
	generation, headVersion            uint64
}

// terminalProjectionReplayFenceFromAgent 将 runtime 的完整终态围栏提取为比较值。
func terminalProjectionReplayFenceFromAgent(agent *agentRuntime) terminalProjectionReplayFence {
	return terminalProjectionReplayFence{
		agentID: strings.TrimSpace(agent.id), publicThreadID: strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID)),
		sessionID: agentSessionID(agent), generation: agent.sessionGeneration, headVersion: agent.terminalHeadVersion,
	}
}

// terminalProjectionReplayFenceFromIdentity 将 durable commit 的终态围栏提取为比较值。
func terminalProjectionReplayFenceFromIdentity(identity contract.CanonicalTerminalIdentity) terminalProjectionReplayFence {
	return terminalProjectionReplayFence{
		agentID: identity.AgentID, publicThreadID: identity.PublicThreadID,
		sessionID: identity.SessionID, generation: identity.Generation, headVersion: identity.HeadVersion,
	}
}

// alignProcessExitTerminalFence 读取并绑定 process-stop 的 durable pre-stop head。
func (s *service) alignProcessExitTerminalFence(ctx context.Context, agent *agentRuntime, commit *contract.TerminalOutcomeCommit) error {
	if s == nil || s.terminalHeadReader == nil || agent == nil || commit == nil {
		return nil
	}
	head, err := s.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, agent.id)
	if err != nil {
		return fmt.Errorf("load process-exit terminal head: %w", err)
	}
	if !processExitHeadMatches(head, commit.Identity) {
		return contract.ErrTerminalOutcomeConflict
	}
	commit.Identity.ExpectedActiveState = head.ExpectedActiveState
	return nil
}

// processExitHeadMatches 严格比较 process-stop commit 与 durable head 的身份字段。
func processExitHeadMatches(head contract.DurableTerminalOutcomeHead, identity contract.CanonicalTerminalIdentity) bool {
	return head.State == "active" && head.Capability == identity.Capability && head.AgentID == identity.AgentID &&
		head.PublicThreadID == identity.PublicThreadID && head.ProviderTurnID == identity.ProviderTurnID &&
		head.SessionID == identity.SessionID && head.Generation == identity.Generation && head.Version == identity.HeadVersion
}

// terminalProjectionSuperseded 判断旧 process-exit commit 是否已被新代际明确取代。
func (s *service) terminalProjectionSuperseded(ctx context.Context, agent *agentRuntime, commit contract.TerminalOutcomeCommit) (bool, error) {
	if commit.ProjectionKind == "turn_completed" {
		return s.completedTurnSupersededByProcessHead(ctx, agent, commit.Identity)
	}
	if agent == nil || !isProcessTerminalProjection(commit.ProjectionKind) {
		return false, nil
	}
	identity := commit.Identity
	if agentSessionID(agent) == identity.SessionID && agent.sessionGeneration == identity.Generation {
		return false, nil
	}
	if supersededByNewLaunch(agent, identity) {
		return true, nil
	}
	return supersededByDurableHead(ctx, s, identity, agentSessionID(agent))
}

// supersededByNewLaunch 只按可解析的数值 session generation 判断新代际。
func supersededByNewLaunch(agent *agentRuntime, identity contract.CanonicalTerminalIdentity) bool {
	oldSeq, oldOK := terminalProjectionLaunchSeq(identity.SessionID)
	currentSeq, currentOK := terminalProjectionLaunchSeq(agentSessionID(agent))
	return oldOK && currentOK && currentSeq > oldSeq
}

// supersededByDurableHead 用 durable head 补充重启后无法解析 session 数字的场景。
func supersededByDurableHead(ctx context.Context, service *service, identity contract.CanonicalTerminalIdentity, currentSession string) (bool, error) {
	if service == nil || service.terminalHeadReader == nil {
		return false, nil
	}
	head, err := service.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, identity.AgentID)
	if errors.Is(err, contract.ErrTerminalOutcomeHeadNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load superseded terminal outcome head: %w", err)
	}
	if head.State == "active" && processExitHeadMatches(head, identity) {
		return false, nil
	}
	return durableHeadShowsNewGeneration(head.State, identity, currentSession), nil
}

// durableHeadShowsNewGeneration 仅在 durable head 与当前 session 明确属于不同代际时跳过旧投影。
func durableHeadShowsNewGeneration(state string, identity contract.CanonicalTerminalIdentity, currentSession string) bool {
	oldSeq, oldOK := terminalProjectionLaunchSeq(identity.SessionID)
	currentSeq, currentOK := terminalProjectionLaunchSeq(currentSession)
	return (state == "active" || state == "terminal") && oldOK && currentOK && currentSeq != oldSeq
}

// isProcessTerminalProjection 标识需要在 outbox ACK 后清理 session 的终态类型。
func isProcessTerminalProjection(projectionKind string) bool {
	return projectionKind == "process_stopped" || projectionKind == "process_failed"
}

// terminalProjectionLaunchSeq 解析 legacy process terminal 使用的数值 session generation。
func terminalProjectionLaunchSeq(sessionID string) (uint64, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(sessionID), 10, 64)
	return value, err == nil && value > 0
}
