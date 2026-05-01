package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

type registryToolProvider struct {
	registry tools.Registry
}

type bootstrapRunner struct {
	cfg    bootstrap.Config
	client bootstrapClient
}

type bootstrapClient interface {
	Start(context.Context) error
	Close() error
	hookSubscriber
}

type runtimeParams struct {
	fx.In

	Logger     *slog.Logger
	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

func newLogger(cfg *platformconfig.Config) *slog.Logger {
	// MCP stdio transport uses stdout for JSON-RPC messages.
	// Write logs to a file so they are visible for debugging.
	// stderr is swallowed by the codex app-server parent process.
	logPath := fmt.Sprintf("/tmp/mcp-orch-%d.log", os.Getpid())
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		pkglogger.InitWithConsoleWriter(f)
	} else {
		pkglogger.InitWithConsoleWriter(os.Stderr)
	}
	return pkglogger.Get()
}

func newPool(cfg *platformconfig.Config) (*pgxpool.Pool, error) {
	return platformdb.NewPool(cfg)
}

func newQueries(pool *pgxpool.Pool) *sqlc.Queries {
	return sqlc.New(pool)
}

// threadStoreAdapter wraps internal/store/thread.Store and converts its
// types to the orchestration-local PersistedThread DTO so the orchestration
// subpackage never imports internal/store/* (modularity-convention §2.4).
type threadStoreAdapter struct {
	inner threadstore.Store
}

func (a threadStoreAdapter) ListAll(ctx context.Context) ([]orchestration.PersistedThread, error) {
	threads, err := a.inner.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]orchestration.PersistedThread, len(threads))
	for i, t := range threads {
		out[i] = toPersistedThread(t)
	}
	return out, nil
}

func (a threadStoreAdapter) GetByThreadID(ctx context.Context, threadID string) (*orchestration.PersistedThread, error) {
	t, err := a.inner.GetByThreadID(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	pt := toPersistedThread(*t)
	return &pt, nil
}

func (a threadStoreAdapter) UpdateStatus(ctx context.Context, params orchestration.PersistedThreadStatusUpdate) error {
	return a.inner.UpdateStatus(ctx, threadstore.UpdateStatusParams{
		ThreadID:  params.ThreadID,
		Status:    params.Status,
		UpdatedAt: params.UpdatedAt,
	})
}

func toPersistedThread(t threadstore.Thread) orchestration.PersistedThread {
	return orchestration.PersistedThread{
		ThreadID:      t.ThreadID,
		AgentID:       t.AgentID,
		ParentAgentID: t.ParentAgentID,
		Name:          t.Name,
		Prompt:        t.Prompt,
		Cwd:           t.Cwd,
		Status:        t.Status,
		Port:          t.Port,
		PID:           t.PID,
		CreatedAt:     t.CreatedAt,
		UpdatedAt:     t.UpdatedAt,
		PendingLaunch: t.PendingLaunch,
	}
}

// bindingStoreAdapter wraps internal/store/binding.Store and converts its
// types to the orchestration-local PersistedBinding DTO (modularity-convention §2.4).
type bindingStoreAdapter struct {
	inner bindingstore.Store
}

func (a bindingStoreAdapter) GetByAgentID(ctx context.Context, agentID string) (*orchestration.PersistedBinding, error) {
	b, err := a.inner.GetByAgentID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	pb := orchestration.PersistedBinding{
		AgentID:          b.AgentID,
		Provider:         b.Provider,
		ProviderThreadID: b.ProviderThreadID,
		CodexThreadID:    b.CodexThreadID,
		Cwd:              b.Cwd,
		Archived:         b.Archived,
		CreatedAt:        b.CreatedAt,
		UpdatedAt:        b.UpdatedAt,
	}
	return &pb, nil
}

func (a bindingStoreAdapter) SetArchived(ctx context.Context, params orchestration.PersistedBindingArchiveUpdate) error {
	return a.inner.SetArchived(ctx, bindingstore.SetArchivedParams{
		AgentID:   params.AgentID,
		Archived:  params.Archived,
		UpdatedAt: params.UpdatedAt,
	})
}

func newAgentThreadStore(pool *pgxpool.Pool) orchestration.AgentThreadStore {
	return threadStoreAdapter{inner: threadstore.NewStoreFromPool(pool)}
}

func newAgentBindingStore(pool *pgxpool.Pool) orchestration.AgentBindingStore {
	return bindingStoreAdapter{inner: bindingstore.NewStoreFromPool(pool)}
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

// noopSessionCleaner satisfies contract.OrchestrationSessionCleaner in
// standalone mode. P22 P4 S4b: RemoveSessionGeneration is now part of
// the owner contract; the noop impl returns silently to preserve the
// pre-S4b duck-typing behavior (the old type assertion would have
// failed and the service would fall through to the generation-unaware
// RemoveSession, which in this noop case is also a no-op).
type noopSessionCleaner struct{}

func (noopSessionCleaner) RemoveSession(string) {}

func (noopSessionCleaner) RemoveSessionGeneration(string, uint64) {}

func newNoopSessionCleaner() contract.OrchestrationSessionCleaner {
	return noopSessionCleaner{}
}

// noopTurnStarter satisfies contract.OrchestrationTurnStarter in standalone mode.
// Turn submission via orchestration will return an error because no upstream turn
// service is wired.
//
// P22 P4 S4a: after WaitForSessionReady joined the owner contract, this
// noop type must commit to it too; returning nil matches the pre-S4a
// duck-typing path in cmd/mcp-orch/orchestration/helpers.go where the
// type-assertion would have failed and the helper returned nil.
type noopTurnStarter struct{}

func (noopTurnStarter) StartTurn(context.Context, contract.TurnSubmission) (string, error) {
	return "", errors.New("turn starter not available in mcp-orch standalone mode")
}

func (noopTurnStarter) WaitForSessionReady(context.Context, string, time.Duration) error {
	return nil
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
	memory contract.MemoryService,
) tools.Registry {
	return tools.NewRegistry(tools.Dependencies{
		Orchestration: orchestration,
		Workspace:     ws,
		Prompt:        prompt,
		CommandCard:   command,
		SharedFile:    sharedFile,
		Memory:        memory,
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

func (p registryToolProvider) ListTools(context.Context) ([]mcpdto.MCPTool, error) {
	defs := p.registry.List()
	toolsList := make([]mcpdto.MCPTool, 0, len(defs))
	for _, def := range defs {
		schema, err := marshalInputSchema(def.InputSchema)
		if err != nil {
			return nil, err
		}
		toolsList = append(toolsList, mcpdto.MCPTool{
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
		)
		<-ctx.Done()
		return nil
	}
	// Only register with the control plane when running as an independent
	// peer (GO_AGENT_PEER_MODE=1), spawned by ServerManager for toolbridge.
	// When codex spawns mcp-orch as a stdio MCP sidecar, bootstrap
	// registration causes sweeper eviction that kills the process.
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		pkglogger.Info("mcp-orch bootstrap skipped (sidecar mode)",
			"rpc_addr", r.cfg.RPCAddr,
			"binary_name", r.cfg.BinaryName,
		)
		<-ctx.Done()
		return nil
	}
	pkglogger.Info("mcp-orch bootstrap starting (peer mode)",
		"rpc_addr", r.cfg.RPCAddr,
		"binary_name", r.cfg.BinaryName,
	)
	if err := r.client.Start(ctx); err != nil {
		pkglogger.Error("mcp-orch bootstrap start failed",
			"binary_name", r.cfg.BinaryName,
			"rpc_addr", r.cfg.RPCAddr,
			"error", err,
		)
		return err
	}
	// P15: subscribe to lifecycle hooks so hookConsumer receives
	// agent.session.start / agent.turn.* / agent.state.change / agent.process.exit.
	if err := subscribeOrchestrationHooks(ctx, r.client); err != nil {
		pkglogger.Warn("mcp-orch hook subscribe failed", "error", err)
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
