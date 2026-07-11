package dashboard

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"go.uber.org/fx"
)

// serviceParams 是 fx 注入 dashboard service 的依赖声明。
type serviceParams struct {
	fx.In

	Orchestration OrchestrationReader       `optional:"true"`
	Reports       OrchestrationReportReader `optional:"true"`
	DAGRuntime    contract.DAGRuntime       `optional:"true"`
	AgentStatuses AgentStatusReader
	SystemLogs    SystemLogReader
	AuditLogs     AuditLogReader
	BusLogs       BusLogReader
	AILogs        AILogReader
	DBQueries     DBQueryExecutor
	CommandCards  CommandCardReader
	Prompts       PromptTemplateReader
	SharedFiles   SharedFileReader
	Skills        contract.SkillLister
}

// dashboardHandlersParams 是 fx 注入 dashboard handler 的依赖声明。
type dashboardHandlersParams struct {
	fx.In

	Service  Service
	Insights InsightReader `optional:"true"`
}

// NewDashboardHandlersWithInsights 注册 dashboard RPC handler 并附加 insights 子路由。
// insights reader 是可选依赖，未配置时只注册 dashboard 核心 handler。
func NewDashboardHandlersWithInsights(p dashboardHandlersParams) platformrpc.HandlerMapResult {
	result := NewDashboardHandlers(p.Service)
	addDashboardInsightHandlers(result.Handlers, p.Insights)
	return result
}

// Module 只组装 dashboard service、handler 和可选 insights RPC；持久化适配由 App 拥有。
var Module = fx.Module("dashboard",
	fx.Provide(func(p serviceParams) Service {
		return newServiceWithDAGRuntime(
			p.Orchestration,
			p.Reports,
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
