package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type RootManager struct {
	svc Service
}

type memoryHookParams struct {
	fx.In

	Lifecycle       fx.Lifecycle
	Dispatcher      *event.Dispatcher      `optional:"true"`
	Hooks           *MemoryLifecycleHooks  `optional:"true"`
	ContextProvider *MemoryContextProvider `optional:"true"`
	NestedRuntime   *NestedRuntime         `optional:"true"`
}

type historySource interface {
	ReadHistory(ctx context.Context, threadID string, limit int) ([]providerdto.Message, error)
}

type threadMetadataStore interface {
	GetByThreadID(ctx context.Context, threadID string) (*threadstore.Thread, error)
}

type sectionInvalidator interface {
	InvalidateSections(reason prompt.InvalidateReason, names ...string) uint64
}

type memoryLifecycleHookParams struct {
	fx.In

	Config          *Config                   `optional:"true"`
	Consolidator    *AutoDreamConsolidator    `optional:"true"`
	Logger          *slog.Logger              `optional:"true"`
	Threads         historySource             `optional:"true"`
	ThreadStore     threadMetadataStore       `optional:"true"`
	Sections        prompt.SectionInvalidator `optional:"true"`
	Extractor       *MemoryExtractor          `optional:"true"`
	ManifestBuilder *ManifestBuilder          `optional:"true"`
}

func NewMemoryLifecycleHooks(p memoryLifecycleHookParams) *MemoryLifecycleHooks {
	return newMemoryLifecycleHooks(
		p.Config,
		p.Consolidator,
		p.Logger,
		p.Threads,
		p.ThreadStore,
		p.Sections,
		p.Extractor,
		p.ManifestBuilder,
	)
}

func newMemoryLifecycleHooks(
	cfg *Config,
	consolidator *AutoDreamConsolidator,
	logger *slog.Logger,
	threads historySource,
	threadStore threadMetadataStore,
	sections sectionInvalidator,
	extractor *MemoryExtractor,
	manifestBuilder *ManifestBuilder,
) *MemoryLifecycleHooks {
	if cfg == nil {
		cfg = &Config{}
	}
	if consolidator == nil {
		consolidator = NewAutoDreamConsolidator(nil)
	}
	consolidator.cfg = memoryConfig(cfg)
	if extractor == nil {
		extractor = NewMemoryExtractor()
	}
	if manifestBuilder == nil {
		manifestBuilder = NewManifestBuilder()
	}
	return &MemoryLifecycleHooks{
		cfg:                 memoryConfig(cfg),
		enabled:             ResolveMemoryGate(contract.BuildCtx{}, cfg).AutoEnabled,
		extractOnStop:       cfg.ExtractOnStop,
		rootDir:             strings.TrimSpace(cfg.RootDir),
		projectRoot:         strings.TrimSpace(cfg.ProjectRoot),
		autoMemPathOverride: strings.TrimSpace(cfg.AutoMemPathOverride),
		consolidator:        consolidator,
		extractFn:           nil,
		extractor:           extractor,
		manifestBuilder:     manifestBuilder,
		threads:             threads,
		threadStore:         threadStore,
		sections:            sections,
		logger:              logger,
		states:              map[string]*ExtractionState{},
		activeTurns:         map[string]string{},
		callTurns:           map[string]toolCallScope{},
		turnWrites:          map[string]map[string]struct{}{},
	}
}

var Module = fx.Module("memory",
	fx.Provide(
		NewConfig,
		NewService,
		NewAgentMemoryManager,
		NewTeamMemoryManager,
		NewTeamMemoryGuard,
		NewNestedRuntime,
		NewClaudeMdSourcesProvider,
		NewMemoryRuleEngine,
		NewRulesProvider,
		NewAgentMemoryPromptProvider,
		NewContextProvider,
		NewAutoDreamConsolidator,
		NewMemoryLifecycleHooks,
		NewMemoryExtractor,
	),
	fx.Invoke(registerLifecycle, registerPromptProviders, registerMemoryHooks),
)

func NewRootManager(svc Service) *RootManager {
	return &RootManager{svc: svc}
}

func (m *RootManager) RootDir() string {
	if m == nil || m.svc == nil {
		return ""
	}
	return m.svc.RootDir()
}

func (m *RootManager) EnsureRoot(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return errors.New("memory service is nil")
	}
	return m.svc.EnsureRoot(ctx)
}

func NewAutoDreamConsolidator(extractor *MemoryExtractor) *AutoDreamConsolidator {
	if extractor == nil {
		extractor = NewMemoryExtractor()
	}
	return &AutoDreamConsolidator{extractor: extractor}
}

func registerLifecycle(lc fx.Lifecycle, svc Service) {
	if svc == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			return svc.EnsureRoot(ctx)
		},
	})
}

func registerMemoryHooks(p memoryHookParams) {
	if p.Dispatcher == nil {
		return
	}
	var cancels []context.CancelFunc
	appendCancel := func(cancel context.CancelFunc) {
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			registerLifecycleSubscriptions(p, appendCancel)
			return nil
		},
		OnStop: func(context.Context) error {
			if p.Hooks != nil {
				p.Hooks.killDreamTask()
			}
			cancelSubscriptions(cancels)
			cancels = nil
			return nil
		},
	})
}

func registerLifecycleSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	registerThreadHookSubscriptions(p, appendCancel)
	registerBackgroundExtractionSubscriptions(p, appendCancel)
	registerContextProviderSubscriptions(p, appendCancel)
	registerAutoDreamSubscriptions(p, appendCancel)
}

func registerBackgroundExtractionSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.Hooks == nil || !p.Hooks.extractOnStop {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStarted) {
		p.Hooks.onTurnStarted(ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInterrupted) {
		p.Hooks.onTurnTerminated(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStalled) {
		p.Hooks.onTurnTerminated(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolCallBegin) {
		p.Hooks.onToolCallBegin(ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolDiffUpdated) {
		p.Hooks.onToolDiffUpdated(ev)
	}, pkglogger.Get()))
}

func registerThreadHookSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.NestedRuntime != nil {
		appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
			p.NestedRuntime.OnThreadStart(ev.ThreadID)
		}, pkglogger.Get()))
		appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolCallEnd) {
			p.NestedRuntime.AddToolReadResult(ev.ThreadID, ev.ToolName, ev.Result, ev.PersistedPath)
		}, pkglogger.Get()))
	}
	if p.Hooks == nil || !p.Hooks.enabled {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
		p.Hooks.onThreadStart(context.Background(), ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		p.Hooks.onTurnCompleted(context.Background(), ev)
	}, pkglogger.Get()))
}

func registerContextProviderSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	if p.ContextProvider == nil {
		return
	}
	registerTurnTerminationSubscriptions(p, appendCancel)
}

func registerTurnTerminationSubscriptions(p memoryHookParams, appendCancel func(context.CancelFunc)) {
	terminate := func(threadID, turnID string) {
		p.ContextProvider.onTurnTerminated(threadID, turnID)
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		terminate(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInterrupted) {
		terminate(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnStalled) {
		terminate(ev.ThreadID, ev.TurnID)
	}, pkglogger.Get()))
}

func cancelSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}
