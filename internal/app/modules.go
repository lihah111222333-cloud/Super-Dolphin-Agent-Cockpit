package app

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/cron"
	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/feedback"
	"github.com/anthropic-ai/super-agent-v3/internal/module/insight"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	"github.com/anthropic-ai/super-agent-v3/internal/module/notify"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	turnobservation "github.com/anthropic-ai/super-agent-v3/internal/module/turn/observation"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/cachekeepalive"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/hooks"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/internal/store"
)

// Module wires the core app surface. It intentionally exposes the ctl control plane
// for externally started MCP binaries, but does not launch or supervise MCP processes.
var Module = fx.Options(
	fx.Provide(NewLogger),
	fx.Provide(pidregistry.New),
	config.Module,
	db.Module,
	bus.Module,
	rpc.Module,
	hooks.Module,
	cachekeepalive.Module,
	mcpcontrol.Module,
	platformrunner.Module,
	statemachine.Module,
	store.Module,
	dashboard.Module,
	feedback.Module,
	memory.Module,
	prompt.Module,
	skill.Module,
	thread.Module,
	turn.Module,
	turnobservation.Module,
	// P21 模块：P1b cron 计划任务、P2 多平台通知、P3 session insights。
	// 三者均采用 fx optional 依赖，缺少上游依赖时自动降级为 noop，不阻塞整体装配图闭合。
	cron.Module,
	notify.Module,
	insight.Module,
	uistate.Module,
	unified.Module,
	claudecli.Module,
	codexapp.Module,
	toolbridge.Module, // P15 新增：始终加载
	// orchestration is handled entirely by the standalone mcp-orch MCP server;
	// the desktop app must NOT embed its own orchestration module, otherwise
	// localLauncher re-spawns the desktop binary as a subprocess which exits
	// immediately and causes agent state to go to "failed".
	fx.Provide(
		AsRPCRunner,
		newThreadOrchestrationFacade,
		newRuntimeReporter,
	),
)

func AsRPCRunner(server *rpc.Server) RunnerResult {
	return RunnerResult{Runner: server}
}
