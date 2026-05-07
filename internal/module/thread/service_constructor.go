package thread

import (
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
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
	promptStore promptstore.Store,
	promptClassifier contract.PromptClassifier,
	classifierFastPath contract.ClassifierFastPathFunc,
	classifierPrune contract.ClassifierPruneCandidatesFunc,
	classifierMaxCandidates contract.ClassifierMaxCandidatesFunc,
	matchWhenEval contract.MatchWhenEvaluator,
) Service {
	return newService(logger, threadStore, bindingStore, sharedFiles, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, promptStore, promptClassifier, classifierFastPath, classifierPrune, classifierMaxCandidates, matchWhenEval)
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
	promptStore promptstore.Store,
	promptClassifier contract.PromptClassifier,
	clsFastPath contract.ClassifierFastPathFunc,
	clsPrune contract.ClassifierPruneCandidatesFunc,
	clsMaxCandidates contract.ClassifierMaxCandidatesFunc,
	matchWhenEval contract.MatchWhenEvaluator,
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
		sharedFiles:             sharedFiles,
		sessions:                sessions,
		starter:                 starter,
		promptAssembly:          promptAssembly,
		cfg:                     cfg,
		toolRegistry:            toolRegistry,
		turns:                   turns,
		orchestration:           orchestration,
		bus:                     dispatcher,
		promptStore:             promptStore,
		classifier:              promptClassifier,
		classifierFastPath:      clsFastPath,
		classifierPrune:         clsPrune,
		classifierMaxCandidates: clsMaxCandidates,
		matchWhenEval:           matchWhenEval,
		emitStarted:             contract.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:             contract.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:             contract.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage:        contract.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:           contract.NewEmitter[threaddto.Compacted](dispatcher),
		emitLaunched:            contract.NewEmitter[threaddto.Launched](dispatcher),
		threadAgents:            make(map[string]string),
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
	// P22 P2 thread S2: sessionRecoveryWorker owns the onAgentFailed
	// slow-path (3s reconnect delay + evict + backgroundResumeIfNeeded).
	// Same construction pattern as the other bus-callback workers.
	s.sessionRecoveryWorker = newSessionRecoveryWorker(s, logger)
	return s
}
