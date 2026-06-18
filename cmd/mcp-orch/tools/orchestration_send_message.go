package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func sendMessageShouldWaitReport(in SendMessageInput) bool {
	return in.WaitReport != nil && *in.WaitReport
}

// submitMessageAndWaitForReport 只处理 idle agent 的 follow-up 等待路径。
// 先记录当前 report_seq，再提交 turn，最后等待更大的 seq，避免误读上一轮旧报告。
func submitMessageAndWaitForReport(ctx context.Context, svc contract.OrchestrationService, in SendMessageInput) (map[string]any, error) {
	agentID, message, err := sendMessageParts(in)
	if err != nil {
		return nil, err
	}
	previousReportSeq, err := previousFollowUpReportSeq(ctx, svc, agentID)
	if err != nil {
		return nil, err
	}
	waitInput := GetAgentReportInput{
		AgentID:        agentID,
		Wait:           in.WaitReport,
		TimeoutMS:      in.TimeoutMS,
		AfterReportSeq: &previousReportSeq,
	}
	if _, _, err := validateAgentReportWait(ctx, waitInput, agentID); err != nil {
		return nil, err
	}
	snapshot, err := svc.Snapshot(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("snapshot agent %q before follow-up: %w", agentID, err)
	}
	if err := requireIdleFollowUpAgent(agentID, snapshot.State); err != nil {
		return nil, err
	}
	submission := turnSubmissionFromMessage(agentID, threadIDFromSnapshot(snapshot, agentID), message)
	if err := submitSendMessageTurn(ctx, svc, submission, message); err != nil {
		return nil, err
	}
	result, err := waitForAgentReport(ctx, svc, waitInput, agentID)
	if err != nil {
		return nil, err
	}
	report, ok := result.(contract.AgentReportResult)
	if !ok {
		return nil, fmt.Errorf("wait report for agent %q returned %T, want AgentReportResult", agentID, result)
	}
	return successResult(map[string]any{
		"agent_id":            agentID,
		"submitted":           true,
		"previous_report_seq": previousReportSeq,
		"report":              report,
	}), nil
}

func previousFollowUpReportSeq(ctx context.Context, svc contract.OrchestrationService, agentID string) (int64, error) {
	report, err := svc.GetReport(ctx, agentID)
	if err != nil {
		return 0, fmt.Errorf("read current report before follow-up for agent %q: %w", agentID, err)
	}
	return report.ReportSeq, nil
}

func requireIdleFollowUpAgent(agentID, state string) error {
	state = strings.TrimSpace(state)
	if strings.EqualFold(state, "idle") {
		return nil
	}
	return fmt.Errorf("send_message wait_report=true requires idle agent follow-up; agent %q is in state %q", agentID, state)
}

func threadIDFromSnapshot(snapshot contract.AgentSnapshot, agentID string) string {
	if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
		return threadID
	}
	return agentID
}

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

func submissionThreadID(ctx context.Context, svc contract.OrchestrationService, agentID string) string {
	snapshot, err := svc.Snapshot(ctx, agentID)
	if err == nil && strings.TrimSpace(snapshot.ThreadID) != "" {
		return strings.TrimSpace(snapshot.ThreadID)
	}
	return agentID
}
