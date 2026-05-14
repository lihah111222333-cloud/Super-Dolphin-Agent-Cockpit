package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
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
	defs []toolDefinition
}

// run boots the MCP binary itself. The core process only exposes ctl/* endpoints
// and manifest metadata; external executors decide when and how this binary starts.
func run() error {
	// MCP stdio transport uses stdout for JSON-RPC messages.
	// Force all logging to stderr so it does not pollute the MCP channel.
	pkglogger.InitWithConsoleWriter(os.Stderr)

	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func(shutdowner fx.Shutdowner, handlers ToolHandlers) bootstrap.Config {
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
					var req struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
						MetaCWD   string          `json:"_cwd,omitempty"`
					}
					if err := json.Unmarshal(params, &req); err != nil {
						return nil, err
					}
					warnLSPToolsCallCWDTrace(req.Name, req.MetaCWD)
					if strings.TrimSpace(req.MetaCWD) != "" {
						ctx = context.WithValue(ctx, common.CwdContextKey, req.MetaCWD)
					}
					result, err := tp.CallTool(ctx, req.Name, req.Arguments)
					if err != nil {
						return nil, err
					}
					text, _ := json.Marshal(result)
					return map[string]any{
						"content":           []map[string]string{{"type": "text", "text": string(text)}},
						"structuredContent": json.RawMessage(text),
					}, nil
				}
				cfg.FinalReport = func() *mcp.ReportRequest {
					return &mcp.ReportRequest{Report: mcp.ReportEnvelope{Type: mcp.ReportVariantCompletion, Completion: &mcp.CompletionReport{Status: "done", Report: "mcp-lsp shutdown"}}}
				}
				cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
					pkglogger.Info("mcp-lsp config changed", "binary_name", cfg.BinaryName, "instance_id", cfg.InstanceID, "scope", notify.Scope, "config_version", notify.ConfigVersion, "selector", notify.Selector, "payload", string(notify.Payload))
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					platformshared.LogIgnoredError(pkglogger.Get(), "mcp-lsp: OnShutdown", shutdowner.Shutdown())
				}
				return cfg
			},
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
	stdout := mcpStdout
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

func (p registryToolProvider) ListTools(context.Context) ([]mcp.MCPTool, error) {
	toolsList := make([]mcp.MCPTool, 0, len(p.defs))
	for _, def := range p.defs {
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

func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return handleToolCall(ctx, p.defs, name, args)
}

func shouldWarnLSPCWDTrace(toolName string) bool {
	toolName = canonicalToolName(toolName)
	switch toolName {
	case "file", "inspect", "xref", "grep", "structure", "edit", "completion", "code_run", "code_run_test":
		return true
	default:
		return false
	}
}

func warnLSPToolsCallCWDTrace(toolName, metaCWD string) {
	if !shouldWarnLSPCWDTrace(toolName) {
		return
	}
	pkglogger.Warn("mcp-lsp: tools/call cwd trace",
		"tool", strings.TrimSpace(toolName),
		"meta_cwd", strings.TrimSpace(metaCWD),
		"has_meta_cwd", strings.TrimSpace(metaCWD) != "",
	)
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
		if def.Manifest.Name != trimmed {
			continue
		}
		if def.Handler == nil {
			return nil, errors.New("tool handler is not configured")
		}
		return def.Handler(ctx, args)
	}
	return nil, errors.New("unknown tool: " + strings.TrimSpace(name))
}

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
