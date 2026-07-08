package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type agentRecoverPort interface {
	Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error)
	Recover(ctx context.Context, agentID string) error
}

// HandleRecoverAgent 恢复 stopped / failed 的子 agent，并返回恢复后的最新快照。
// 活跃状态不能重复 recover，避免把正在运行的 session 覆盖成旧状态。
func HandleRecoverAgent(svc agentRecoverPort) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in AgentIDInput) (contract.AgentSnapshot, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return contract.AgentSnapshot{}, err
		}
		snapshot, err := svc.Snapshot(ctx, agentID)
		if err != nil {
			return contract.AgentSnapshot{}, err
		}
		if err := requireRecoverableAgentState(agentID, snapshot.State); err != nil {
			return contract.AgentSnapshot{}, err
		}
		if err := svc.Recover(ctx, agentID); err != nil {
			return contract.AgentSnapshot{}, err
		}
		recovered, err := svc.Snapshot(ctx, agentID)
		if err != nil {
			return contract.AgentSnapshot{}, err
		}
		return recovered, nil
	})
}

func requireRecoverableAgentState(agentID, state string) error {
	trimmed := strings.TrimSpace(state)
	switch trimmed {
	case "stopped", "failed":
		return nil
	default:
		return fmt.Errorf("recover_agent requires stopped or failed agent; agent %q is in state %q", agentID, trimmed)
	}
}
