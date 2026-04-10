package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

type bootstrapRunner struct {
	cfg    bootstrap.Config
	client *bootstrap.Client
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
				// P15: register tools/list and tools/call so toolbridge can call this peer.
				toolProvider := registryToolProvider{defs: toolDefinitions(handlers)}
				cfg.OnToolsList = func(ctx context.Context) (any, error) {
					tools, err := toolProvider.ListTools(ctx)
					if err != nil { return nil, err }
					return map[string]any{"tools": tools}, nil
				}
				cfg.OnToolsCall = func(ctx context.Context, params json.RawMessage) (any, error) {
					var req struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					}
					if err := json.Unmarshal(params, &req); err != nil {
						return nil, err
					}
					result, err := toolProvider.CallTool(ctx, req.Name, req.Arguments)
					if err != nil { return nil, err }
					text, _ := json.Marshal(result)
					return map[string]any{
						"content": []map[string]string{{"type": "text", "text": string(text)}},
					}, nil
				}
				cfg.FinalReport = func() *mcp.ReportRequest {
					return &mcp.ReportRequest{
						Report: mcp.ReportEnvelope{
							Type: mcp.ReportVariantCompletion,
							Completion: &mcp.CompletionReport{
								Status: "done",
								Report: "mcp-lsp shutdown",
							},
						},
					}
				}
				cfg.OnConfigChanged = func(notify mcp.ConfigChangedNotify) {
					pkglogger.Info("mcp-lsp config changed",
						"binary_name", cfg.BinaryName,
						"instance_id", cfg.InstanceID,
						"scope", notify.Scope,
						"config_version", notify.ConfigVersion,
						"selector", notify.Selector,
						"payload", string(notify.Payload),
					)
				}
				cfg.OnShutdown = func(mcp.ShutdownRequest) {
					_ = shutdowner.Shutdown()
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

func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client}
}

func (p registryToolProvider) ListTools(context.Context) ([]common.MCPTool, error) {
	toolsList := make([]common.MCPTool, 0, len(p.defs))
	for _, def := range p.defs {
		schema, err := marshalInputSchema(def.Manifest.Schema)
		if err != nil {
			return nil, err
		}
		toolsList = append(toolsList, common.MCPTool{
			Name:        def.Manifest.Name,
			Description: def.Manifest.Description,
			InputSchema: schema,
		})
	}
	return toolsList, nil
}

func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return handleToolCall(ctx, p.defs, name, args)
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
	trimmed := strings.TrimSpace(name)
	for _, def := range defs {
		if def.Manifest.Name != trimmed {
			continue
		}
		if def.Handler == nil {
			return nil, errors.New("tool handler is not configured")
		}
		return def.Handler(ctx, args)
	}
	return nil, errors.New("unknown tool: " + trimmed)
}

func (r bootstrapRunner) Run(ctx context.Context) error {
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
	var cancel context.CancelFunc
	done := make(chan error, 1)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel
			go func() {
				err := platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{
					EnableSignals: true,
				})
				done <- err
				close(done)
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Error("mcp-lsp runtime exited", "error", err)
				}
				_ = params.Shutdowner.Shutdown()
			}()
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
