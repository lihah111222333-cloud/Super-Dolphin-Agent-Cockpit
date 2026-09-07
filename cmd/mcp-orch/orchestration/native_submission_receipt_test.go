package orchestration

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestNativeReceiptExecutionFence 拒绝旧代际、跨provider/thread/agent和无确认身份。
func TestNativeReceiptExecutionFence(t *testing.T) {
	agent := &agentRuntime{id: "agent", remoteThreadID: "thread", provider: "codex", launchSeq: 7}
	valid := contract.ProviderSubmissionReceipt{Provider: "codex", AgentID: "agent", ThreadID: "thread", TurnID: "turn", ReceiptID: "receipt", LaunchSeq: 7}
	if err := validateNativeReceiptFence(valid, agent); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*contract.ProviderSubmissionReceipt){
		"agent":      func(r *contract.ProviderSubmissionReceipt) { r.AgentID = "other" },
		"thread":     func(r *contract.ProviderSubmissionReceipt) { r.ThreadID = "old" },
		"generation": func(r *contract.ProviderSubmissionReceipt) { r.LaunchSeq = 6 },
		"provider":   func(r *contract.ProviderSubmissionReceipt) { r.Provider = "claude" },
		"turn":       func(r *contract.ProviderSubmissionReceipt) { r.TurnID = "" },
		"receipt":    func(r *contract.ProviderSubmissionReceipt) { r.ReceiptID = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			receipt := valid
			mutate(&receipt)
			if err := validateNativeReceiptFence(receipt, agent); err == nil {
				t.Fatal("invalid receipt fence accepted")
			}
		})
	}
	if err := validateNativeReceiptFence(valid, nil); err == nil {
		t.Fatal("missing agent accepted")
	}
}
