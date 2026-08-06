package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	commandcardstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	terminaloutcomestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/terminaloutcome"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools/modelregistry"
	workspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/builtinprompts"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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
	logRuntime *pkglogger.Runtime
	stdioReady <-chan struct{} // closed when stdio server is ready
}

// bootstrapClient 定义 bootstrap 客户端的核心操作接口。
type bootstrapClient interface {
	InstallLogRelay(*pkglogger.Runtime)
	Start(context.Context) error
	Close() error
	hookSubscriber
}

// runtimeParams 是 bindRuntime 的 fx 依赖注入容器。
type runtimeParams struct {
	fx.In

	Logger     *slog.Logger
	LogRuntime *pkglogger.Runtime
	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
}

type openLogFileFunc func(string, int, os.FileMode) (*os.File, error)

// newLoggerRuntime creates the owner shared by mcp-orch transports and its relay.
func newLoggerRuntime() *pkglogger.Runtime {
	return pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
}

// newLogger 初始化日志写入器，优先写入 /tmp/mcp-orch-<pid>.log，失败时回退到 stderr。
func newLogger(logRuntime *pkglogger.Runtime, cfg *platformconfig.Config) (*slog.Logger, error) {
	return newLoggerWithOpenFile(logRuntime, cfg, os.OpenFile, os.Stderr)
}

// newLoggerWithOpenFile 初始化 mcp-orch logger，并把文件打开动作注入出来供测试覆盖失败分支。
func newLoggerWithOpenFile(logRuntime *pkglogger.Runtime, _ *platformconfig.Config, openLogFile openLogFileFunc, stderr io.Writer) (*slog.Logger, error) {
	if logRuntime == nil {
		return nil, errors.New("mcp-orch logger runtime is required")
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	// MCP stdio 的 stdout 留给 JSON-RPC，日志回退也只能写 stderr 或文件。
	logPath := fmt.Sprintf("/tmp/mcp-orch-%d.log", os.Getpid())
	if f, err := openLogFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		logRuntime.InitWithConsoleWriter(f)
	} else {
		slog.New(slog.NewTextHandler(stderr, nil)).Warn("mcp-orch logger fallback to stderr", "path", logPath, "error", err)
		logRuntime.InitWithConsoleWriter(stderr)
	}
	logRuntime.BindDefault()
	return logRuntime.Get(), nil
}

// newQueries 创建 mcp-orch store 使用的 sqlc 查询集。
func newQueries(db *sql.DB) *sqlc.Queries { return sqlc.New(db) }

// newAgentThreadStore 创建 orchestration 读取持久化 thread 的适配 store。
func newAgentThreadStore(db *sql.DB) orchestration.AgentThreadStore {
	return agentstore.NewThreadStore(db)
}

// newTerminalOutcomeStore 创建 canonical terminal commit/public read/outbox 端口。
func newTerminalOutcomeStore(db *sql.DB) contract.TerminalOutcomeCommitPort {
	return terminaloutcomestore.New(db)
}

// newAgentBindingStore 创建 orchestration 读取 provider binding 的适配 store。
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

// newNoopSessionCleaner 返回 standalone 模式下的空 session cleaner。
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

// newNoopTurnStarter 返回 standalone 模式下的 turn starter，占位实现会 fail-fast。
func newNoopTurnStarter() contract.OrchestrationTurnStarter {
	return noopTurnStarter{}
}

// newRegistryParams 是 newRegistry 的 fx 依赖注入容器。
type newRegistryParams struct {
	fx.In

	AgentLaunch      contract.AgentLaunchPort
	AgentState       contract.AgentStateReader
	AgentStopWait    contract.AgentStopWaitPort
	AgentRecovery    contract.AgentRecoveryPort
	AgentInterrupt   contract.AgentInterruptPort
	AgentReports     contract.AgentReportPort
	TurnSubmission   contract.TurnSubmissionPort
	DAGCreate        contract.DAGCreateRuntime
	DAGRuntime       contract.DAGRuntime
	DAGDelete        contract.DAGDeleteRuntime
	DAGNodeStatus    contract.DAGNodeStatusRuntime
	DAGNodeDispatch  contract.DAGNodeDispatchRuntime
	WS               workspace.Service
	Prompt           promptstore.Store
	BuiltinPrompts   contract.BuiltinPromptRegistry
	Command          commandcardstore.Store
	SharedFile       sharedfilestore.Store
	ModelRegistry    modelregistry.Registry
	AgentIDGenerator *idgen.Generator
}

// newModelRegistry 创建模型注册表，失败时报错阻断启动。
func newModelRegistry(logger *slog.Logger) (modelregistry.Registry, error) {
	registry, err := modelregistry.NewDefaultRegistry(modelregistry.WithLogger(logger))
	if err == nil {
		return registry, nil
	}
	return nil, fmt.Errorf("model registry load failed for %s: %w", modelregistry.DefaultRegistryPath(), err)
}

// newBuiltinPromptRegistry 创建内置 prompt registry，供 mcp-orch 工具直接读取。
func newBuiltinPromptRegistry() (contract.BuiltinPromptRegistry, error) {
	return builtinprompts.NewDefaultRegistry()
}

// newRegistry 汇总 orchestration、workspace、prompt 等依赖并注册 MCP tools。
func newRegistry(p newRegistryParams) tools.Registry {
	return tools.NewRegistryWithAgentIDGenerator(tools.Dependencies{
		ToolPorts:      toolPortsFromRegistryParams(p),
		Workspace:      p.WS,
		Prompt:         p.Prompt,
		BuiltinPrompts: p.BuiltinPrompts,
		CommandCard:    p.Command,
		SharedFile:     p.SharedFile,
		ModelRegistry:  p.ModelRegistry,
	}, p.AgentIDGenerator)
}

// toolPortsFromRegistryParams 把 fx 注入的 contract 窄口装配为工具 registry 的端口集合。
func toolPortsFromRegistryParams(p newRegistryParams) tools.ToolPorts {
	return tools.ToolPorts{
		AgentLaunch:            p.AgentLaunch,
		AgentMessenger:         newSendMessagePorts(p.AgentState, p.AgentReports, p.TurnSubmission),
		AgentStopWait:          p.AgentStopWait,
		AgentRecovery:          p.AgentRecovery,
		AgentInterrupt:         p.AgentInterrupt,
		AgentList:              newAgentListPorts(p.AgentState, p.AgentReports),
		AgentReports:           p.AgentReports,
		DAGCreate:              p.DAGCreate,
		DAGRuntime:             p.DAGRuntime,
		DAGDelete:              p.DAGDelete,
		NodeStatus:             p.DAGNodeStatus,
		NodeDispatch:           p.DAGNodeDispatch,
		WorkflowDiagnostics:    p.DAGRuntime,
		WorkflowRecovery:       p.DAGRuntime,
		DAGIdentityDiagnostics: p.DAGRuntime,
	}
}

// newSendMessagePorts 在依赖完整时返回 send_message 的分离端口集合。
func newSendMessagePorts(
	state contract.AgentStateReader,
	reports contract.AgentReportPort,
	turns contract.TurnSubmissionPort,
) tools.SendMessagePorts {
	if state == nil || reports == nil || turns == nil {
		return tools.SendMessagePorts{}
	}
	return tools.SendMessagePorts{
		Snapshots: state,
		Reports:   reports,
		Turns:     turns,
	}
}

// newAgentListPorts 在依赖完整时返回 list_agents 的分离端口集合。
func newAgentListPorts(state contract.AgentStateReader, reports contract.AgentReportPort) tools.AgentListPorts {
	if state == nil || reports == nil {
		return tools.AgentListPorts{}
	}
	return tools.AgentListPorts{
		Snapshots: state,
		Reports:   reports,
	}
}

// newStdioServer 创建 mcp-orch stdio MCP server，stdout 使用已绑定的 MCP 输出通道。
// mcpStdout 由 main() 最早阶段写入；nil 表示程序装配顺序异常，必须 fail-fast 阻止用脏 stdout 破坏 JSON-RPC framing。
func newStdioServer(registry tools.Registry, logRuntime *pkglogger.Runtime) (*common.Server, error) {
	stdout := mcpStdout.Load()
	if stdout == nil {
		return nil, fmt.Errorf("mcp-orch: mcpStdout not initialized; program assembly order is broken")
	}
	transport := common.NewStdioTransport(os.Stdin, stdout)
	return common.NewServer(
		"mcp-orch",
		"dev",
		transport,
		registryToolProvider{registry: registry},
		common.WithToolErrorClassifier(tools.ToolErrorClassifier),
		common.WithLoggerRuntime(logRuntime),
	), nil
}

// newStdioRunner 将 stdio MCP server 适配为 run group runner。
func newStdioRunner(server *common.Server) platformrunner.Runner {
	return server
}

// newBootstrapRunner 创建 peer bootstrap runner，等待 stdio server ready 后再注册主控。
func newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client, logRuntime *pkglogger.Runtime, server *common.Server) (platformrunner.Runner, error) {
	if logRuntime == nil {
		return nil, errors.New("mcp-orch logger runtime is required")
	}
	return bootstrapRunner{cfg: cfg, client: client, logRuntime: logRuntime, stdioReady: server.Ready()}, nil
}

// ListTools 只把 registry 定义转换为 MCP tool 列表，不解释 scope。
// scope 已经由上游放进 ctx，别在这里让 stdio/bootstrap 分叉。
func (p registryToolProvider) ListTools(context.Context) ([]mcpdto.MCPTool, error) {
	defs, err := p.registry.List()
	if err != nil {
		return nil, err
	}
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

// marshalInputSchema 将工具 schema 复制为 JSON，空 schema 输出空对象。
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

// handleToolCall 查找并调用 registry tool，未知工具和未配置 handler 都直接报错。
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

// Run 在 peer 模式才向主控注册，让 toolbridge 能代理 tools/list 和 tools/call。
// 普通 stdio sidecar 不能注册，否则可能被主控当独立 peer 清理掉。
func (r bootstrapRunner) Run(ctx context.Context) error {
	r.client.InstallLogRelay(r.logRuntime)
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
	// 只有独立 peer 模式才向控制面注册；stdio sidecar 若注册，会被 sweeper 误认为可回收进程。
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
	// bootstrap 成功后订阅生命周期 hook，让 hookConsumer 能接收 session、turn、state 和进程退出事件。
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
			// sidecar 是独立子进程，故意用 background context 作为 run group 根。
			// 父进程关闭和 run group 自行退出最终都会进入 OnStop，由 cancel 和 done channel 收口。
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
