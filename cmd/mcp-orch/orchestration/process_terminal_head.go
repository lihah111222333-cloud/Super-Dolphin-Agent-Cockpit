package orchestration

import (
	"context"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

// prepareProcessTerminalHeadLocked 保留活动 turn 的权威 head，只为已投影完成的 idle 创建独立生命周期 head。
func (s *service) prepareProcessTerminalHeadLocked(ctx context.Context, agent *agentRuntime) error {
	if s.terminalOutcomes == nil {
		return nil
	}
	if s.terminalHeadReader == nil {
		return errors.New("process terminal requires durable head reader")
	}
	head, err := s.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, agent.id)
	if err != nil {
		return err
	}
	if !processTerminalHeadOwnerMatches(agent, head) {
		return contract.ErrTerminalOutcomeConflict
	}
	if head.State == "active" {
		if !terminalProjectionTurnMatches(agent, contract.CanonicalTerminalIdentity{ProviderTurnID: head.ProviderTurnID, SessionID: head.SessionID}) {
			return contract.ErrTerminalOutcomeConflict
		}
		return nil
	}
	return s.advanceCompletedTurnToProcessHeadLocked(ctx, agent, head)
}

// processTerminalHeadOwnerMatches 锁住当前代际与 head 版本，不能把外部 head 绑定给旧 runtime。
func processTerminalHeadOwnerMatches(agent *agentRuntime, head contract.DurableTerminalOutcomeHead) bool {
	return head.Capability == contract.TerminalOutcomeCapabilityV2 && head.AgentID == agent.id &&
		head.PublicThreadID == strings.TrimSpace(firstNonEmpty(agent.remoteThreadID, agent.threadID)) &&
		head.SessionID == agentSessionID(agent) && head.Generation == agent.sessionGeneration && head.Version == agent.terminalHeadVersion
}

// advanceCompletedTurnToProcessHeadLocked 不改写已完成 turn 历史，仅 CAS 激活新的 session terminal。
func (s *service) advanceCompletedTurnToProcessHeadLocked(ctx context.Context, agent *agentRuntime, head contract.DurableTerminalOutcomeHead) error {
	if head.State != "terminal" {
		return contract.ErrTerminalOutcomeConflict
	}
	outcome, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, agent.id)
	if err != nil {
		return err
	}
	if outcome.ProjectionKind != "turn_completed" {
		return contract.ErrTerminalOutcomeConflict
	}
	// 公开report来自durable读端，可能先于异步projector返回；仍只使用唯一投影器消费已提交真相。
	if err := s.projectTerminalOutcomeLocked(ctx, agent, outcome); err != nil {
		return err
	}
	if agent.state != agentdto.StateIdle || agent.activeTurnID != "" {
		return contract.ErrTerminalOutcomeConflict
	}
	next, err := s.turns.activateTerminalTurnHeadLocked(ctx, agent, "session-terminal:"+agentSessionID(agent), string(agent.state))
	if err != nil {
		return err
	}
	agent.terminalHeadVersion = next.Version
	return nil
}

// completedTurnSupersededByProcessHead 仅跳过被同会话生命周期head接替的旧runtime投影；outbox仍交付原DAG并ACK。
func (s *service) completedTurnSupersededByProcessHead(ctx context.Context, agent *agentRuntime, identity contract.CanonicalTerminalIdentity) (bool, error) {
	if agent == nil || s.terminalHeadReader == nil || agent.terminalHeadVersion <= identity.HeadVersion {
		return false, nil
	}
	head, err := s.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, identity.AgentID)
	if err != nil {
		return false, err
	}
	if agent.sessionGeneration == 0 {
		return s.cleanedProcessHeadSupersedesTurn(ctx, agent, head, identity)
	}
	if !processTerminalHeadOwnerMatches(agent, head) {
		return false, contract.ErrTerminalOutcomeConflict
	}
	if recoveredHeadSupersedesCompletedTurn(agent, head, identity) {
		return true, nil
	}
	return processHeadSupersedesCompletedTurn(head, identity), nil
}

// processHeadSupersedesCompletedTurn 严格限定同owner、同代际、已前进的session-terminal身份。
func processHeadSupersedesCompletedTurn(head contract.DurableTerminalOutcomeHead, identity contract.CanonicalTerminalIdentity) bool {
	return head.Capability == identity.Capability && head.AgentID == identity.AgentID &&
		head.PublicThreadID == identity.PublicThreadID && head.SessionID == identity.SessionID &&
		head.Generation == identity.Generation && head.Version > identity.HeadVersion &&
		head.ProviderTurnID == "session-terminal:"+identity.SessionID
}

// cleanedProcessHeadSupersedesTurn 仅用已投影的durable进程终态证明generation清理，不修改或复制运行中对象。
func (s *service) cleanedProcessHeadSupersedesTurn(ctx context.Context, agent *agentRuntime, head contract.DurableTerminalOutcomeHead, identity contract.CanonicalTerminalIdentity) (bool, error) {
	if head.State != "terminal" || !processHeadSupersedesCompletedTurn(head, identity) {
		return false, contract.ErrTerminalOutcomeConflict
	}
	outcome, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, identity.AgentID)
	if err != nil {
		return false, err
	}
	if !processTerminalProjectionAppliedAfterCleanup(agent, head, outcome) {
		return false, contract.ErrTerminalOutcomeConflict
	}
	return true, nil
}

// processTerminalProjectionAppliedAfterCleanup 用durable终态及完整投影字段证明generation已被安全清理。
func processTerminalProjectionAppliedAfterCleanup(agent *agentRuntime, head contract.DurableTerminalOutcomeHead, outcome contract.TerminalOutcomeCommit) bool {
	if agent.sessionGeneration != 0 || head.State != "terminal" || !isProcessTerminalProjection(outcome.ProjectionKind) || outcome.Identity.ProviderTurnID != head.ProviderTurnID {
		return false
	}

	// 只构造投影比较字段；generation必须来自已提交head，其余字段全部保持runtime现值。
	projected := &agentRuntime{
		id: agent.id, threadID: agent.threadID, remoteThreadID: agent.remoteThreadID,
		launchSeq: agent.launchSeq, sessionGeneration: head.Generation,
		terminalHeadVersion: agent.terminalHeadVersion, activeTurnID: agent.activeTurnID,
		state: agent.state, lastReport: agent.lastReport, outcome: agent.outcome,
	}
	return processTerminalHeadOwnerMatches(projected, head) && terminalProjectionAlreadyApplied(projected, outcome)
}

// recoveredHeadSupersedesCompletedTurn 仅认可同owner且会话与generation均严格递增的已接受head。
func recoveredHeadSupersedesCompletedTurn(agent *agentRuntime, head contract.DurableTerminalOutcomeHead, identity contract.CanonicalTerminalIdentity) bool {
	return head.Capability == identity.Capability && head.AgentID == identity.AgentID &&
		head.Version > identity.HeadVersion && head.Generation > identity.Generation &&
		supersededByNewLaunch(agent, identity)
}
