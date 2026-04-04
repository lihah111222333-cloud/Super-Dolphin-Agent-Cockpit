package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

type registryToolProvider struct {
	registry tools.Registry
}

type bootstrapRunner struct {
	cfg    bootstrap.Config
	client *bootstrap.Client
}

type runtimeParams struct {
	fx.In

	Logger     *slog.Logger
	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

func newLogger(cfg *platformconfig.Config) *slog.Logger {
	// MCP stdio transport uses stdout for JSON-RPC messages.
	// Force all logging to stderr so it does not pollute the MCP channel.
	pkglogger.InitWithConsoleWriter(os.Stderr)
	return pkglogger.Get()
}

func newPool(cfg *platformconfig.Config) (*pgxpool.Pool, error) {
	return platformdb.NewPool(cfg)
}

func newQueries(pool *pgxpool.Pool) *sqlc.Queries {
	return sqlc.New(pool)
}

func registerPoolLifecycle(lc fx.Lifecycle, logger *slog.Logger, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			pool.Close()
			if logger != nil {
				logger.Info("mcp-orch db pool closed")
			}
			return nil
		},
	})
}

// noopSessionCleaner satisfies contract.OrchestrationSessionCleaner in standalone mode.
type noopSessionCleaner struct{}

func (noopSessionCleaner) RemoveSession(string) {}

func newNoopSessionCleaner() contract.OrchestrationSessionCleaner {
	return noopSessionCleaner{}
}

// noopTurnStarter satisfies contract.OrchestrationTurnStarter in standalone mode.
// Turn submission via orchestration will return an error because no upstream turn
// service is wired.
type noopTurnStarter struct{}

func (noopTurnStarter) StartTurn(context.Context, contract.TurnSubmission) (string, error) {
	return "", errors.New("turn starter not available in mcp-orch standalone mode")
}

func newNoopTurnStarter() contract.OrchestrationTurnStarter {
	return noopTurnStarter{}
}

func newRegistry(
	orchestration contract.OrchestrationService,
	ws workspace.Service,
	prompt promptstore.Store,
	command commandcardstore.Store,
	sharedFile sharedfilestore.Store,
) tools.Registry {
	return tools.NewRegistry(tools.Dependencies{
		Orchestration: orchestration,
		Workspace:     ws,
		Prompt:        prompt,
		CommandCard:   command,
		SharedFile:    sharedFile,
	})
}

func newStdioRunner(registry tools.Registry) platformrunner.Runner {
	stdout := mcpStdout
	if stdout == nil {
		stdout = os.Stdout
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer("mcp-orch", "dev", transport, registryToolProvider{registry: registry})
}

func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client}
}

func (p registryToolProvider) ListTools(context.Context) ([]common.MCPTool, error) {
	defs := p.registry.List()
	toolsList := make([]common.MCPTool, 0, len(defs))
	for _, def := range defs {
		schema, err := marshalInputSchema(def.InputSchema)
		if err != nil {
			return nil, err
		}
		toolsList = append(toolsList, common.MCPTool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: schema,
		})
	}
	return toolsList, nil
}

func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	return handleToolCall(ctx, p.registry, name, args)
}

func marshalInputSchema(schema tools.Schema) (json.RawMessage, error) {
	if len(schema) == 0 {
		return json.RawMessage("{}"), nil
	}
	raw, err := json.Marshal(map[string]any(schema))
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func handleToolCall(ctx context.Context, registry tools.Registry, name string, args json.RawMessage) (any, error) {
	def, ok := registry.Lookup(strings.TrimSpace(name))
	if !ok {
		return nil, errors.New("unknown tool: " + strings.TrimSpace(name))
	}
	if def.Handler == nil {
		return nil, errors.New("tool handler is not configured")
	}
	return def.Handler(ctx, args)
}

func (r bootstrapRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.cfg.RPCAddr) == "" {
		pkglogger.Warn("mcp-orch bootstrap disabled: GO_AGENT_CTL_RPC_ADDR missing",
			"binary_name", r.cfg.BinaryName,
			"client_kind", r.cfg.ClientKind,
			"thread_id", r.cfg.ThreadID,
			"capabilities", r.cfg.Capabilities,
			"subscriptions", r.cfg.Subscriptions,
		)
		<-ctx.Done()
		return nil
	}
	pkglogger.Info("mcp-orch bootstrap starting",
		"binary_name", r.cfg.BinaryName,
		"rpc_addr", r.cfg.RPCAddr,
		"thread_id", r.cfg.ThreadID,
		"capabilities", r.cfg.Capabilities,
		"subscriptions", r.cfg.Subscriptions,
	)
	if err := r.client.Start(ctx); err != nil {
		pkglogger.Warn("mcp-orch bootstrap start failed, continuing without control plane",
			"binary_name", r.cfg.BinaryName,
			"rpc_addr", r.cfg.RPCAddr,
			"error", err,
		)
		// Non-fatal: MCP tools still work without the control plane.
		// Returning an error would kill RunGroup and shut down the
		// MCP stdio server, making all orchestration tools unavailable.
		<-ctx.Done()
		return nil
	}
	if err := subscribeOrchestrationHooks(ctx, r.client); err != nil {
		pkglogger.Warn("mcp-orch hook subscription failed",
			"binary_name", r.cfg.BinaryName,
			"rpc_addr", r.cfg.RPCAddr,
			"topics", orchestrationHookTopics,
			"error", err,
		)
	} else {
		pkglogger.Info("mcp-orch hook subscription ready",
			"binary_name", r.cfg.BinaryName,
			"rpc_addr", r.cfg.RPCAddr,
			"topics", orchestrationHookTopics,
		)
	}
	<-ctx.Done()
	return r.client.Close()
}

func bindRuntime(lc fx.Lifecycle, params runtimeParams) {
	logger := params.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
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
					logger.Error("mcp-orch runtime exited", "error", err)
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
