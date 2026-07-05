package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// InterruptAgentInput 是 interrupt_agent 工具入参，兼容 agent_id 与 pos 定位。
type InterruptAgentInput struct {
	AgentID   string `json:"agent_id"`
	Pos       string `json:"pos,omitempty"`
	Source    string `json:"source,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// HandleInterruptAgent 中断远程 Codex 子 agent 当前 turn，并返回收口后的状态。
func HandleInterruptAgent(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in InterruptAgentInput) (map[string]any, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return nil, err
		}
		timeout, err := interruptAgentTimeout(in.TimeoutMS)
		if err != nil {
			return nil, err
		}
		interruptCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
		defer cancel()
		result, err := svc.InterruptAgent(interruptCtx, agentID, interruptAgentSource(in.Source))
		if err != nil {
			return nil, err
		}
		return successResult(map[string]any{
			"agent_id":       result.AgentID,
			"interrupted":    true,
			"state":          result.State,
			"active_turn_id": "",
		}), nil
	})
}

func interruptAgentTimeout(timeoutMS int) (time.Duration, error) {
	if timeoutMS < 0 {
		return 0, fmt.Errorf("timeout_ms must be non-negative")
	}
	if timeoutMS > 0 {
		return time.Duration(timeoutMS) * time.Millisecond, nil
	}
	return platformconfig.RPCRequestTimeout, nil
}

func interruptAgentSource(source string) string {
	if source = strings.TrimSpace(source); source != "" {
		return source
	}
	return "parent_agent"
}
