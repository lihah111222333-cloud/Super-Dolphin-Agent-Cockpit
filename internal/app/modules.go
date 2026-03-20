package app

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/store"
)

var Module = fx.Options(
	fx.Provide(NewLogger),
	config.Module,
	db.Module,
	bus.Module,
	rpc.Module,
	platformrunner.Module,
	statemachine.Module,
	store.Module,
	skill.Module,
	thread.Module,
	turn.Module,
	orchestration.Module,
	workspace.Module,
	unified.Module,
	claudecli.Module,
	codexapp.Module,
	fx.Provide(
		AsRPCRunner,
		newThreadOrchestrationFacade,
	),
)

func AsRPCRunner(server *rpc.Server) RunnerResult {
	return RunnerResult{Runner: server}
}
