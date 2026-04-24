package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	memagent "github.com/anthropic-ai/super-agent-v3/internal/module/memory/agent"
	nestedpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/nested"
	retrievalpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/retrieval"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type RootManager struct {
	svc Service
}

type promptProviderParams struct {
	fx.In

	Registry          contract.DynamicSectionRegistrar         `optional:"true"`
	ClaudeMdRegistrar contract.ClaudeMdSourceProviderRegistrar `optional:"true"`
	ClaudeMdProvider  contract.ClaudeMdSourceProvider          `optional:"true"`
	Provider          *MemoryRulesProvider                     `optional:"true"`
	AgentProvider     *memagent.PromptProvider                 `optional:"true"`
	ContextProvider   *MemoryContextProvider                   `optional:"true"`
}

type memoryHandlerDeps struct {
	fx.In

	Service            Service                     `optional:"true"`
	SharedFiles        sharedfilestore.Reader      `optional:"true"`
	SharedFilesDeleter sharedfilestore.Deleter     `optional:"true"`
	Sections           contract.SectionInvalidator `optional:"true"`
}

type historySource interface {
	ReadHistory(ctx context.Context, threadID string, limit int) ([]providerdto.Message, error)
}

type threadMetadataStore = contract.ThreadMetadataStore

type sectionInvalidator interface {
	InvalidateSections(reason contract.InvalidateReason, names ...string) uint64
}

type memorySubscriberParams struct {
	fx.In

	Hooks           *MemoryLifecycleHooks    `optional:"true"`
	ContextProvider *MemoryContextProvider   `optional:"true"`
	NestedRuntime   *nestedpkg.NestedRuntime `optional:"true"`
	ThreadStore     threadMetadataStore      `optional:"true"`
	TeamSync        teampkg.Lifecycle        `optional:"true"`
}

type autoDreamSchedulerProviderParams struct {
	fx.In

	Hooks *MemoryLifecycleHooks `optional:"true"`
}

type nestedIngestWorkerProviderParams struct {
	fx.In

	NestedRuntime *nestedpkg.NestedRuntime `optional:"true"`
}

type teamSyncCoordinatorProviderParams struct {
	fx.In

	TeamSync    teampkg.Lifecycle   `optional:"true"`
	ThreadStore threadMetadataStore `optional:"true"`
}

type memorySubscriptionDeps struct {
	Dispatcher      *event.Dispatcher
	Hooks           *MemoryLifecycleHooks
	ContextProvider *MemoryContextProvider
	NestedRuntime   *nestedpkg.NestedRuntime
	ThreadStore     threadMetadataStore
	TeamSync        teampkg.Lifecycle
}

type memoryLifecycleHookParams struct {
	fx.In

	Config          *Config                     `optional:"true"`
	Team            *TeamMemoryManager          `optional:"true"`
	Consolidator    *AutoDreamConsolidator      `optional:"true"`
	DreamExtractFn  ExtractFunc                 `optional:"true"`
	Logger          *slog.Logger                `optional:"true"`
	Threads         historySource               `optional:"true"`
	ThreadStore     threadMetadataStore         `optional:"true"`
	Sections        contract.SectionInvalidator `optional:"true"`
	Extractor       *MemoryExtractor            `optional:"true"`
	ManifestBuilder *ManifestBuilder            `optional:"true"`
}

type dreamExtractParams struct {
	fx.In

	Executor contract.DreamExecutor `optional:"true"`
}

func NewMemoryLifecycleHooks(p memoryLifecycleHookParams) *MemoryLifecycleHooks {
	hooks := newMemoryLifecycleHooksWithTeam(
		p.Config,
		p.Team,
		p.Consolidator,
		p.Logger,
		p.Threads,
		p.ThreadStore,
		p.Sections,
		p.Extractor,
		p.ManifestBuilder,
	)
	if hooks != nil && hooks.consolidator != nil && p.DreamExtractFn != nil {
		hooks.consolidator.extractFn = p.DreamExtractFn
	}
	return hooks
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
	return newMemoryLifecycleHooksWithTeam(cfg, nil, consolidator, logger, threads, threadStore, sections, extractor, manifestBuilder)
}

func newMemoryLifecycleHooksWithTeam(
	cfg *Config,
	team *TeamMemoryManager,
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
		team:                team,
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

func asTeamSyncLifecycle(svc *teampkg.TeamSyncService) teampkg.Lifecycle { return svc }

func provideNestedDependencies(cfg *Config) nestedpkg.Dependencies {
	cfg = memoryConfig(cfg)
	return nestedpkg.Dependencies{
		NestedEnabled: cfg.NestedMemory.Enabled,
		Gate: func(buildCtx contract.BuildCtx) nestedpkg.GateSnapshot {
			snapshot := ResolveMemoryGate(buildCtx, cfg)
			return nestedpkg.GateSnapshot{
				BareMode:                 snapshot.BareMode,
				HasAdditionalDirsForBare: snapshot.HasAdditionalDirsForBare,
				DisableClaudeMds:         snapshot.DisableClaudeMds,
				SkipProjectLocalClaudeMd: snapshot.SkipProjectLocalClaudeMd,
				InjectMemoryIndex:        snapshot.InjectMemoryIndex,
				InjectTeamMemIndex:       snapshot.InjectTeamMemIndex,
			}
		},
		AutoMemRoot: func(buildCtx contract.BuildCtx) string {
			projectRoot := strings.TrimSpace(buildCtx.GitRoot)
			if projectRoot == "" {
				projectRoot = strings.TrimSpace(buildCtx.CWD)
			}
			root, err := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
			if err != nil {
				return ""
			}
			cleaned, err := shared.CleanAbsolutePath(root)
			if err != nil {
				return ""
			}
			return cleaned
		},
		TeamRoot: func(buildCtx contract.BuildCtx) string {
			root, err := configuredTeamMemRoot(cfg, buildCtx)
			if err != nil {
				return ""
			}
			cleaned, err := shared.CleanAbsolutePath(root)
			if err != nil {
				return ""
			}
			return cleaned
		},
		IsAgentMemoryPath: func(path string) bool {
			return IsAgentMemoryPath(cfg, path)
		},
	}
}

func provideTeamMemoryManagerContract(manager *TeamMemoryManager) contract.TeamMemoryManager {
	return manager
}

var Module = fx.Module("memory",
	fx.Provide(
		NewConfig,
		provideAgentMemoryConfig,
		provideAgentMemoryPathHelper,
		provideAgentMemoryPromptBuilder,
		provideAgentMemoryGateResolver,
		provideNestedDependencies,
		provideTeamConfig,
		provideTeamMemoryManagerContract,
		asTeamSyncLifecycle,
		provideMemoryService,
		NewMemoryHandlers,
		NewMemoryRuleEngine,
		NewRulesProvider,
		NewContextProvider,
		AsTurnContextProvider,
		provideDreamExtractFunc,
		provideAutoDreamConsolidator,
		NewMemoryLifecycleHooks,
		NewMemoryExtractor,
		newAutoDreamSchedulerProvider,
		newNestedIngestWorkerProvider,
		newTeamSyncCoordinatorProvider,
		NewMemorySubscribers,
	),
	fx.Options(memagent.Module),
	fx.Options(nestedpkg.Module),
	fx.Options(retrievalpkg.Module),
	fx.Options(teampkg.Module),
	fx.Provide(
		fx.Annotate(autoDreamSchedulerAsRunner, fx.ResultTags(`group:"runners"`)),
		fx.Annotate(nestedIngestWorkerAsRunner, fx.ResultTags(`group:"runners"`)),
		fx.Annotate(teamSyncCoordinatorAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerTeamMemoryRuntime, registerLifecycle, registerPromptProviders, bindMemoryDrainShutdown),
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
	return newAutoDreamConsolidator(extractor, nil)
}

func newAutoDreamConsolidator(extractor *MemoryExtractor, extractFn ExtractFunc) *AutoDreamConsolidator {
	if extractor == nil {
		extractor = NewMemoryExtractor()
	}
	return &AutoDreamConsolidator{extractor: extractor, extractFn: extractFn}
}

func provideDreamExtractFunc(p dreamExtractParams) ExtractFunc {
	if p.Executor == nil {
		return nil
	}
	return p.Executor.ExecuteDream
}

func provideAutoDreamConsolidator(extractor *MemoryExtractor, dreamExtractFn ExtractFunc) *AutoDreamConsolidator {
	return newAutoDreamConsolidator(extractor, dreamExtractFn)
}

func provideMemoryService(
	cfg *Config,
	logger *slog.Logger,
	consolidator *AutoDreamConsolidator,
	hooks *MemoryLifecycleHooks,
) Service {
	return NewService(cfg, logger, consolidator, hooks)
}

func NewMemoryHandlers(p memoryHandlerDeps) rpc.HandlerMapResult {
	handlers := handler.Map{
		"memory/consolidate": rpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			if err := p.Service.RunConsolidation(ctx); err != nil {
				return nil, err
			}
			return map[string]any{"status": "completed"}, nil
		}),
	}
	for name, item := range registerUIMemoryHandlers(p) {
		handlers[name] = item
	}
	for name, item := range registerUIMemoryMutationHandlers(p) {
		handlers[name] = item
	}
	return rpc.HandlerMapResult{Handlers: handlers}
}

func buildConsolidationRuntimeContext(source string, sessionsSinceLast int, lastSuccess time.Time, threadID string) string {
	source = strings.TrimSpace(source)
	threadID = strings.TrimSpace(threadID)
	if source == "" {
		source = "manual consolidation request"
	}
	if sessionsSinceLast < 0 {
		sessionsSinceLast = 0
	}
	lines := []string{
		"Execution source: " + source + ".",
		"The runtime is read-only during consolidation; return JSON memories only and let the host apply writes.",
		fmt.Sprintf("Sessions since last consolidation: %d.", sessionsSinceLast),
	}
	if threadID != "" {
		lines = append(lines, "Triggering thread: "+threadID+".")
	}
	if lastSuccess.IsZero() {
		lines = append(lines, "No successful consolidation has been recorded yet.")
	} else {
		lines = append(lines, "Last successful consolidation: "+lastSuccess.UTC().Format(time.RFC3339)+".")
	}
	return renderSection("### Runtime context — execution envelope", lines)
}

func appendConsolidationRuntimeContext(promptText, runtimeContext string) string {
	promptText = strings.TrimSpace(promptText)
	runtimeContext = strings.TrimSpace(runtimeContext)
	switch {
	case promptText == "":
		return runtimeContext
	case runtimeContext == "":
		return promptText
	default:
		return promptText + "\n\n" + runtimeContext
	}
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

type memoryDrainShutdownParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Hooks     *MemoryLifecycleHooks `optional:"true"`
}

func bindMemoryDrainShutdown(p memoryDrainShutdownParams) {
	if p.Hooks == nil {
		return
	}
	p.Lifecycle.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			drainDreamTask(ctx, p.Hooks)
			return nil
		},
	})
}

func registerTeamMemoryRuntime(lc fx.Lifecycle) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			setTeamMemoryRuntimeReady(true)
			return nil
		},
		OnStop: func(context.Context) error {
			setTeamMemoryRuntimeReady(false)
			return nil
		},
	})
}

// drainMemoryHooks preserves the legacy drain order for focused tests and
// fallback callers. Production ownership is split: scheduler / nested /
// teamSync stop through their RunnerModule adapters, while the legacy dream
// task is closed by bindMemoryDrainShutdown during resource shutdown.
func drainMemoryHooks(ctx context.Context, scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, hooks *MemoryLifecycleHooks) {
	drainAutoDreamScheduler(ctx, scheduler)
	drainNestedIngestWorker(ctx, nested)
	drainTeamSyncCoordinator(ctx, teamSync)
	drainDreamTask(ctx, hooks)
}

func drainTeamSyncCoordinator(ctx context.Context, coordinator *teamSyncCoordinator) {
	if coordinator == nil {
		return
	}
	if err := coordinator.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: team sync coordinator drain failed", "error", err)
	}
}

func drainAutoDreamScheduler(ctx context.Context, scheduler *autoDreamScheduler) {
	if scheduler == nil {
		return
	}
	if err := scheduler.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: auto-dream scheduler drain failed", "error", err)
	}
}

func drainNestedIngestWorker(ctx context.Context, nested *nestedIngestWorker) {
	if nested == nil {
		return
	}
	if err := nested.Stop(ctx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: nested ingest worker drain failed", "error", err)
	}
}

func drainDreamTask(ctx context.Context, hooks *MemoryLifecycleHooks) {
	if hooks == nil {
		return
	}
	hooks.killDreamTask()
	waitCtx := ctx
	if waitCtx == nil {
		waitCtx = context.Background()
	}
	if err := hooks.waitDreamTask(waitCtx); err != nil && !errors.Is(err, context.Canceled) {
		pkglogger.Get().Warn("memory: dream task drain failed", "error", err)
	}
}

func registerLifecycleSubscriptions(p memorySubscriptionDeps, scheduler *autoDreamScheduler, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, appendCancel func(context.CancelFunc)) {
	registerThreadHookSubscriptions(p, nested, teamSync, appendCancel)
	registerBackgroundExtractionSubscriptions(p, appendCancel)
	registerContextProviderSubscriptions(p, appendCancel)
	registerAutoDreamSubscriptions(p, scheduler, appendCancel)
}

func registerBackgroundExtractionSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
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

func registerThreadHookSubscriptions(p memorySubscriptionDeps, nested *nestedIngestWorker, teamSync *teamSyncCoordinator, appendCancel func(context.CancelFunc)) {
	if p.NestedRuntime != nil {
		appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
			p.NestedRuntime.OnThreadStart(ev.ThreadID)
		}, pkglogger.Get()))
		// P22 P2 Finding 10: the ToolCallEnd callback must stay off the
		// synchronous nested-read slow-path; nestedIngestWorker owns the
		// lossless pending-set + wake-signal and invokes AddToolReadResult
		// (which os.ReadFile's the persisted result) on its own goroutine.
		if nested != nil {
			appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev tooldto.ToolCallEnd) {
				nested.Enqueue(ev.ThreadID, ev.ToolName, ev.Result, ev.PersistedPath)
			}, pkglogger.Get()))
		}
	}
	if p.TeamSync != nil {
		registerTeamSyncSubscriptions(p, teamSync, appendCancel)
	}
	if p.Hooks == nil || !p.Hooks.enabled {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
		// P22: callback only updates in-memory state (no I/O); ctx unused by callee.
		p.Hooks.onThreadStart(context.Background(), ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnInputReceived) {
		// P22: callback only updates in-memory state (no I/O); ctx unused by callee.
		p.Hooks.onTurnInputReceived(context.Background(), ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev turndto.TurnCompleted) {
		// P22: callback only updates in-memory state (no I/O); ctx unused by callee.
		p.Hooks.onTurnCompleted(context.Background(), ev)
	}, pkglogger.Get()))
}

// registerTeamSyncSubscriptions is the P22 P2 Finding 5/6 boundary: the bus
// callback does nothing more than forward the event to the
// teamSyncCoordinator. Every slow-path bit (ThreadStore.GetByThreadID,
// git resolveRepoSlug, remote pull/push, fsnotify watcher spawn) runs on
// the coordinator's worker goroutine, not on the dispatcher callback.
func registerTeamSyncSubscriptions(p memorySubscriptionDeps, coordinator *teamSyncCoordinator, appendCancel func(context.CancelFunc)) {
	if coordinator == nil {
		return
	}
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Started) {
		coordinator.EnqueueStart(ev)
	}, pkglogger.Get()))
	appendCancel(bus.ResilientSubscribe(p.Dispatcher, func(ev threaddto.Stopped) {
		coordinator.EnqueueStop(ev)
	}, pkglogger.Get()))
}

func registerContextProviderSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
	if p.ContextProvider == nil {
		return
	}
	registerTurnTerminationSubscriptions(p, appendCancel)
}

func registerTurnTerminationSubscriptions(p memorySubscriptionDeps, appendCancel func(context.CancelFunc)) {
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
