package thread

import (
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// NewService 构造最小 thread 服务。
// 该入口只装配 store、session、starter、turn 清理和 orchestration，适合不需要 prompt assembly 的测试或轻量运行时。
func NewService(
	logger *slog.Logger,
	threadStore threadServiceStorePort,
	bindingStore bindingServiceStorePort,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
) Service {
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, nil, nil, nil, nil, nil, nil, nil, nil)
}

// NewServiceWithPromptAssembly 构造带 prompt assembly 的 thread 服务。
// 它额外接入配置和工具 registry，使 thread/start 可以把 prompt、MCP 和工具上下文交给 prompt 模块组装。
func NewServiceWithPromptAssembly(
	logger *slog.Logger,
	threadStore threadServiceStorePort,
	bindingStore bindingServiceStorePort,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
) Service {
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, nil, nil, nil, nil, nil)
}

// NewServiceWithPromptAssemblyAndSharedFiles 构造完整 thread 服务。
// 除 prompt assembly 外，它还接入 runtime prompt catalog、match/enable_when 评估器和可选 tracing。
func NewServiceWithPromptAssemblyAndSharedFiles(
	logger *slog.Logger,
	threadStore threadServiceStorePort,
	bindingStore bindingServiceStorePort,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptCatalog promptServiceCatalogPort,
	matchWhenEval contract.MatchWhenEvaluator,
	enableWhenEval contract.EnableWhenEvaluator,
	tracingOpt ...*platformobs.Service,
) Service {
	var tracing *platformobs.Service
	if len(tracingOpt) > 0 {
		tracing = tracingOpt[0]
	}
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, mcpServers, promptCatalog, matchWhenEval, enableWhenEval, tracing)
}

// newService 统一完成 thread service wiring。
// 构造阶段会创建事件 emitter、后台 worker 和进程内缓存；外层构造器只负责选择依赖集合。
func newService(
	logger *slog.Logger,
	threadStore threadServiceStorePort,
	bindingStore bindingServiceStorePort,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptCatalog promptServiceCatalogPort,
	matchWhenEval contract.MatchWhenEvaluator,
	enableWhenEval contract.EnableWhenEvaluator,
	tracing *platformobs.Service,
) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	var dispatcher *event.Dispatcher
	if threadEvents != nil {
		dispatcher = threadEvents.Dispatcher()
	}
	s := &service{
		logger:                  logger,
		threadStore:             threadStore,
		bindingStore:            bindingStore,
		sessions:                sessions,
		starter:                 starter,
		promptAssembly:          promptAssembly,
		cfg:                     cfg,
		toolRegistry:            toolRegistry,
		mcpServers:              mcpServers,
		turns:                   turns,
		orchestration:           orchestration,
		sessionGenerationBinder: sessionGenerationBinderFromOrchestration(orchestration),
		tracing:                 tracing,
		bus:                     dispatcher,
		promptCatalog:           promptCatalog,
		matchWhenEval:           matchWhenEval,
		enableWhenEval:          enableWhenEval,
		emitStarted:             contract.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:             contract.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:             contract.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage:        contract.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:           contract.NewEmitter[threaddto.Compacted](dispatcher),
		emitLaunched:            contract.NewEmitter[threaddto.Launched](dispatcher),
		threadAgents:            make(map[string]string),
		reconnectDelay:          sessionRecoveryReconnectDelay,
	}
	// Workers live beside service methods they call; bus callbacks only enqueue.
	s.agentLaunchedWorker = newAgentLaunchedWorker(s, logger)
	s.sessionRecoveryWorker = newSessionRecoveryWorker(s, logger)
	return s
}

// sessionGenerationBinderFromOrchestration 从兼容 facade 里提取独立 generation 绑定端口。
func sessionGenerationBinderFromOrchestration(orchestration OrchestrationFacade) SessionGenerationBinder {
	binder, _ := orchestration.(SessionGenerationBinder)
	return binder
}
