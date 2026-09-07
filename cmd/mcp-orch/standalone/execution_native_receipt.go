package standalone

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// nativeSubmissionAck 只保留当前未消费的 user 重放确认，不属于持久恢复域。
type nativeSubmissionAck struct {
	threadID, turnID string
	done             chan struct{}
	accepted         bool
}

// SubmitTurnWithReceipt 只在真实 provider 协议确认后返回，未知结果不重发。
func (e *Execution) SubmitTurnWithReceipt(ctx context.Context, sub contract.TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	input, err := codexInput(sub)
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	session, err := e.session(sub.AgentID)
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	session.mu.Lock()
	provider, threadID, seq := session.provider.Name, session.threadID, session.launchSeq
	session.mu.Unlock()
	if err := validateNativeIdentity(provider, threadID, sub.ThreadID, seq); err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	var turnID string
	if provider == "claude" {
		turnID, err = e.submitClaudeReceipt(ctx, session, sub)
	} else {
		turnID, err = e.submitCodex(ctx, session, input)
	}
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	if !e.sameNativeSession(session, sub.AgentID, provider, threadID, seq) {
		return contract.ProviderSubmissionReceipt{}, errors.New("native submission session generation changed")
	}
	return nativeReceipt(provider, sub.AgentID, threadID, turnID, seq)
}

// nativeReceipt 将协议身份逐字段映射为窄回执。
func nativeReceipt(provider, agentID, threadID, turnID string, seq uint64) (contract.ProviderSubmissionReceipt, error) {
	receipt := contract.ProviderSubmissionReceipt{Provider: provider, AgentID: agentID, ThreadID: threadID, TurnID: turnID, ReceiptID: turnID, LaunchSeq: seq}
	return receipt, receipt.Validate()
}

// acceptNativeClaudeAck 只认可根会话中匹配输入 UUID 的 user 回显。
func acceptNativeClaudeAck(session *cliSession, event claudewire.Event) {
	session.mu.Lock()
	defer session.mu.Unlock()
	ack := session.pendingNativeAck
	if ack == nil || ack.accepted || !event.IsReplay || event.Type != "user" || event.ParentToolUseID != nil ||
		event.SessionID != session.threadID || event.SessionID != ack.threadID || event.UUID != ack.turnID {
		return
	}
	ack.accepted = true
	close(ack.done)
}

// releaseNativeAck 只释放本次请求槽，终态与下一轮不能覆盖等待对象。
func releaseNativeAck(session *cliSession, ack *nativeSubmissionAck) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingNativeAck == ack {
		session.pendingNativeAck = nil
	}
}

// waitNativeAck 已记录确认优先于随后取消；进程退出不能冒充确认。
func waitNativeAck(ctx context.Context, session *cliSession, ack *nativeSubmissionAck, processDone <-chan struct{}) error {
	select {
	case <-ack.done:
		return nil
	case <-ctx.Done():
	case <-processDone:
	}
	session.mu.Lock()
	accepted := ack.accepted
	session.mu.Unlock()
	if accepted {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("Claude process exited without user acknowledgement")
}

// validateNativeIdentity 拒绝缺失执行身份。
func validateNativeIdentity(provider, threadID, requestedThread string, seq uint64) error {
	if requestedThread != threadID {
		return errors.New("native submission thread identity mismatch")
	}
	if provider != "claude" && provider != "codex" || threadID == "" || seq == 0 {
		return errors.New("native submission requires provider session identity")
	}
	return nil
}

// sameNativeSession 检查映射未被另一执行代际替换。
func (e *Execution) sameNativeSession(session *cliSession, agentID, provider, threadID string, seq uint64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	session.mu.Lock()
	defer session.mu.Unlock()
	return e.sessions[agentID] == session && session.threadID == threadID && session.launchSeq == seq && session.provider.Name == provider
}

// submitClaudeReceipt 保持确认槽直到本调用消费，避免终态抢先清空 turn 后丢失确认。
func (e *Execution) submitClaudeReceipt(ctx context.Context, session *cliSession, sub contract.TurnSubmission) (string, error) {
	session.mu.Lock()
	process := session.claude
	session.mu.Unlock()
	if process == nil {
		return "", errors.New("Claude process unavailable")
	}
	ack := &nativeSubmissionAck{done: make(chan struct{})}
	defer releaseNativeAck(session, ack)
	turnID, err := e.submitClaudeTracked(ctx, session, sub, ack)
	if err != nil {
		return "", err
	}
	if err := waitNativeAck(ctx, session, ack, process.Done()); err != nil {
		return "", err
	}
	return turnID, nil
}
