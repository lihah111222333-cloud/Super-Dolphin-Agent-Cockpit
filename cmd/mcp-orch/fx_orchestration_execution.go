package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/fxadapter"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// orchestrationExecutionOptions 装配 agent launcher 与 DAG node executor。
func orchestrationExecutionOptions(remoteAddr string) fx.Option {
	options := []fx.Option{
		fx.Module("orchestration-execution",
			fx.Provide(
				func(lc fx.Lifecycle, turnStarter contract.OrchestrationTurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
					return buildLauncher(lc, turnStarter, logger, remoteAddr)
				},
				newAutomationCommandGetter,
				nodeexec.NewShellCommandRunner,
				func(r *nodeexec.ShellCommandRunner) nodeexec.AutomationCommandRunner { return r },
				orchestration.ProvideAgentLifecycleController,
				orchestration.NewServiceAgentLauncher,
				fxadapter.NewStoreNodeSpawnRecorder,
				orchestration.ProvideNodeLifecycleHooks,
				orchestration.ProvideAutomationExecutor,
				orchestration.NewStoreSharedFileReader,
				orchestration.NewStoreSharedFileWriter,
				orchestration.ProvideAgentExecutor,
				orchestration.NewNodeExecutorRouter,
			),
		),
	}
	if remoteAddr == "" {
		options = append(options, fx.Provide(fx.Annotate(orchestration.NewRunnerActor, fx.ResultTags(`group:"runners"`))))
	}
	return fx.Options(options...)
}

type automationCommandGetter struct {
	handler tools.ToolHandler
}

func newAutomationCommandGetter(store commandcardstore.Store) nodeexec.AutomationCommandGetter {
	return automationCommandGetter{handler: tools.HandleCommandGet(store)}
}

// GetCommandCard 返回桌面端展示 mcp-orch 命令所需的卡片信息。
func (g automationCommandGetter) GetCommandCard(ctx context.Context, cardKey string) (nodeexec.AutomationCommandCard, error) {
	if g.handler == nil {
		return nodeexec.AutomationCommandCard{}, errors.New("command_get client is not configured")
	}
	input, err := json.Marshal(map[string]string{"card_key": cardKey})
	if err != nil {
		return nodeexec.AutomationCommandCard{}, fmt.Errorf("marshal command_get input: %w", err)
	}
	result, err := g.handler(ctx, input)
	if err != nil {
		return nodeexec.AutomationCommandCard{}, err
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nodeexec.AutomationCommandCard{}, fmt.Errorf("marshal command_get result: %w", err)
	}
	var card nodeexec.AutomationCommandCard
	if err := json.Unmarshal(payload, &card); err != nil {
		return nodeexec.AutomationCommandCard{}, fmt.Errorf("parse command_get result: %w", err)
	}
	return card, nil
}

func buildLauncher(lc fx.Lifecycle, turnStarter contract.OrchestrationTurnStarter, logger *slog.Logger, remoteAddr string) orchestration.AgentLauncher {
	if remoteAddr == "" {
		return orchestration.NewLocalLauncher(turnStarter, logger)
	}
	launcher := orchestration.NewRemoteLauncher(remoteAddr)
	if closer, ok := launcher.(interface{ Close() error }); ok {
		lc.Append(fx.Hook{OnStop: func(context.Context) error { return closer.Close() }})
	}
	return launcher
}
