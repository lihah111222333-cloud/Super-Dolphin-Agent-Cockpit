package dashboard

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
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
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
	"go.uber.org/fx"
)

type serviceParams struct {
	fx.In

	Orchestration contract.OrchestrationService `optional:"true"`
	AgentStatuses agentstatusstore.Store
	SystemLogs    systemlogstore.Store
	AuditLogs     auditlogstore.Store
	BusLogs       buslogstore.Store
	AILogs        ailogstore.Store
	DBQueries     dbquerystore.Store
	TaskTraces    tasktracestore.Store
	CommandCards  commandcardstore.Reader
	Prompts       promptstore.Reader
	SharedFiles   sharedfilestore.Reader
	Skills        skillmodule.SkillLister
}

type dashboardHandlersParams struct {
	fx.In

	Service  Service
	Insights InsightReader `optional:"true"`
}

func NewDashboardHandlersWithInsights(p dashboardHandlersParams) platformrpc.HandlerMapResult {
	result := NewDashboardHandlers(p.Service)
	addDashboardInsightHandlers(result.Handlers, p.Insights)
	return result
}

var Module = fx.Module("dashboard",
	fx.Provide(func(p serviceParams) Service {
		return NewService(
			p.Orchestration,
			p.AgentStatuses,
			p.SystemLogs,
			p.AuditLogs,
			p.BusLogs,
			p.AILogs,
			p.DBQueries,
			p.TaskTraces,
			p.CommandCards,
			p.Prompts,
			p.SharedFiles,
			p.Skills,
		)
	}),
	fx.Provide(NewDashboardHandlersWithInsights),
)
