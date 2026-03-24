package dashboard

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
	"go.uber.org/fx"
)

type serviceParams struct {
	fx.In

	Orchestration contract.OrchestrationService `optional:"true"`
	SystemLogs    systemlogstore.Store
	AILogs        ailogstore.Store
	DBQueries     dbquerystore.Store
	TaskTraces    tasktracestore.Store
	Queries       *sqlc.Queries
	Skills        skillmodule.Service
}

var Module = fx.Options(
	fx.Provide(func(p serviceParams) Service {
		return NewService(
			p.Orchestration,
			p.SystemLogs,
			p.AILogs,
			p.DBQueries,
			p.TaskTraces,
			p.Queries,
			p.Skills,
		)
	}),
	fx.Provide(NewDashboardHandlers),
)
