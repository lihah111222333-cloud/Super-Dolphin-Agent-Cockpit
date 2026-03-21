package orchestration

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

type runtimeReporter struct {
	svc Service
}

func NewRuntimeReporter(svc Service) contract.RuntimeReporter {
	return runtimeReporter{svc: svc}
}

func (r runtimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	return r.svc.UpdateRuntime(ctx, agentdto.RuntimeReport{
		AgentID:  report.AgentID,
		Port:     report.Port,
		Provider: report.Provider,
	})
}
