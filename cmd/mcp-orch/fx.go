package main

import (
	"context"
	"os"
	"strings"

	"github.com/kelindar/event"
	orchnotify "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/notify"
	commandcardstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	taskdagstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	storeworkspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/workspace"
	workspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/workspace"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"go.uber.org/fx"
)

// run 启动 mcp-orch 进程，完成进程级依赖装配、运行和退出清理。
// sidecar 内的生命周期、DAG、执行和 transport 依赖分别由命名 option group 提供。
func run() error {
	remoteAddr := strings.TrimSpace(os.Getenv("GO_AGENT_CTL_RPC_ADDR"))
	app := newMCPOrchApp(remoteAddr)
	if err := app.Err(); err != nil {
		return err
	}
	startCtx, startCancel := platformconfig.WithRPCRequestTimeout(context.Background())
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}
	<-app.Wait()
	stopCtx, stopCancel := platformconfig.WithRPCRequestTimeout(context.Background())
	defer stopCancel()
	return app.Stop(stopCtx)
}

// newMCPOrchApp 组装完整 mcp-orch 依赖图，但不启动任何生命周期 hook。
func newMCPOrchApp(remoteAddr string) *fx.App {
	return fx.New(
		fx.NopLogger,
		platformconfig.Module,
		platformdb.Module,
		platformbus.Module,
		orchnotify.Module,
		promptstore.Module,
		commandcardstore.Module,
		sharedfilestore.Module,
		taskdagstore.Module,
		storeworkspace.Module,
		fx.Options(buildOrchestrationOptions(remoteAddr)...),
		orchestrationTransportOptions(),
		fx.Provide(
			newLogger,
			newQueries,
			newAgentThreadStore,
			newAgentBindingStore,
			newTerminalOutcomeStore,
			func(store storeworkspace.Store, dispatcher *event.Dispatcher) workspace.Service {
				return workspace.NewService(store, dispatcher)
			},
			newNoopSessionCleaner,
			newNoopTurnStarter,
			newModelRegistry,
			newBuiltinPromptRegistry,
		),
		fx.Invoke(bindRuntime),
	)
}

// buildOrchestrationOptions 只组合 lifecycle、DAG 与执行期 option groups。
func buildOrchestrationOptions(remoteAddr string) []fx.Option {
	return []fx.Option{
		orchestrationLifecycleOptions(),
		orchestrationDAGOptions(),
		orchestrationExecutionOptions(remoteAddr),
	}
}
