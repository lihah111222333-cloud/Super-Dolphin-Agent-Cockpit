package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	commandstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/commandcard"
	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	sharedstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	dagstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	workspacestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools/modelregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// newStandaloneAppWithPrincipalIssuer 只装配独立运行图，身份来源由启动根入口显式提供。
func newStandaloneAppWithPrincipalIssuer(cfg standalone.Config, credential, readyPath string, db *sql.DB, issuer common.CallerPrincipalIssuer, lock *standalone.InstanceLock, extra ...fx.Option) *fx.App {
	options := []fx.Option{fx.NopLogger,
		fx.Supply(cfg, db, standaloneHTTPSettings{credential: credential, readyPath: readyPath, principalIssuer: issuer, instanceLock: lock}),
		fx.Provide(newStandalonePlatformConfig, newStandaloneLogger, newLoggerRuntime,
			newQueries, newAgentThreadStore, newAgentBindingStore,
			newTerminalOutcomeStore, newBuiltinPromptRegistry, newStandaloneModels, idgen.NewGenerator,
			directpeerstore.NewStore,
			newStandaloneExecution, newStandaloneLauncher, newStandaloneTurnStarter, newStandaloneSessionCleaner,
			func(store workspacestore.Store, dispatcher *event.Dispatcher) workspace.Service {
				return workspace.NewService(store, dispatcher)
			}, newRegistry,
			fx.Annotate(newStandaloneHTTPRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(newStandaloneDirectPeerEventProjector, fx.As(new(platformrunner.Runner)), fx.ResultTags(`group:"runners"`))),
		bus.Module, canonicalOwnerModules(), promptstore.Module, commandstore.Module, sharedstore.Module, dagstore.Module, workspacestore.Module,
		orchestrationLifecycleOptions(), orchestrationDAGOptions(), orchestrationNodeExecutionOptions(),
		fx.Invoke(bindRuntime), fx.Invoke(bindStandaloneExecutionSink)}
	return fx.New(append(options, extra...)...)
}

// newStandalonePlatformConfig 显式转换启动配置，空 RPCAddr 不会导出宿主环境变量。
func newStandalonePlatformConfig(cfg standalone.Config) *contract.Config {
	return &contract.Config{ProjectRoot: cfg.WorkspaceRoot, SQLitePath: filepath.Join(cfg.StateDir, "orch.sqlite"),
		LogLevel: "info", Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction}}
}

// newStandaloneLogger 只写 stderr，不读取默认日志路径或记录凭据。
func newStandaloneLogger(runtime *pkglogger.Runtime) *slog.Logger {
	runtime.InitWithConsoleWriter(os.Stderr)
	runtime.BindDefault()
	return runtime.Get()
}

// newStandaloneModels 从显式 provider 配置派生同一工具模型 registry。
func newStandaloneModels(cfg standalone.Config) modelregistry.Registry {
	providers := make([]modelregistry.ProviderModels, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		available := true
		providers = append(providers, modelregistry.ProviderModels{Provider: p.Name, Models: append([]string(nil), p.Models...), Available: &available})
	}
	return modelregistry.NewStaticRegistry(providers)
}

// newStandaloneDB 只打开数据库，关闭责任由 serve 根入口持有，不依赖 Fx 图构造成功。
func newStandaloneDB(cfg *contract.Config) (*sql.DB, error) {
	db, err := platformdb.NewDB(cfg)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func newStandaloneLauncher(execution *standalone.Execution) (orchestration.AgentLauncher, error) {
	return orchestration.NewCLILauncher(execution)
}

// newStandaloneExecution 把全部受管 CLI 资源的关闭纳入根生命周期。
func newStandaloneExecution(lc fx.Lifecycle, cfg standalone.Config, db *sql.DB) (*standalone.Execution, error) {
	execution := standalone.NewExecution(cfg)
	if err := execution.BindSessionStore(agentstore.NewSessionStore(db)); err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return execution.Close() }})
	return execution, nil
}

// bindStandaloneExecutionSink 把独立 provider 的 canonical 终态绑定到 orchestration owner。
func bindStandaloneExecutionSink(execution *standalone.Execution, sink orchestration.AgentTerminalHandler) error {
	if execution == nil || sink == nil {
		return errors.New("standalone execution terminal sink requires execution and service")
	}
	if receiptSink, ok := sink.(orchestration.AgentTerminalReceiptHandler); ok {
		execution.BindTerminalSinkWithReceipt(receiptSink.HandleTerminalReceipt)
		return nil
	}
	execution.BindTerminalSink(sink.HandleTerminal)
	return nil
}

type standaloneTurnStarter struct{ execution *standalone.Execution }

// StartTurn 与 CLI launcher 使用同一执行器，不将任务交给宿主。
func (s standaloneTurnStarter) StartTurn(ctx context.Context, sub contract.TurnSubmission) (string, error) {
	return s.execution.SubmitTurn(ctx, sub)
}

// WaitForSessionReady 当前执行能力尚未接通，不报告虚假就绪。
func (s standaloneTurnStarter) WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error {
	return s.execution.WaitForSessionReady(ctx, agentID, timeout)
}

func newStandaloneTurnStarter(execution *standalone.Execution) contract.OrchestrationTurnStarter {
	return standaloneTurnStarter{execution: execution}
}

// newStandaloneSessionCleaner CLI 句柄生命周期由执行器独占，无宿主 session registry。
func newStandaloneSessionCleaner() contract.OrchestrationSessionCleaner {
	return newNoopSessionCleaner()
}

type standaloneHTTPSettings struct {
	credential, readyPath string
	principalIssuer       common.CallerPrincipalIssuer
	instanceLock          *standalone.InstanceLock
}

type standaloneHTTPRunner struct {
	server                        *common.HTTPServer
	listen, readyPath, instanceID string
	instanceLock                  *standalone.InstanceLock
}

// newStandaloneHTTPRunner 校验工具注册表后注入单实例的 HTTP 生命周期 runner。
func newStandaloneHTTPRunner(cfg standalone.Config, settings standaloneHTTPSettings, registry tools.Registry, logger *pkglogger.Runtime) (platformrunner.Runner, error) {
	source, ok := settings.principalIssuer.(*standaloneOperatorSource)
	if !ok || source == nil {
		return nil, errors.New("standalone HTTP requires a persistent operator source")
	}
	if _, err := registry.List(); err != nil {
		return nil, err
	}
	provider, err := newStandaloneOperatorToolProvider(standaloneHTTPToolProvider{base: registryToolProvider{registry: registry}}, source)
	if err != nil {
		return nil, err
	}
	server, err := common.NewLocalHTTPServer("mcp-orch", "standalone-v1", provider,
		common.LocalHTTPProfile{WorkspaceRoot: cfg.WorkspaceRoot, BearerToken: settings.credential, PrincipalIssuer: settings.principalIssuer}, logger)
	if err != nil {
		return nil, err
	}
	return &standaloneHTTPRunner{server: server, listen: cfg.Listen, readyPath: settings.readyPath, instanceID: shared.NewID("orch"), instanceLock: settings.instanceLock}, nil
}

// newStandaloneDirectPeerEventProjector 保留 standalone 图的命名入口，实际复用共享 provider。
func newStandaloneDirectPeerEventProjector(store *directpeerstore.Store, dispatcher *event.Dispatcher, logger *slog.Logger) (platformrunner.Runner, error) {
	return newDirectPeerEventProjector(store, dispatcher, logger)
}

// Run 先绑定实际端口再发布 ready，退出只删除本次拥有的发现文件。
func (r *standaloneHTTPRunner) Run(ctx context.Context) (err error) {
	listener, err := net.Listen("tcp", r.listen)
	if err != nil {
		return err
	}
	var cleanup func() error
	ready := standalone.Ready{Endpoint: "http://" + listener.Addr().String() + "/mcp", InstanceID: r.instanceID}
	if r.instanceLock != nil {
		cleanup, err = standalone.PublishReadyAfterInstanceLock(r.instanceLock, r.readyPath, ready)
	} else {
		cleanup, err = standalone.PublishReady(r.readyPath, ready)
	}
	if err != nil {
		return errors.Join(err, listener.Close())
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	return r.server.ServeLocal(ctx, listener)
}
