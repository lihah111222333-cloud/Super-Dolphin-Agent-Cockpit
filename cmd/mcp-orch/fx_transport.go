package main

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

// orchestrationTransportOptions 装配 bootstrap、registry 与 stdio/HTTP runner。
func orchestrationTransportOptions() fx.Option {
	return fx.Module("orchestration-transport",
		fx.Provide(
			buildBootstrapConfig,
			bootstrap.New,
			newRegistry,
			newStdioServer,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newHTTPRunner, fx.ResultTags(`group:"runners"`)),
		),
	)
}

// buildBootstrapConfig 组装 mcp-orch 的启动配置，把工具注册和 scope 注入接到 bootstrap。
func buildBootstrapConfig(shutdowner fx.Shutdowner, hookAfter contract.BootstrapHookAfterHandler, registry tools.Registry) bootstrap.Config {
	cfg := bootstrap.ReadBootConfig()
	cfg.AgentID = ""
	p := registryToolProvider{registry: registry}
	cfg.OnToolsList = func(ctx context.Context) (any, error) {
		tools, err := p.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tools": tools}, nil
	}
	cfg.OnToolsCall = func(ctx context.Context, params json.RawMessage) (any, error) {
		return handleScopedToolsCall(ctx, p, mcp.ClientKindOrch, params)
	}
	cfg.Capabilities = []string{
		"tools/orchestration", "tools/task", "tools/workspace",
		"tools/prompt", "tools/command", "tools/shared_file", "tools/video",
	}
	cfg.Subscriptions = []string{"config/agent", "config/thread"}
	cfg.FinalReport = func() *mcp.ReportRequest {
		return &mcp.ReportRequest{Report: mcp.ReportEnvelope{
			Type:       mcp.ReportVariantCompletion,
			Completion: &mcp.CompletionReport{Status: "done", Report: "mcp-orch shutdown"},
		}}
	}
	cfg.OnConfigChanged = func(n mcp.ConfigChangedNotify) {
		pkglogger.Get().Info("mcp-orch config changed", "scope", n.Scope, "version", n.ConfigVersion)
	}
	cfg.OnShutdown = func(mcp.ShutdownRequest) {
		platformshared.LogIgnoredError(pkglogger.Get(), "mcp-orch: OnShutdown", shutdowner.Shutdown())
	}
	if hookAfter != nil {
		cfg.Hooks = bootstrap.HookConfig{OnAfter: bootstrap.HookAfterHandler(hookAfter)}
	}
	return cfg
}

func handleScopedToolsCall(ctx context.Context, p registryToolProvider, family string, params json.RawMessage) (any, error) {
	return handleScopedToolsCallWithCaller(ctx, family, params, p.CallTool)
}

// handleScopedToolsCallWithCaller 只信 bootstrap 注入的 scope，不信模型传来的同名字段。
func handleScopedToolsCallWithCaller(
	ctx context.Context,
	family string,
	params json.RawMessage,
	call func(context.Context, string, json.RawMessage) (any, error),
) (any, error) {
	req, err := common.DecodeToolCallParams(params)
	if err != nil {
		return nil, err
	}
	ctx = common.WithToolScope(ctx, req.Scope(family))
	result, err := call(ctx, req.Name, req.Arguments)
	if err != nil {
		result = newOrchToolErrorEnvelope(req.Name, err)
	}
	return wrapScopedToolResult(result)
}

// wrapScopedToolResult 把 tool 成功或错误结果收敛为 MCP content/structuredContent envelope。
func wrapScopedToolResult(result any) (any, error) {
	text, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": string(text)}},
		"structuredContent": json.RawMessage(text),
		"isError":           structuredToolResultIsError(text),
	}, nil
}

func structuredToolResultIsError(raw []byte) bool {
	var probe struct {
		Success *bool `json:"success"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Success != nil && !*probe.Success
}

func newOrchToolErrorEnvelope(toolName string, err error) common.ToolErrorEnvelope {
	return common.NewToolErrorEnvelopeWithClassifier(toolName, "", err, nil, tools.ToolErrorClassifier)
}
