package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/memdata"
	nestedpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/nested"
	retrievalpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/retrieval"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// RootManager owns the root memory service dependency for legacy wiring.
type RootManager struct {
	svc Service
}

type promptProviderParams struct {
	fx.In

	Registry           contract.DynamicSectionRegistrar         `optional:"true"`
	ClaudeMdRegistrar  contract.ClaudeMdSourceProviderRegistrar `optional:"true"`
	ClaudeMdProvider   contract.ClaudeMdSourceProvider          `optional:"true"`
	Provider           *MemoryRulesProvider                     `optional:"true"`
	EntrypointProvider *MemoryEntrypointProvider                `optional:"true"`
	ContextProvider    *MemoryContextProvider                   `optional:"true"`
}

type memoryHandlerDeps struct {
	fx.In

	Service             Service                       `optional:"true"`
	Orchestration       contract.OrchestrationService `optional:"true"`
	DAGRuntime          contract.DAGRuntime           `optional:"true"`
	SharedFiles         contract.SharedFileReader     `optional:"true"`
	SharedFilesDeleter  contract.SharedFileDeleter    `optional:"true"`
	SharedFilesUpserter contract.SharedFileUpserter   `optional:"true"`
	Sections            contract.SectionInvalidator   `optional:"true"`
	Logger              *pkglogger.Logger             `optional:"true"`
	DreamExecutor       contract.DreamExecutor        `optional:"true"`
	Dispatcher          *event.Dispatcher             `optional:"true"`
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
	Logger          *pkglogger.Logger           `optional:"true"`
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

// NewMemoryLifecycleHooks 创建线程生命周期里的记忆 hook。
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
	if hooks != nil {
		hooks.feedbackTracker = NewFeedbackTracker(3)
	}
	if hooks != nil {
		hooks.dedupFilter = buildDedupFilter(hooks)
	}
	return hooks
}

func buildDedupFilter(hooks *MemoryLifecycleHooks) *dedup.Filter {
	privateRoot := hooks.rootDir
	scanPrivate := func(typeFilter string) ([]dedup.EntrySnapshot, error) {
		root, err := resolvedStoreRoot(privateRoot, hooks.projectRoot, hooks.autoMemPathOverride)
		if err != nil {
			return nil, err
		}
		return scanEntriesAsSnapshots(root, typeFilter, "private")
	}
	var scanTeam dedup.ScanFunc
	if hooks.team != nil && hooks.team.GetTeamMemPath() != "" {
		teamRoot := hooks.team.GetTeamMemPath()
		scanTeam = func(typeFilter string) ([]dedup.EntrySnapshot, error) {
			return scanEntriesAsSnapshots(teamRoot, typeFilter, "team")
		}
	}
	return dedup.NewFilter(scanPrivate, scanTeam)
}

// scanEntriesAsSnapshots 把扫描到的记忆条目转换成快照。
func scanEntriesAsSnapshots(root, typeFilter, scope string) ([]dedup.EntrySnapshot, error) {
	entries, err := scanMemoryEntries(root)
	if err != nil {
		return nil, err
	}
	snapshots := make([]dedup.EntrySnapshot, 0, len(entries))
	for _, e := range entries {
		t := ""
		if e.Frontmatter.Type != nil {
			t = string(*e.Frontmatter.Type)
		}
		if typeFilter != "" && t != typeFilter {
			continue
		}
		snapshots = append(snapshots, dedup.EntrySnapshot{
			Name:        e.Frontmatter.Name,
			Type:        t,
			Description: e.Frontmatter.Description,
			SearchKeys:  e.Frontmatter.SearchKeys,
			Lang:        e.Frontmatter.Lang,
			Aliases:     e.Frontmatter.Aliases,
			Source:      e.Frontmatter.Source,
			Content:     e.Content,
			Path:        e.FilePath,
			Scope:       scope,
		})
	}
	return snapshots, nil
}

// newMemoryLifecycleHooksWithTeam 创建带团队记忆能力的生命周期 hook。
func newMemoryLifecycleHooksWithTeam(
	cfg *Config,
	team *TeamMemoryManager,
	consolidator *AutoDreamConsolidator,
	logger *pkglogger.Logger,
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
	hooks := &MemoryLifecycleHooks{
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
		locks:               newDiskLockCoordinator(),
	}
	hooks.consolidator.locks = hooks.locks // share process-scoped coordinator
	return hooks
}

func asTeamSyncLifecycle(svc *teampkg.TeamSyncService) teampkg.Lifecycle { return svc }

// provideNestedDependencies 为记忆模块组装内部依赖。
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
				SuppressForOverlay:       snapshot.SuppressForOverlay(),
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
	}
}

func provideTeamMemoryManagerContract(manager *TeamMemoryManager) contract.TeamMemoryManager {
	return manager
}

// Module 把 memory 接到 prompt、thread 事件和 provider 工具。
// 记忆文件怎么写、prompt 怎么看到、provider 怎么读写，分别从这里接出去。
var Module = fx.Module("memory",
	fx.Provide(
		NewConfig,
		provideNestedDependencies,
		provideTeamConfig,
		provideTeamMemoryManagerContract,
		asTeamSyncLifecycle,
		provideMemoryService,
		NewMemoryHandlers,
		NewMemoryRuleEngine,
		NewRulesProvider,
		NewEntrypointProvider,
		NewContextProvider,
		AsTurnContextProvider,
		provideDreamExtractFunc,
		provideAutoDreamConsolidator,
		NewMemoryLifecycleHooks,
		provideAgentMemoryReader,
		provideAgentMemoryWriter,
		NewMemoryExtractor,
		newAutoDreamSchedulerProvider,
		newNestedIngestWorkerProvider,
		newTeamSyncCoordinatorProvider,
		NewMemorySubscribers,
	),
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

// NewRootManager 创建记忆根目录管理器。
func NewRootManager(svc Service) *RootManager {
	return &RootManager{svc: svc}
}

// RootDir 返回当前服务使用的根目录。
func (m *RootManager) RootDir() string {
	if m == nil || m.svc == nil {
		return ""
	}
	return m.svc.RootDir()
}

// EnsureRoot 确保记忆根目录存在且可用。
func (m *RootManager) EnsureRoot(ctx context.Context) error {
	if m == nil || m.svc == nil {
		return errors.New("memory service is nil")
	}
	return m.svc.EnsureRoot(ctx)
}

// NewAutoDreamConsolidator 创建自动 dream 记忆整理器。
func NewAutoDreamConsolidator(extractor *MemoryExtractor) *AutoDreamConsolidator {
	return newAutoDreamConsolidator(extractor, nil)
}

func newAutoDreamConsolidator(extractor *MemoryExtractor, extractFn ExtractFunc) *AutoDreamConsolidator {
	if extractor == nil {
		extractor = NewMemoryExtractor()
	}
	return &AutoDreamConsolidator{extractor: extractor, extractFn: extractFn, locks: newDiskLockCoordinator()}
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

type provideMemoryServiceParams struct {
	fx.In

	Cfg          *Config
	Logger       *pkglogger.Logger
	Consolidator *AutoDreamConsolidator
	Hooks        *MemoryLifecycleHooks
}

func provideMemoryService(p provideMemoryServiceParams) Service {
	return NewService(p.Cfg, p.Logger, p.Consolidator, p.Hooks)
}

// provideAgentMemoryReader 把 memory_read 接到统一的 reader。
// provider 只拿这个接口，不需要知道 memory 文件怎么存。
func provideAgentMemoryReader(hooks *MemoryLifecycleHooks) contract.AgentMemoryReader {
	return hooks
}

// provideAgentMemoryWriter 把 memory_write 接到统一的 writer。
// 写入后的去重、MEMORY.md 刷新和 prompt 更新都还在 hooks 里做。
func provideAgentMemoryWriter(hooks *MemoryLifecycleHooks) contract.AgentMemoryWriter {
	return hooks
}

// NewMemoryHandlers 创建记忆 RPC 和工具处理器。
func NewMemoryHandlers(p memoryHandlerDeps) platformrpc.HandlerMapResult {
	handlers := handler.Map{
		"memory/consolidate": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
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
	return platformrpc.HandlerMapResult{Handlers: handlers}
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
		OnStart: func(context.Context) error {
			if p.Hooks.feedbackTracker != nil {
				p.Hooks.feedbackTracker.LoadFromDir(p.Hooks.rootDir)
			}
			return nil
		},
		OnStop: func(ctx context.Context) error {
			drainDreamTask(ctx, p.Hooks)
			return nil
		},
	})
}

// registerTeamMemoryRuntime 只标记 team memory 是否已可用。
// combined memory prompt 会看这个状态；这里不要启动同步或写文件。
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
