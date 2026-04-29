package rpc

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/creachadair/jrpc2/handler"
)

type skillMCPToolMetricReportParams struct {
	Tool      string `json:"tool,omitempty"`
	Method    string `json:"method,omitempty"`
	Outcome   string `json:"outcome"`
	AgentID   string `json:"agentId,omitempty"`
	ThreadID  string `json:"threadId,omitempty"`
	CallID    string `json:"callId,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
}

type skillMCPToolMetricReportResult struct {
	OK      bool   `json:"ok"`
	Outcome string `json:"outcome"`
}

func NewSkillMCPMetricHandlers() HandlerMapResult {
	return HandlerMapResult{Handlers: handler.Map{
		skillmetrics.SkillMCPReportMethod: StrictHandler(reportSkillMCPToolMetric),
	}}
}

func reportSkillMCPToolMetric(_ context.Context, p skillMCPToolMetricReportParams) (skillMCPToolMetricReportResult, error) {
	outcome := skillmetrics.IncSkillMCPToolOutcome(strings.TrimSpace(p.Outcome))
	return skillMCPToolMetricReportResult{OK: true, Outcome: outcome}, nil
}
