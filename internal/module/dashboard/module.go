package dashboard

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
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
	SystemLogs    systemlogstore.Store
	AILogs        ailogstore.Store
	DBQueries     dbquerystore.Store
	TaskTraces    tasktracestore.Store
	SharedFiles   sharedfilestore.Store
	CommandCards  commandcardstore.Store
	Prompts       promptstore.Store
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
			p.SharedFiles,
			p.CommandCards,
			p.Prompts,
			p.Skills,
		)
	}),
	fx.Provide(NewDashboardHandlers),
	fx.Provide(NewPromptHandlers),
)
