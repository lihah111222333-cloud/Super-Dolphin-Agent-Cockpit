package dashboard

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	"go.uber.org/fx"
)

// serviceParams 是 fx 注入 dashboard service 的依赖声明。
type serviceParams struct {
	fx.In

	Orchestration contract.OrchestrationService `optional:"true"`
	DAGRuntime    contract.DAGRuntime           `optional:"true"`
	AgentStatuses agentstatusstore.Store
	SystemLogs    systemlogstore.Store
	AuditLogs     auditlogstore.Store
	BusLogs       buslogstore.Store
	AILogs        ailogstore.Store
	DBQueries     dbquerystore.Store
	CommandCards  commandcardstore.Reader
	Prompts       promptstore.Reader
	SharedFiles   sharedfilestore.Reader
	Skills        contract.SkillLister
}

// dashboardHandlersParams 是 fx 注入 dashboard handler 的依赖声明。
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
