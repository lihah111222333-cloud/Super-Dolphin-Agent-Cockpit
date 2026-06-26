package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// sendMessageShouldWaitReport 检查 send_message 是否需要等待后续报告。
func sendMessageShouldWaitReport(in SendMessageInput) bool {
	return in.WaitReport != nil && *in.WaitReport
}

// submitMessageAndWaitForReport 只处理 idle agent 的后续消息等待路径。
// 先记录当前 report_seq，再提交 turn，最后等待更大的 seq，避免误读上一轮旧报告。
func submitMessageAndWaitForReport(ctx context.Context, svc contract.OrchestrationService, in SendMessageInput) (map[string]any, error) {
	agentID, message, err := sendMessageParts(in)
	if err != nil {
		return nil, err
	}
	waitInput := GetAgentReportInput{
		AgentID:   agentID,
		Wait:      in.WaitReport,
		TimeoutMS: in.TimeoutMS,
	}
	timeout, _, err := validateAgentReportWait(ctx, waitInput, agentID)
	if err != nil {
		return nil, err
	}
	followUpCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	defer cancel()
	previousReportSeq, err := previousFollowUpReportSeq(followUpCtx, svc, agentID)
	if err != nil {
		return nil, err
	}
	waitInput.AfterReportSeq = &previousReportSeq
	snapshot, err := svc.Snapshot(followUpCtx, agentID)
	if err != nil {
		return nil, fmt.Errorf("snapshot agent %q before follow-up: %w", agentID, err)
	}
	if err := requireIdleFollowUpAgent(agentID, snapshot.State); err != nil {
		return nil, err
	}
	submission := turnSubmissionFromMessage(agentID, threadIDFromSnapshot(snapshot, agentID), message)
	if err := submitSendMessageTurn(followUpCtx, svc, submission, message); err != nil {
		return nil, sendMessageSubmitError(agentID, timeout, err)
	}
	report, err := waitForFollowUpReport(ctx, followUpCtx, svc, waitInput, agentID, timeout, previousReportSeq)
	if err != nil {
		return nil, err
	}
	return successResult(map[string]any{
		"agent_id":            agentID,
		"submitted":           true,
		"previous_report_seq": previousReportSeq,
		"report":              report,
	}), nil
}

// waitForFollowUpReport 保持原有 report 等待错误格式，同时复用同一个后续消息 deadline。
func waitForFollowUpReport(ctx, followUpCtx context.Context, svc contract.OrchestrationService, waitInput GetAgentReportInput, agentID string, timeout time.Duration, previousReportSeq int64) (contract.AgentReportResult, error) {
	result, err := waitForAgentReport(followUpCtx, svc, waitInput, agentID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && followUpCtx.Err() != nil && ctx.Err() == nil {
			return contract.AgentReportResult{}, agentReportWaitTimeoutError(ctx, timeout, agentID, &previousReportSeq)
		}
		return contract.AgentReportResult{}, err
	}
	report, ok := result.(contract.AgentReportResult)
	if !ok {
		return contract.AgentReportResult{}, fmt.Errorf("wait report for agent %q returned %T, want AgentReportResult", agentID, result)
	}
	return report, nil
}

// sendMessageSubmitError 把后续 turn 提交阶段的 deadline 转成可诊断的工具错误。
func sendMessageSubmitError(agentID string, timeout time.Duration, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("submit follow-up turn for agent %q timed out after %s: %w", agentID, timeout, err)
	}
	return err
}

// previousFollowUpReportSeq 读取提交前的 report_seq，后续等待必须看到更大的序号。
func previousFollowUpReportSeq(ctx context.Context, svc contract.OrchestrationService, agentID string) (int64, error) {
	report, err := svc.GetReport(ctx, agentID)
	if err != nil {
		return 0, fmt.Errorf("read current report before follow-up for agent %q: %w", agentID, err)
	}
	return report.ReportSeq, nil
}

// requireIdleFollowUpAgent 限制 wait_report=true 只能投递给 idle agent。
// 非 idle 状态下继续追加消息会和正在运行的 turn 竞争，必须 fail-fast。
func requireIdleFollowUpAgent(agentID, state string) error {
	state = strings.TrimSpace(state)
	if strings.EqualFold(state, "idle") {
		return nil
	}
	return fmt.Errorf("send_message wait_report=true requires idle agent follow-up; agent %q is in state %q", agentID, state)
}

// threadIDFromSnapshot 优先使用快照里的 thread_id，缺失时退回 agentID 保持旧接口兼容。
func threadIDFromSnapshot(snapshot contract.AgentSnapshot, agentID string) string {
	if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
		return threadID
	}
	return agentID
}

// submitSendMessageTurn 向 orchestration service 提交后续 turn，并记录最小诊断日志。
func submitSendMessageTurn(ctx context.Context, svc contract.OrchestrationService, submission contract.TurnSubmission, message string) error {
	pkglogger.Warn("orchestration_send_message: submit begin",
		"agent_id", submission.AgentID,
		"thread_id", submission.ThreadID,
		"input_items", len(submission.Inputs),
		"message_len", len([]rune(strings.TrimSpace(message))))
	if err := svc.SubmitTurn(ctx, submission); err != nil {
		pkglogger.Warn("orchestration_send_message: submit failed",
			"agent_id", submission.AgentID,
			"thread_id", submission.ThreadID,
			"error", err)
		return err
	}
	pkglogger.Warn("orchestration_send_message: submit accepted",
		"agent_id", submission.AgentID,
		"thread_id", submission.ThreadID)
	return nil
}

// submissionFromMessage 构造普通 send_message 的 turn 提交体。
// 它会解析 pos/agent_id 并按当前快照选择 thread_id。
func submissionFromMessage(
	ctx context.Context,
	svc contract.OrchestrationService,
	in SendMessageInput,
) (contract.TurnSubmission, error) {
	agentID, message, err := sendMessageParts(in)
	if err != nil {
		return contract.TurnSubmission{}, err
	}
	return turnSubmissionFromMessage(agentID, submissionThreadID(ctx, svc, agentID), message), nil
}

// sendMessageParts 解析 send_message 的目标 agent 和正文，两者都不能为空。
func sendMessageParts(in SendMessageInput) (string, string, error) {
	agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
	if err != nil {
		return "", "", err
	}
	message, err := requireTrimmed(in.Message, "message")
	if err != nil {
		return "", "", err
	}
	return agentID, message, nil
}

// turnSubmissionFromMessage 把纯文本消息封装成服务层 TurnSubmission。
func turnSubmissionFromMessage(agentID, threadID, message string) contract.TurnSubmission {
	return contract.TurnSubmission{
		AgentID:  agentID,
		ThreadID: threadID,
		Inputs: []shareddto.InputItem{{
			Type:    "text",
			Content: message,
		}},
	}
}

// submissionThreadID 从快照读取 thread_id；读取失败或为空时使用 agentID 保持可提交。
func submissionThreadID(ctx context.Context, svc contract.OrchestrationService, agentID string) string {
	snapshot, err := svc.Snapshot(ctx, agentID)
	if err == nil && strings.TrimSpace(snapshot.ThreadID) != "" {
		return strings.TrimSpace(snapshot.ThreadID)
	}
	return agentID
}
