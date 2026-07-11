package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// stopAgentResult 在旧 stop/archive 返回上按需追加等待结果；wait 省略时必须保持原有 wire shape。
func stopAgentResult(ctx context.Context, svc contract.AgentStopWaitPort, agentID string, archived bool, in stopAgentInput) (map[string]any, error) {
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
func waitForStopAgentSettlement(ctx context.Context, svc contract.AgentStopWaitPort, agentID string, archived bool, timeoutMS int) (string, error) {
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

// stopAgentWaitTimeout 校验 wait timeout，并在未指定时使用报告等待的默认窗口。
func stopAgentWaitTimeout(timeoutMS int) (time.Duration, error) {
	if timeoutMS < 0 {
		return 0, fmt.Errorf("timeout_ms must be non-negative")
	}
	if timeoutMS > 0 {
		return time.Duration(timeoutMS) * time.Millisecond, nil
	}
	return defaultAgentReportWaitTimeout, nil
}

// stopAgentSettlementState 从 list_agents 同源快照读取目标 agent 状态。
// 返回 found=false 表示快照里已不可见，调用方按 stop/archive 语义收口。
func stopAgentSettlementState(ctx context.Context, svc contract.AgentStopWaitPort, agentID string) (string, bool, error) {
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

// stopAgentSnapshotMatches 兼容 runtime id、agent_id 和 launch_id 三种历史标识。
func stopAgentSnapshotMatches(agent contract.AgentSnapshot, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	return strings.TrimSpace(agent.ID) == agentID ||
		strings.TrimSpace(agent.AgentID) == agentID ||
		strings.TrimSpace(agent.LaunchID) == agentID
}

// stopAgentSettledState 判断 stop_agent 等待可以结束的终态。
func stopAgentSettledState(state string) bool {
	switch strings.TrimSpace(state) {
	case "stopped", "failed", "archived":
		return true
	default:
		return false
	}
}

// missingStopAgentState 把快照缺失转换成调用者期望的最终状态。
func missingStopAgentState(archived bool) string {
	if archived {
		return "archived"
	}
	return "stopped"
}

// stopAgentWaitTimeoutError 保留 agent id 与底层超时原因，方便调用端诊断。
func stopAgentWaitTimeoutError(agentID string, cause error) error {
	return fmt.Errorf("timed out waiting for stop_agent agent %q to settle: %w", agentID, cause)
}
