package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	defaultAgentReportWaitTimeout = platformconfig.RPCRequestTimeout
	agentReportPollInterval       = 50 * time.Millisecond
)

type GetAgentReportInput struct {
	AgentID     string `json:"agent_id"`
	Pos         string `json:"pos,omitempty"`
	Wait        *bool  `json:"wait,omitempty"`
	RequesterID string `json:"requester_id,omitempty"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"`
}

// HandleGetAgentReport 处理get代理report。
func HandleGetAgentReport(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in GetAgentReportInput) (any, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return nil, err
		}
		if !agentReportShouldWait(in) {
			return svc.GetReport(ctx, agentID)
		}
		return waitForAgentReport(ctx, svc, in, agentID)
	})
}

func agentReportShouldWait(in GetAgentReportInput) bool {
	return in.Wait != nil && *in.Wait
}

// waitForAgentReport 等待编排侧产出代理报告。
func waitForAgentReport(ctx context.Context, svc contract.OrchestrationService, in GetAgentReportInput, agentID string) (any, error) {
	timeout, requesterID, err := validateAgentReportWait(ctx, in, agentID)
	if err != nil {
		return nil, err
	}
	if requesterID != "" {
		if _, err := svc.RememberReportRequest(ctx, contract.RememberReportRequest{AgentID: agentID, RequesterID: requesterID}); err != nil {
			if !agentReportWaitableError(err) {
				return nil, err
			}
		}
	}
	return pollAgentReport(ctx, svc, agentID, timeout)
}

func validateAgentReportWait(ctx context.Context, in GetAgentReportInput, agentID string) (time.Duration, string, error) {
	if in.TimeoutMS < 0 {
		return 0, "", fmt.Errorf("timeout_ms must be non-negative")
	}
	timeout := defaultAgentReportWaitTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	requesterID := reportWaitRequester(ctx, in.RequesterID)
	if requesterID != "" && strings.EqualFold(agentID, requesterID) {
		return 0, "", fmt.Errorf("agent_id and requester_id must differ when waiting for a report")
	}
	return timeout, requesterID, nil
}

func reportWaitRequester(ctx context.Context, requesterID string) string {
	requesterID = strings.TrimSpace(requesterID)
	if requesterID != "" {
		return requesterID
	}
	if scope, ok := mcpcommon.ToolScopeFromContext(ctx); ok {
		return shared.FirstTrimmed(scope.AgentID, scope.ThreadID)
	}
	return ""
}

// pollAgentReport 处理poll代理report。
func pollAgentReport(ctx context.Context, svc contract.OrchestrationService, agentID string, timeout time.Duration) (contract.AgentReportResult, error) {
	waitCtx, cancel := platformconfig.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(agentReportPollInterval)
	defer ticker.Stop()
	for {
		result, err := svc.GetReport(waitCtx, agentID)
		if completed, ok := completedAgentReport(result); ok && (err == nil || agentReportWaitableError(err)) {
			return completed, nil
		}
		if err := agentReportPollError(ctx, waitCtx, err, timeout, agentID); err != nil {
			return contract.AgentReportResult{}, err
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return contract.AgentReportResult{}, ctx.Err()
			}
			return contract.AgentReportResult{}, agentReportWaitTimeoutError(ctx, timeout, agentID)
		case <-ticker.C:
		}
	}
}

func completedAgentReport(result contract.AgentReportResult) (contract.AgentReportResult, bool) {
	if strings.TrimSpace(result.Report) != "" {
		return result, true
	}
	if !terminalAgentReportState(result.State) {
		return contract.AgentReportResult{}, false
	}
	result.Report = agentReportNoReportFallback(result.State)
	return result, true
}

func terminalAgentReportState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "failed", "stopped":
		return true
	default:
		return false
	}
}

func agentReportNoReportFallback(state string) string {
	state = strings.TrimSpace(state)
	if state == "" {
		return "agent ended without producing a turn report"
	}
	return "agent ended in state '" + state + "' without producing a turn report"
}

func agentReportPollError(ctx, waitCtx context.Context, err error, timeout time.Duration, agentID string) error {
	if err == nil {
		return nil
	}
	if waitCtx.Err() != nil {
		return agentReportWaitTimeoutError(ctx, timeout, agentID)
	}
	if agentReportWaitableError(err) {
		return nil
	}
	return err
}

func agentReportWaitTimeoutError(ctx context.Context, timeout time.Duration, agentID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("timed out waiting %s for report from agent %q", timeout, agentID)
}

func agentReportWaitableError(err error) bool {
	return errors.Is(err, contract.ErrAgentNotFound)
}
