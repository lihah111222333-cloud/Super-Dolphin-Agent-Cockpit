// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/tools"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"
)

// bootstrapRunner 持有启动配置和控制面客户端，等待 stdio server 就绪后再连接 RPC。
type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     *bootstrap.Client
	stdioReady <-chan struct{} // closed when stdio server is ready
}

// runtimeParams 聚合 fx 注入的 runner 列表和 shutdowner，供 bindRuntime 使用。
type runtimeParams struct {
	fx.In

	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

// registryToolProvider 基于工具定义列表实现 common.ToolProvider，支持按需过滤语义 LSP 工具。
type registryToolProvider struct {
	defs                   []toolDefinition
	semanticToolsAvailable func(context.Context) bool
}

// run 组装并启动 mcp-lsp sidecar 自身的 fx 应用。
// 该进程只暴露 ctl 工具与 manifest 元数据，stdout 必须保留给 MCP stdio 协议通道。
func run() error {
	// MCP stdio 协议把 stdout 当作 JSON-RPC 通道；日志必须固定写 stderr。
	// 如果这里回到 stdout，客户端会把普通日志当作协议帧解析而失败。
	pkglogger.InitWithConsoleWriter(os.Stderr)

	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func(shutdowner fx.Shutdowner, handlers ToolHandlers, runtimeManager *Manager) bootstrap.Config {
				cfg := bootstrap.ReadBootConfig()
				cfg.AgentID = ""
				cfg.Capabilities = []string{"tools/lsp"}
				tp := registryToolProvider{defs: toolDefinitions(handlers)}
				cfg.OnToolsList = func(ctx context.Context) (any, error) {
					tools, err := tp.ListTools(ctx)
					if err != nil {
						return nil, err
					}
					return map[string]any{"tools": tools}, nil
				}
				cfg.OnToolsCall = func(ctx context.Context, params json.RawMessage) (any, error) {
					return handleScopedToolsCall(ctx, tp, mcp.ClientKindLSP, params)
				}
				cfg.FinalReport = func() *mcp.ReportRequest {
					return &mcp.ReportRequest{Report: mcp.ReportEnvelope{Type: mcp.ReportVariantCompletion, Completion: &mcp.CompletionReport{Status: "done", Report: "mcp-lsp shutdown"}}}
				}
				cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
					fields := []any{"binary_name", cfg.BinaryName, "instance_id", cfg.InstanceID, "scope", notify.Scope, "config_version", notify.ConfigVersion, "selector", notify.Selector}
					fields = append(fields, platformshared.SafePayloadLogFields("payload", notify.Payload)...)
					pkglogger.Info("mcp-lsp config changed", fields...)
				}
				cfg.OnLSPReleaseScope = func(ctx context.Context, req mcp.LSPReleaseScopeRequest) (mcp.LSPReleaseScopeResult, error) {
					if runtimeManager == nil {
						return mcp.LSPReleaseScopeResult{}, nil
					}
					return runtimeManager.ReleaseScope(req)
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					platformshared.LogIgnoredError(pkglogger.Get(), "mcp-lsp: OnShutdown", shutdowner.Shutdown())
				}
				return cfg
			},
			platformconfig.New,
			bootstrap.New,
			newManager,
			newToolHandlers,
			newServer,
			fx.Annotate(newBootstrapRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStdioRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newHTTPRunner, fx.ResultTags(`group:"runners"`)),
			// 每种语言 ManagerPool 的后台 recycler 由根运行组托管，构造函数只负责建模。
			// flatten 会把 runner 切片拆成独立成员，确保 fx 生命周期统一启动和停止。
			fx.Annotate(provideLSPBackgroundRunners, fx.ResultTags(`group:"runners,flatten"`)),
		),
		fx.Invoke(func() { common.RegisterToolResultPlainTextRenderer(tools.FormatToPlainText) }),
		fx.Invoke(bindRuntime),
	)
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

// newServer 创建 stdio 传输层的 MCP server，使用受保护的 stdout 作为写端。
func newServer(handlers ToolHandlers) (*common.Server, error) {
	stdout := mcpStdout.Load()
	if stdout == nil {
		return nil, errors.New("mcp-lsp: mcpStdout not initialized; program assembly order is broken")
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer(binaryName, binaryVersion, transport, registryToolProvider{
		defs: toolDefinitions(handlers),
	}), nil
}

// newBootstrapRunner 构建 bootstrapRunner，等待 stdio server ready 信号后连接控制面。
func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, server *common.Server) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client, stdioReady: server.Ready()}
}

// provideLSPBackgroundRunners 将语言 manager 的后台 runner 挂到根运行组。
// 后台 recycler 必须受 fx 生命周期管理，避免构造阶段隐式启动 goroutine。
func provideLSPBackgroundRunners(m *Manager) []platformrunner.Runner {
	return m.BackgroundRunners()
}

// ListTools 返回当前 peer 暴露的工具列表，语义 LSP server 不可用时过滤掉对应工具。
func (p registryToolProvider) ListTools(ctx context.Context) ([]mcp.MCPTool, error) {
	semanticAvailable, err := p.semanticLSPAvailable(ctx)
	if err != nil {
		return nil, err
	}
	toolsList := make([]mcp.MCPTool, 0, len(p.defs))
	for _, def := range p.defs {
		if isSemanticLSPToolName(def.Manifest.Name) && !semanticAvailable {
			continue
		}
		schema, err := marshalInputSchema(def.Manifest.Schema)
		if err != nil {
			return nil, err
		}
		tool := mcp.MCPTool{
			Name:        def.Manifest.Name,
			Description: def.Manifest.Description,
			InputSchema: schema,
		}
		if len(def.Manifest.OutputSchema) > 0 {
			outSchema, err := json.Marshal(def.Manifest.OutputSchema)
			if err != nil {
				return nil, err
			}
			tool.OutputSchema = outSchema
		}
		toolsList = append(toolsList, tool)
	}
	return toolsList, nil
}

// semanticLSPAvailable 检查当前环境是否有可用的语义 LSP server。
func (p registryToolProvider) semanticLSPAvailable(ctx context.Context) (bool, error) {
	if p.semanticToolsAvailable != nil {
		return p.semanticToolsAvailable(ctx), nil
	}
	return runtimeSemanticLSPToolsAvailable(ctx)
}

// isSemanticLSPToolName 判断工具名是否属于需要语义 LSP server 才能运行的工具。
func isSemanticLSPToolName(name string) bool {
	switch canonicalToolName(name) {
	case "inspect", "xref", "structure", "edit", "completion":
		return true
	default:
		return false
	}
}

// runtimeSemanticLSPToolsAvailable 检查打包环境或 PATH 中是否存在语义 LSP server 二进制。
func runtimeSemanticLSPToolsAvailable(context.Context) (bool, error) {
	lspBundle, packaged, err := runtimeenv.LoadLSPBundleFromEnv()
	if packaged {
		if err != nil {
			return false, err
		}
		return len(lspBundle.SemanticLanguages()) > 0, nil
	}
	binaries, err := runtimeSemanticLSPServerBinaries()
	if err != nil {
		return false, err
	}
	for _, binary := range binaries {
		if _, err := exec.LookPath(binary); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// runtimeSemanticLSPServerBinaries 返回支持的语义 LSP server 二进制名称列表。
func runtimeSemanticLSPServerBinaries() ([]string, error) {
	adapters := multilsp.NewDefaultLanguageAdapterRegistry()
	binaries := make([]string, 0, len(runtimePrimaryLanguageIDs()))
	seen := make(map[string]struct{}, len(runtimePrimaryLanguageIDs()))
	for _, languageID := range runtimePrimaryLanguageIDs() {
		adapter, ok := adapters.AdapterForLanguage(languageID)
		if !ok {
			return nil, errors.New("missing LSP language adapter: " + languageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			continue
		}
		command, err := adapter.ServerCommand(context.Background(), multilsp.ResolvedLanguageScope{})
		if err != nil {
			return nil, err
		}
		binary := strings.TrimSpace(command.Executable)
		if binary == "" {
			return nil, errors.New("missing semantic LSP server command for language: " + languageID)
		}
		if _, ok := seen[binary]; ok {
			continue
		}
		seen[binary] = struct{}{}
		binaries = append(binaries, binary)
	}
	return binaries, nil
}

// CallTool 调用当前 peer 暴露的工具，先补全工作区作用域后分发到具体处理器。
func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	var err error
	ctx, err = withRuntimeWorkspaceScopeFallback(ctx)
	if err != nil {
		return nil, err
	}
	return handleToolCall(ctx, p.defs, name, args)
}

// withRuntimeWorkspaceScopeFallback 将 sidecar 配置的运行时 roots 合并进工具作用域。
// 缺少 metadata roots 的调用仍会打 runtime fallback 标记，供 grep 等工具阻断 stale-root 搜索。
func withRuntimeWorkspaceScopeFallback(ctx context.Context) (context.Context, error) {
	scope, ok := common.ToolScopeFromContext(ctx)
	hadTrustedRoots := ok && len(scope.WorkspaceRoots) > 0
	runtimeRoots, configured, err := runtimeWorkspaceRootsFromEnv()
	if err != nil {
		return ctx, err
	}
	if len(runtimeRoots) == 0 {
		if configured {
			return ctx, errors.New("runtime workspace roots env is explicitly configured but empty")
		}
		if hadTrustedRoots {
			return ctx, nil
		}
		return ctx, errors.New("runtime workspace roots env is required")
	}
	if strings.TrimSpace(scope.CWD) == "" {
		scope.CWD = runtimeRoots[0]
	}
	scope.WorkspaceRoots = append(scope.WorkspaceRoots, runtimeRoots...)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = mcp.ClientKindLSP
	}
	ctx = common.WithToolScope(ctx, scope)
	if hadTrustedRoots {
		return ctx, nil
	}
	return common.WithRuntimeWorkspaceScopeFallback(ctx), nil
}

// shouldWarnLSPCWDTrace 判断该工具名是否需要记录工作区追踪日志。
func shouldWarnLSPCWDTrace(toolName string) bool {
	toolName = canonicalToolName(toolName)
	switch toolName {
	case "file", "inspect", "xref", "grep", "structure", "edit", "completion":
		return true
	default:
		return false
	}
}

// warnLSPToolsCallScopeTrace 记录工具调用的作用域追踪日志，仅对需要追踪的工具生效。
func warnLSPToolsCallScopeTrace(toolName string, scope common.ToolScope) {
	if !shouldWarnLSPCWDTrace(toolName) {
		return
	}
	fields := []any{
		"tool", strings.TrimSpace(toolName),
		"agent_id", scope.AgentID,
		"thread_id", scope.ThreadID,
		"call_id", scope.CallID,
		"has_cwd", scope.CWD != "",
	}
	fields = append(fields, platformshared.SafePathLogFields("cwd", scope.CWD)...)
	pkglogger.Warn("mcp-lsp: tools/call scope trace", fields...)
}

// handleScopedToolsCall 解码工具调用参数，设置作用域后分发到具体工具处理器，panic 时包装为错误返回。
func handleScopedToolsCall(ctx context.Context, tp registryToolProvider, family string, params json.RawMessage) (result any, err error) {
	toolName := "tools/call"
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = wrapScopedToolResult(common.NewToolErrorEnvelope(toolName, common.NewPanicToolError(recovered)))
		}
	}()
	req, err := common.DecodeToolCallParams(params)
	if err != nil {
		return nil, err
	}
	toolName = req.Name
	scope := req.Scope(family)
	warnLSPToolsCallScopeTrace(req.Name, scope)
	ctx = common.WithToolScope(ctx, scope)
	result, err = tp.CallTool(ctx, req.Name, req.Arguments)
	if err != nil {
		if result == nil {
			result = common.NewToolErrorEnvelope(req.Name, err)
		}
	}
	return wrapScopedToolResult(result)
}

// wrapScopedToolResult 将工具结果序列化为含 content/structuredContent/isError 的 MCP 响应格式。
func wrapScopedToolResult(result any) (any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	plainText, err := common.ResolveToolResultText(result, raw)
	if err != nil {
		return nil, err
	}
	structuredContent, err := common.StructuredContentFromRaw(raw)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": plainText}},
		"structuredContent": structuredContent,
		"isError":           common.ToolResultIsError(result),
	}, nil
}

// marshalInputSchema 将工具输入 schema 序列化为 JSON，空 schema 返回 "{}"。
func marshalInputSchema(schema map[string]any) (json.RawMessage, error) {
	if len(schema) == 0 {
		return json.RawMessage("{}"), nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// handleToolCall 按工具名在定义列表中查找处理器并执行，未找到时返回错误。
func handleToolCall(ctx context.Context, defs []toolDefinition, name string, args json.RawMessage) (any, error) {
	trimmed := canonicalToolName(name)
	for _, def := range defs {
		if canonicalToolName(def.Manifest.Name) != trimmed {
			continue
		}
		if def.Handler == nil {
			return nil, errors.New("tool handler is not configured")
		}
		return def.Handler(ctx, args)
	}
	return nil, errors.New("unknown tool: " + strings.TrimSpace(name))
}

// Run 启动 LSP bootstrap 流程，等待 stdio server 就绪后按模式决定是否连接控制面 RPC。
func (r bootstrapRunner) Run(ctx context.Context) error {
	r.client.InstallLogRelay()
	// 双通道启动顺序：先等本地 stdio MCP server 就绪，再连接控制面 jrpc2。
	// 这样控制面开始派发请求时，工具执行通道已经存在。
	if r.stdioReady != nil {
		select {
		case <-r.stdioReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if strings.TrimSpace(r.cfg.RPCAddr) == "" {
		pkglogger.Warn("mcp-lsp bootstrap disabled: GO_AGENT_CTL_RPC_ADDR missing",
			"binary_name", r.cfg.BinaryName,
			"client_kind", r.cfg.ClientKind,
			"thread_id", r.cfg.ThreadID,
			"capabilities", r.cfg.Capabilities,
		)
		<-ctx.Done()
		return nil
	}
	// 只有独立 peer 模式才注册控制面；provider 拉起的 sidecar 避免和宿主 sweeper 抢占生命周期。
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		pkglogger.Info("mcp-lsp bootstrap skipped (sidecar mode)",
			"rpc_addr", r.cfg.RPCAddr,
			"binary_name", r.cfg.BinaryName,
		)
		<-ctx.Done()
		return nil
	}
	pkglogger.Info("mcp-lsp bootstrap starting (peer mode)",
		"binary_name", r.cfg.BinaryName,
		"rpc_addr", r.cfg.RPCAddr,
		"capabilities", r.cfg.Capabilities,
	)
	if err := r.client.Start(ctx); err != nil {
		pkglogger.Error("mcp-lsp bootstrap start failed",
			"binary_name", r.cfg.BinaryName,
			"rpc_addr", r.cfg.RPCAddr,
			"error", err,
		)
		return err
	}
	<-ctx.Done()
	return r.client.Close()
}

// bindRuntime 将运行组生命周期绑定到 fx，OnStart 启动 goroutine 运行所有 runner，
// OnStop 取消 ctx 并等待运行组退出，超时时返回 ctx 错误。
func bindRuntime(lc fx.Lifecycle, params runtimeParams) {
	log := pkglogger.Get()
	var (
		cancel       context.CancelFunc
		shutdownOnce sync.Once
	)
	done := make(chan error, 1)
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			platformshared.LogIgnoredError(log, "mcp-lsp shutdown error", params.Shutdowner.Shutdown())
		})
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// sidecar 是独立子进程，不能继承主应用 RootCtxProvider。
			// 父进程关闭、控制面 OnShutdown 或 RunGroup 自退出最终都会进入 OnStop，统一 cancel 并等待 done。
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			runtimesafe.SafeGo(runCtx, log, "mcp-lsp.runtime.runGroup", func(context.Context) {
				err := platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{
					EnableSignals: false,
				})
				done <- err
				close(done)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Error("mcp-lsp runtime exited", "error", err)
				}
				requestShutdown()
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			select {
			case err := <-done:
				if errors.Is(err, context.Canceled) {
					return nil
				}
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
