package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	agentstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/agent"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools/modelregistry"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/builtinprompts"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

// registryToolProvider 把 tools.Registry 适配为 common.ToolProvider 接口，供 stdio/HTTP/bootstrap 共用同一套工具注册表。
type registryToolProvider struct {
	registry tools.Registry
}

// bootstrapRunner 负责向主控注册并订阅 hook，在 peer 模式下启动 bootstrap 客户端。
type bootstrapRunner struct {
	cfg        bootstrap.Config
	client     bootstrapClient
	stdioReady <-chan struct{} // closed when stdio server is ready
}

// bootstrapClient 定义 bootstrap 客户端的核心操作接口。
type bootstrapClient interface {
	InstallLogRelay()
	Start(context.Context) error
	Close() error
	hookSubscriber
}

// runtimeParams 是 bindRuntime 的 fx 依赖注入容器。
type runtimeParams struct {
	fx.In

	Logger     *slog.Logger
	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

// newLogger 初始化日志写入器，优先写入 /tmp/mcp-orch-<pid>.log，失败时回退到 stderr。
func newLogger(cfg *platformconfig.Config) *slog.Logger {
	// MCP stdio uses stdout for JSON-RPC; keep local fallback logs off stdout.
	logPath := fmt.Sprintf("/tmp/mcp-orch-%d.log", os.Getpid())
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		pkglogger.InitWithConsoleWriter(f)
	} else {
		pkglogger.InitWithConsoleWriter(os.Stderr)
	}
	return pkglogger.Get()
}

// newQueries 创建 sqlc 查询集。
func newQueries(db *sql.DB) *sqlc.Queries { return sqlc.New(db) }

// newAgentThreadStore 创建线程存储。
func newAgentThreadStore(db *sql.DB) orchestration.AgentThreadStore {
	return agentstore.NewThreadStore(db)
}

// newAgentBindingStore 创建 agent 绑定存储。
func newAgentBindingStore(db *sql.DB) orchestration.AgentBindingStore {
	return agentstore.NewBindingStore(db)
}

// mcpOrchDBReadyProbe 是数据库就绪探针接口，用于启动时校验 schema 版本。
type mcpOrchDBReadyProbe interface {
	PingContext(context.Context) error
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// verifyMCPOrchDatabaseReady 校验数据库连通性和最低 schema 版本，启动时 fail-fast。
func verifyMCPOrchDatabaseReady(ctx context.Context, probe mcpOrchDBReadyProbe) error {
	if err := probe.PingContext(ctx); err != nil {
		return fmt.Errorf("mcp-orch database ping failed: %w", err)
	}
	if err := platformdb.VerifyMinSchemaVersion(ctx, probe); err != nil {
		return fmt.Errorf("mcp-orch database schema check failed: %w", err)
	}
	return nil
}

// noopSessionCleaner 在独立模式下满足 contract.OrchestrationSessionCleaner 接口。
type noopSessionCleaner struct{}

// RemoveSession 移除 runtime 中的会话记录。
func (noopSessionCleaner) RemoveSession(sessionID string) {
	_ = sessionID
}

// RemoveSessionGeneration 移除会话代际记录。
func (noopSessionCleaner) RemoveSessionGeneration(sessionID string, generation uint64) {
	_ = sessionID
	_ = generation
}

func newNoopSessionCleaner() contract.OrchestrationSessionCleaner {
	return noopSessionCleaner{}
}

// noopTurnStarter 在独立模式下满足 contract.OrchestrationTurnStarter 接口，提交 turn 时返回错误。
type noopTurnStarter struct{}

// StartTurn 把 cron 运行转换成一次线程 turn。
func (noopTurnStarter) StartTurn(context.Context, contract.TurnSubmission) (string, error) {
	return "", errors.New("turn starter not available in mcp-orch standalone mode")
}

// WaitForSessionReady 等待会话进入可接收 turn 的状态。
func (noopTurnStarter) WaitForSessionReady(context.Context, string, time.Duration) error {
	return nil
}

func newNoopTurnStarter() contract.OrchestrationTurnStarter {
	return noopTurnStarter{}
}

// newRegistryParams 是 newRegistry 的 fx 依赖注入容器。
type newRegistryParams struct {
	fx.In

	Orchestration  contract.OrchestrationService
	WS             workspace.Service
	Prompt         promptstore.Store
	BuiltinPrompts contract.BuiltinPromptRegistry
	Command        commandcardstore.Store
	SharedFile     sharedfilestore.Store
	ModelRegistry  modelregistry.Registry
}

// newModelRegistry 创建模型注册表，失败时报错阻断启动。
func newModelRegistry(logger *slog.Logger) (modelregistry.Registry, error) {
	registry, err := modelregistry.NewDefaultRegistry(modelregistry.WithLogger(logger))
	if err == nil {
		return registry, nil
	}
	return nil, fmt.Errorf("model registry load failed for %s: %w", modelregistry.DefaultRegistryPath(), err)
}

func newBuiltinPromptRegistry() (contract.BuiltinPromptRegistry, error) {
	return builtinprompts.NewDefaultRegistry()
}

func newRegistry(p newRegistryParams) tools.Registry {
	return tools.NewRegistry(tools.Dependencies{
		Orchestration:  p.Orchestration,
		Workspace:      p.WS,
		Prompt:         p.Prompt,
		BuiltinPrompts: p.BuiltinPrompts,
		CommandCard:    p.Command,
		SharedFile:     p.SharedFile,
		ModelRegistry:  p.ModelRegistry,
	})
}

func newStdioServer(registry tools.Registry) *common.Server {
	stdout := mcpStdout.Load()
	if stdout == nil {
		stdout = os.Stdout
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer("mcp-orch", "dev", transport, registryToolProvider{registry: registry})
}

// newStdioRunner adapts the stdio *common.Server as a Runner for the run group.
func newStdioRunner(server *common.Server) platformrunner.Runner {
	return server
}

func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, server *common.Server) platformrunner.Runner {
	return bootstrapRunner{cfg: cfg, client: client, stdioReady: server.Ready()}
}

// 这里只转 registry，不解释 scope。
// scope 已经由上游放进 ctx，别在这里让 stdio/bootstrap 分叉。
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

// CallTool 调用已注册工具并返回标准工具结果。
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

// 只有 peer 模式才向主控注册，让 toolbridge 能代理 tools/list 和 tools/call。
// 普通 stdio sidecar 不能注册，否则可能被主控当独立 peer 清理掉。
func (r bootstrapRunner) Run(ctx context.Context) error {
	r.client.InstallLogRelay()
	if r.stdioReady != nil {
		select {
		case <-r.stdioReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

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
		return errors.Join(err, r.client.Close())
	}
	<-ctx.Done()
	return r.client.Close()
}

// bindRuntime 把 runtime 能力绑定到 mcp-orch RPC 服务。
func bindRuntime(lc fx.Lifecycle, params runtimeParams) {
	logger := params.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	var (
		cancel       context.CancelFunc
		shutdownOnce sync.Once
	)
	done := make(chan error, 1)
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			platformshared.LogIgnoredError(logger, "mcp-orch shutdown error", params.Shutdowner.Shutdown())
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
			runtimesafe.SafeGo(runCtx, logger, "mcp-orch.runtime.runGroup", func(context.Context) {
				err := platformrunner.RunGroup(runCtx, params.Runners, platformrunner.GroupOptions{
					EnableSignals: false,
				})
				done <- err
				close(done)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("mcp-orch runtime exited", "error", err)
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
