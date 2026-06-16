package thread

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/kelindar/event"
)

// NewService 创建服务。
func NewService(
	logger *pkglogger.Logger,
	threadStore contract.ThreadStore,
	bindingStore contract.BindingStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
) Service {
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

// NewServiceWithPromptAssembly 创建带promptassembly的服务。
func NewServiceWithPromptAssembly(
	logger *pkglogger.Logger,
	threadStore contract.ThreadStore,
	bindingStore contract.BindingStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
) Service {
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, nil, nil, nil, nil, nil, nil)
}

// NewServiceWithPromptAssemblyAndSharedFiles 创建带promptassemblyshared文件的服务。
func NewServiceWithPromptAssemblyAndSharedFiles(
	logger *pkglogger.Logger,
	threadStore contract.ThreadStore,
	bindingStore contract.BindingStore,
	sharedFiles contract.SharedFileStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptStore contract.PromptStore,
	promptCatalog contract.RuntimePromptCatalog,
	matchWhenEval contract.MatchWhenEvaluator,
	enableWhenEval contract.EnableWhenEvaluator,
	tracingOpt ...*platformobs.Service,
) Service {
	var tracing *platformobs.Service
	if len(tracingOpt) > 0 {
		tracing = tracingOpt[0]
	}
	return newService(logger, threadStore, bindingStore, sharedFiles, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, mcpServers, promptStore, promptCatalog, matchWhenEval, enableWhenEval, tracing)
}

// newService 创建服务。
func newService(
	logger *pkglogger.Logger,
	threadStore contract.ThreadStore,
	bindingStore contract.BindingStore,
	sharedFiles contract.SharedFileStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptStore contract.PromptStore,
	promptCatalog contract.RuntimePromptCatalog,
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
		logger:           logger,
		threadStore:      threadStore,
		bindingStore:     bindingStore,
		sharedFiles:      sharedFiles,
		sessions:         sessions,
		starter:          starter,
		promptAssembly:   promptAssembly,
		cfg:              cfg,
		toolRegistry:     toolRegistry,
		mcpServers:       mcpServers,
		turns:            turns,
		orchestration:    orchestration,
		tracing:          tracing,
		bus:              dispatcher,
		promptStore:      promptStore,
		promptCatalog:    promptCatalog,
		matchWhenEval:    matchWhenEval,
		enableWhenEval:   enableWhenEval,
		emitStarted:      contract.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:      contract.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:      contract.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage: contract.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:    contract.NewEmitter[threaddto.Compacted](dispatcher),
		emitLaunched:     contract.NewEmitter[threaddto.Launched](dispatcher),
		threadAgents:     make(map[string]string),
		reconnectDelay:   sessionRecoveryReconnectDelay,
	}
	// Workers live beside service methods they call; bus callbacks only enqueue.
	s.agentLaunchedWorker = newAgentLaunchedWorker(s, logger)
	s.sessionRecoveryWorker = newSessionRecoveryWorker(s, logger)
	return s
}
