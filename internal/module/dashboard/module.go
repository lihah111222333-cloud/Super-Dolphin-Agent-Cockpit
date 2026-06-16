package dashboard

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"go.uber.org/fx"
)

type serviceParams struct {
	fx.In

	Orchestration contract.OrchestrationService `optional:"true"`
	DAGRuntime    contract.DAGRuntime           `optional:"true"`
	AgentStatuses contract.AgentStatusStore
	SystemLogs    contract.SystemLogStore
	AuditLogs     contract.AuditLogStore
	BusLogs       contract.BusLogStore
	AILogs        contract.AILogStore
	DBQueries     contract.DBQueryStore
	CommandCards  contract.CommandCardReader
	Prompts       contract.PromptReader
	SharedFiles   contract.SharedFileReader
	Skills        contract.SkillLister
}

type dashboardHandlersParams struct {
	fx.In

	Service  Service
	Insights InsightReader `optional:"true"`
}

// NewDashboardHandlersWithInsights 创建带insights的dashboard处理器。
func NewDashboardHandlersWithInsights(p dashboardHandlersParams) platformrpc.HandlerMapResult {
	result := NewDashboardHandlers(p.Service)
	addDashboardInsightHandlers(result.Handlers, p.Insights)
	return result
}

// Module wires dashboard services and RPC handlers.
var Module = fx.Module("dashboard",
	fx.Provide(func(p serviceParams) Service {
		return newServiceWithDAGRuntime(
			p.Orchestration,
			p.DAGRuntime,
			p.AgentStatuses,
			p.SystemLogs,
			p.AuditLogs,
			p.BusLogs,
			p.AILogs,
			p.DBQueries,
			p.CommandCards,
			p.Prompts,
			p.SharedFiles,
			p.Skills,
		)
	}),
	fx.Provide(NewDashboardHandlersWithInsights),
)
