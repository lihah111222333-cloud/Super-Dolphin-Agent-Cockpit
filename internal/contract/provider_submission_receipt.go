package contract

import (
	"errors"
	"strings"
)

// ProviderSubmissionReceipt 表示真实 CLI 协议确认，不能替代模型完成或 OUT 恢复域回执。
// Codex 的证据为 turn/start 响应；Claude 的证据为匹配 session/UUID 的 user 重放确认。
type ProviderSubmissionReceipt struct {
	Provider  string `json:"provider"`
	AgentID   string `json:"agent_id"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	ReceiptID string `json:"receipt_id"`
	LaunchSeq uint64 `json:"launch_seq"`
}

// Validate 拒绝不完整、未知 provider 或缺少执行代际的确认。
func (r ProviderSubmissionReceipt) Validate() error {
	if r.Provider != "codex" && r.Provider != "claude" {
		return errors.New("provider submission receipt has unsupported provider")
	}
	for _, value := range []string{r.AgentID, r.ThreadID, r.TurnID, r.ReceiptID} {
		if value == "" || strings.TrimSpace(value) != value {
			return errors.New("provider submission receipt identity is required")
		}
	}
	if r.LaunchSeq == 0 {
		return errors.New("provider submission receipt launch generation is required")
	}
	return nil
}
