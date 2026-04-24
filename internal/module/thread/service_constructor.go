package thread

import (
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
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
	turns turn.Service,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
) Service {
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, nil, nil, nil, nil, nil)
}

func NewServiceWithPromptAssembly(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns turn.Service,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *platformconfig.Config,
	toolRegistry contract.ToolRegistry,
) Service {
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, nil, nil)
}

func NewServiceWithPromptAssemblyAndSharedFiles(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sharedFiles sharedfilestore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns turn.Service,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *platformconfig.Config,
	toolRegistry contract.ToolRegistry,
	promptStore promptstore.Store,
	promptClassifier classifier.Classifier,
) Service {
	return newService(logger, threadStore, bindingStore, sharedFiles, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, promptStore, promptClassifier)
}

func newService(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sharedFiles sharedfilestore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns turn.Service,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *platformconfig.Config,
	toolRegistry contract.ToolRegistry,
	promptStore promptstore.Store,
	promptClassifier classifier.Classifier,
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
		turns:            turns,
		orchestration:    orchestration,
		bus:              dispatcher,
		promptStore:      promptStore,
		classifier:       promptClassifier,
		emitStarted:      bus.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:      bus.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:      bus.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage: bus.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:    bus.NewEmitter[threaddto.Compacted](dispatcher),
		emitLaunched:     bus.NewEmitter[threaddto.Launched](dispatcher),
		threadAgents:     make(map[string]string),
	}
	// P22 P2 thread S3: the taskHandoffWorker owns the
	// onTurnCompleted -> refreshTaskHandoffFromThread slow-path so the bus
	// callback is a cheap Enqueue. Constructed here (not in module.go)
	// because the refresher is a service method and the worker is a
	// service-internal resource with the same lifetime as the service.
	s.taskHandoffWorker = newTaskHandoffWorker(s, logger)
	// P22 P2 thread S4: same ownership story for the agentLaunchedWorker
	// — the processor is a service method (processAgentLaunched), so the
	// worker lives beside the service.
	s.agentLaunchedWorker = newAgentLaunchedWorker(s, logger)
	return s
}
