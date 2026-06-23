package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// stopAgentResult 在旧 stop/archive 返回上按需追加等待结果；wait 省略时必须保持原有 wire shape。
func stopAgentResult(ctx context.Context, svc contract.OrchestrationService, agentID string, archived bool, in stopAgentInput) (map[string]any, error) {
	result := map[string]any{"agent_id": agentID, "archived": archived}
	if in.Wait == nil || !*in.Wait {
		return successResult(result), nil
	}
	state, err := waitForStopAgentSettlement(ctx, svc, agentID, archived, in.TimeoutMS)
	if err != nil {
		return nil, err
	}
	result["stopped"] = true
	result["state"] = state
	return successResult(result), nil
}

// waitForStopAgentSettlement 轮询 list_agents 同源快照，直到目标进入终态或从列表消失。
func waitForStopAgentSettlement(ctx context.Context, svc contract.OrchestrationService, agentID string, archived bool, timeoutMS int) (string, error) {
	timeout, err := stopAgentWaitTimeout(timeoutMS)
	if err != nil {
		return "", err
	}
	waitCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(agentReportPollInterval)
	defer ticker.Stop()
	for {
		state, found, err := stopAgentSettlementState(waitCtx, svc, agentID)
		if err != nil {
			if waitCtx.Err() != nil {
				return "", stopAgentWaitTimeoutError(agentID, waitCtx.Err())
			}
			return "", err
		}
		if !found {
			return missingStopAgentState(archived), nil
		}
		if stopAgentSettledState(state) {
			return state, nil
		}
		select {
		case <-waitCtx.Done():
			return "", stopAgentWaitTimeoutError(agentID, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func stopAgentWaitTimeout(timeoutMS int) (time.Duration, error) {
	if timeoutMS < 0 {
		return 0, fmt.Errorf("timeout_ms must be non-negative")
	}
	if timeoutMS > 0 {
		return time.Duration(timeoutMS) * time.Millisecond, nil
	}
	return defaultAgentReportWaitTimeout, nil
}

func stopAgentSettlementState(ctx context.Context, svc contract.OrchestrationService, agentID string) (string, bool, error) {
	agents, err := listAgentSnapshots(ctx, svc)
	if err != nil {
		return "", false, err
	}
	for _, agent := range agents {
		if stopAgentSnapshotMatches(agent, agentID) {
			return strings.TrimSpace(agent.State), true, nil
		}
	}
	return "", false, nil
}

func stopAgentSnapshotMatches(agent contract.AgentSnapshot, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(agent.ID) == agentID ||
		strings.TrimSpace(agent.AgentID) == agentID ||
		strings.TrimSpace(agent.LaunchID) == agentID
}

func stopAgentSettledState(state string) bool {
	switch strings.TrimSpace(state) {
	case "stopped", "failed", "archived":
		return true
	default:
		return false
	}
}

func missingStopAgentState(archived bool) string {
	if archived {
		return "archived"
	}
	return "stopped"
}

func stopAgentWaitTimeoutError(agentID string, cause error) error {
	return fmt.Errorf("timed out waiting for stop_agent agent %q to settle: %w", agentID, cause)
}
