package thread

import (
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
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
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, nil)
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
) Service {
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, promptAssembly)
}

func newService(
	logger *slog.Logger,
	threadStore threadstore.Store,
	bindingStore bindingstore.Store,
	sessions SessionProvider,
	starter SessionStarter,
	turns turn.Service,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
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
		sessions:         sessions,
		starter:          starter,
		promptAssembly:   promptAssembly,
		turns:            turns,
		orchestration:    orchestration,
		bus:              dispatcher,
		emitStarted:      bus.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:      bus.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:      bus.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage: bus.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:    bus.NewEmitter[threaddto.Compacted](dispatcher),
		threadAgents:     make(map[string]string),
	}
}
