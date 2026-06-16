package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/tools"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"
)

type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     *bootstrap.Client
	stdioReady <-chan struct{} // closed when stdio server is ready
}

type runtimeParams struct {
	fx.In

	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

type registryToolProvider struct {
	defs                   []toolDefinition
	semanticToolsAvailable func(context.Context) bool
}

// run boots the MCP binary itself. The core process only exposes ctl/* endpoints
// and manifest metadata; external executors decide when and how this binary starts.
// run 运行LSP。
func run() error {
	// MCP stdio transport uses stdout for JSON-RPC messages.
	// Force all logging to stderr so it does not pollute the MCP channel.
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
					pkglogger.Info("mcp-lsp config changed", "binary_name", cfg.BinaryName, "instance_id", cfg.InstanceID, "scope", notify.Scope, "config_version", notify.ConfigVersion, "selector", notify.Selector, "payload", string(notify.Payload))
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
			// P22 P2 LSP-S1: per-language ManagerPool recyclers now
			// join the root runner group instead of being launched from
			// NewManagerPool's constructor. `flatten` unpacks the slice
			// so each recycler becomes its own Runner entry.
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

func newServer(handlers ToolHandlers) *common.Server {
	stdout := mcpStdout.Load()
	if stdout == nil {
		stdout = os.Stdout
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer(binaryName, binaryVersion, transport, registryToolProvider{
		defs: toolDefinitions(handlers),
	})
}

func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, server *common.Server) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client, stdioReady: server.Ready()}
}

// provideLSPBackgroundRunners lifts each language manager's background
// runner (today: its ManagerPool recycler) into the root
// `group:"runners"` aggregation. See cmd/mcp-lsp/runtime.go
// *Manager.BackgroundRunners and P22 P2 LSP-S1.
func provideLSPBackgroundRunners(m *Manager) []platformrunner.Runner {
	return m.BackgroundRunners()
}

// ListTools 返回当前 peer 暴露的工具列表。
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

func (p registryToolProvider) semanticLSPAvailable(ctx context.Context) (bool, error) {
	if p.semanticToolsAvailable != nil {
		return p.semanticToolsAvailable(ctx), nil
	}
	return runtimeSemanticLSPToolsAvailable(ctx)
}

func isSemanticLSPToolName(name string) bool {
	switch canonicalToolName(name) {
	case "inspect", "xref", "structure", "edit", "completion":
		return true
	default:
		return false
	}
}

func runtimeSemanticLSPToolsAvailable(context.Context) (bool, error) {
	lspBundle, packaged, err := runtimeenv.LoadLSPBundleFromEnv()
	if packaged {
		if err != nil {
			return false, err
		}
		return len(lspBundle.SemanticLanguages()) > 0, nil
	}
	for _, binary := range runtimeSemanticLSPServerBinaries() {
		if _, err := exec.LookPath(binary); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func runtimeSemanticLSPServerBinaries() []string {
	return []string{
		"gopls",
		"typescript-language-server",
		"pyright-langserver",
		"vscode-css-language-server",
		"rust-analyzer",
		"jdtls",
	}
}

// CallTool 调用当前 peer 暴露的工具。
func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	var err error
	ctx, err = withRuntimeWorkspaceScopeFallback(ctx)
	if err != nil {
		return nil, err
	}
	return handleToolCall(ctx, p.defs, name, args)
}

// withRuntimeWorkspaceScopeFallback 设置运行时工作区作用域兜底。
func withRuntimeWorkspaceScopeFallback(ctx context.Context) (context.Context, error) {
	scope, ok := common.ToolScopeFromContext(ctx)
	if ok && len(scope.WorkspaceRoots) > 0 {
		return ctx, nil
	}
	roots, err := runtimeWorkspaceRoots()
	if err != nil {
		return ctx, err
	}
	if len(roots) == 0 {
		return ctx, nil
	}
	scope.CWD = roots[0]
	scope.WorkspaceRoots = append([]string(nil), roots...)
	if strings.TrimSpace(scope.Family) == "" {
		scope.Family = mcp.ClientKindLSP
	}
	return common.WithRuntimeWorkspaceScopeFallback(common.WithToolScope(ctx, scope)), nil
}

func shouldWarnLSPCWDTrace(toolName string) bool {
	toolName = canonicalToolName(toolName)
	switch toolName {
	case "file", "inspect", "xref", "grep", "structure", "edit", "completion":
		return true
	default:
		return false
	}
}

func warnLSPToolsCallScopeTrace(toolName string, scope common.ToolScope) {
	if !shouldWarnLSPCWDTrace(toolName) {
		return
	}
	pkglogger.Warn("mcp-lsp: tools/call scope trace",
		"tool", strings.TrimSpace(toolName),
		"agent_id", scope.AgentID,
		"thread_id", scope.ThreadID,
		"call_id", scope.CallID,
		"cwd", scope.CWD,
		"has_cwd", scope.CWD != "",
	)
}

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

// Run 启动LSP后台流程。
func (r bootstrapRunner) Run(ctx context.Context) error {
	r.client.InstallLogRelay()
	// Dual-channel startup ordering: wait for the local stdio MCP server
	// to be ready before connecting to the control-plane jrpc2. This
	// guarantees the tool-execution surface is available when the
	// control plane starts dispatching requests.
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
	// Only register when running as independent peer (GO_AGENT_PEER_MODE=1).
	// Sidecar mode (spawned by codex/claude) skips registration to avoid sweeper conflicts.
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

// bindRuntime 绑定运行时。
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
			// Sidecar lifecycle: context.Background() is intentional here.
			// Unlike the main app (internal/app/runner.go) which derives runCtx
			// from an owner-supplied RootCtxProvider, sidecars run as independent
			// child processes. Their lifetime is governed by:
			//   1. Parent process kill / OnShutdown callback → fx.Shutdowner.Shutdown()
			//   2. RunGroup self-exit → requestShutdown()
			// Both paths converge on OnStop, which calls cancel() and waits for
			// the run group to drain via the done channel.
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
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
