package thread

import (
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformobs "github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

func NewService(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
) Service {
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func NewServiceWithPromptAssembly(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
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

func NewServiceWithPromptAssemblyAndSharedFiles(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sharedFiles sharedfilestore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptStore promptstore.Store,
	promptCatalog promptstore.RuntimePromptCatalog,
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

func newService(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sharedFiles sharedfilestore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptStore promptstore.Store,
	promptCatalog promptstore.RuntimePromptCatalog,
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
