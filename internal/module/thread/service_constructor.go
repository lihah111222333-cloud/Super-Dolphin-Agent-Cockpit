package thread

import (
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
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
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, nil, nil, nil)
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
	return newService(logger, threadStore, bindingStore, nil, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry)
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
) Service {
	return newService(logger, threadStore, bindingStore, sharedFiles, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry)
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
) Service {
	if logger == nil {
		logger = pkglogger.Get()
	}
	var dispatcher *event.Dispatcher
	if threadEvents != nil {
		dispatcher = threadEvents.Dispatcher()
	}
	return &service{
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
		emitStarted:      bus.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:      bus.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:      bus.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage: bus.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:    bus.NewEmitter[threaddto.Compacted](dispatcher),
		emitLaunched:     bus.NewEmitter[threaddto.Launched](dispatcher),
		threadAgents:     make(map[string]string),
	}
}
