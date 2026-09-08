package tools

import (
	"encoding/json"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// sendMessageRecoveryMetadata 保存本次提交阶段及仅用于观察报告的恢复查询。
type sendMessageRecoveryMetadata struct {
	Phase             string                   `json:"phase"`
	SubmissionStatus  string                   `json:"submission_status"`
	AgentID           string                   `json:"agent_id"`
	ThreadID          string                   `json:"thread_id"`
	PreviousReportSeq *int64                   `json:"previous_report_seq,omitempty"`
	Recovery          sendMessageRecoveryQuery `json:"recovery"`
}

// sendMessageRecoveryQuery 仅允许恢复读取报告，不携带再次提交输入的参数。
type sendMessageRecoveryQuery struct {
	Tool      string                       `json:"tool"`
	Arguments sendMessageRecoveryArguments `json:"arguments"`
}

// sendMessageRecoveryArguments 明确区分无基线快照与真实游标等待。
type sendMessageRecoveryArguments struct {
	AgentID        string `json:"agent_id"`
	Wait           bool   `json:"wait"`
	AfterReportSeq *int64 `json:"after_report_seq,omitempty"`
}

// sendMessageRecoveryError 经现有错误 envelope 透传结构化恢复信息，未知结果禁止自动重发。
func sendMessageRecoveryError(code string, cause error, submission contract.TurnSubmission, afterSeq *int64) error {
	if code != "submission_outcome_unknown" && code != "report_wait_incomplete" {
		return fmt.Errorf("unsupported send_message recovery code %q: %w", code, cause)
	}
	if code == "report_wait_incomplete" && afterSeq == nil {
		return fmt.Errorf("report wait recovery requires the observed report sequence: %w", cause)
	}
	metadata := sendMessageRecoveryMetadata{
		Phase: "submit", SubmissionStatus: "unknown", AgentID: submission.AgentID, ThreadID: submission.ThreadID,
		Recovery: sendMessageRecoveryQuery{Tool: "get_agent_report", Arguments: sendMessageRecoveryArguments{AgentID: submission.AgentID}},
	}
	hint := "Do not automatically resend send_message."
	if afterSeq != nil {
		seq := *afterSeq
		metadata.PreviousReportSeq = &seq
		metadata.Recovery.Arguments.Wait = true
		metadata.Recovery.Arguments.AfterReportSeq = &seq
	}
	if code == "report_wait_incomplete" {
		metadata.Phase = "wait_report"
		metadata.SubmissionStatus = "submitted"
		hint = "Do not resend send_message: the turn was submitted."
	}
	query, err := json.Marshal(metadata.Recovery)
	if err != nil {
		return fmt.Errorf("encode send_message recovery query: %w", err)
	}
	hint += " Recovery query: " + string(query) + ". A report is not native acknowledgement of this specific request."
	return &mcpcommon.CodedToolError{
		Err: cause, Code: code, Retryable: false, Hint: hint,
		Meta: map[string]any{"submission_recovery": metadata},
	}
}
