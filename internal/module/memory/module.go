// Package memory 实现持久化记忆系统：从 turn 事件中提取记忆、写入磁盘、注入 prompt，
// 并通过 auto-dream 自动整理。支持 standard、combined（team）和 kairos 三种模式。
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	nestedpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/nested"
	retrievalpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/retrieval"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/sharedfileport"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// RootManager 向外暴露 memory 根目录准备能力。
// 它只委托 Service，不解析 provider 或 UI 请求，避免根目录生命周期散落到调用方。
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

type memoryHandlerFxDeps struct {
	fx.In

	Service            Service                     `optional:"true"`
	DAGRuntime         contract.DAGRuntime         `optional:"true"`
	SharedFiles        sharedfilestore.Reader      `optional:"true"`
	SharedFilesDeleter sharedfilestore.Deleter     `optional:"true"`
	Sections           contract.SectionInvalidator `optional:"true"`
	Logger             *slog.Logger                `optional:"true"`
	DreamExecutor      contract.DreamExecutor      `optional:"true"`
	Dispatcher         *event.Dispatcher           `optional:"true"`
}

type memoryHandlerDeps struct {
	Service            Service
	DAGRuntime         contract.DAGRuntime
	SharedFiles        sharedfileport.Reader
	SharedFilesDeleter sharedfileport.Deleter
	Sections           contract.SectionInvalidator
	Logger             *slog.Logger
	DreamExecutor      contract.DreamExecutor
	Dispatcher         *event.Dispatcher
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

// newMemoryLifecycleHooksWithTeam 创建带团队记忆能力的生命周期 hook。
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
		newMemoryHandlerDeps,
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
		provideMemoryExtractionDrainer,
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
	fx.Invoke(validateMemoryModuleRoot, registerTeamMemoryRuntime, registerLifecycle, registerPromptProviders, bindMemoryDrainShutdown),
)

// validateMemoryModuleRoot 在 fx 构造期校验启用状态下的记忆根目录，避免 provider 之后再静默退回空根。
func validateMemoryModuleRoot(cfg *Config) error {
	if !shouldValidateMemoryRoot(cfg) {
		return nil
	}
	cfg = memoryConfig(cfg)
	_, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, configuredAutoMemPathOverride(cfg))
	return err
}

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
	return func(ctx context.Context, prompt string) (string, error) {
		options := contract.DreamOptions{RuntimePolicy: contract.StrictDreamRuntimePolicy()}
		if withOptions, ok := p.Executor.(contract.DreamExecutorWithOptions); ok {
			return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
		}
		return p.Executor.ExecuteDream(ctx, prompt)
	}
}

func provideAutoDreamConsolidator(extractor *MemoryExtractor, dreamExtractFn ExtractFunc) *AutoDreamConsolidator {
	return newAutoDreamConsolidator(extractor, dreamExtractFn)
}

type provideMemoryServiceParams struct {
	fx.In

	Cfg          *Config
	Logger       *slog.Logger
	Consolidator *AutoDreamConsolidator
	Hooks        *MemoryLifecycleHooks
}

func provideMemoryService(p provideMemoryServiceParams) Service {
	return NewService(p.Cfg, p.Logger, p.Consolidator, p.Hooks)
}

// newMemoryHandlerDeps 把 Fx 图里的 store 实现收束成 contract 端口。
// 这样 UI/RPC handler 不再直接持有 store 包接口或 DTO。
func newMemoryHandlerDeps(p memoryHandlerFxDeps) memoryHandlerDeps {
	return memoryHandlerDeps{
		Service:            p.Service,
		DAGRuntime:         p.DAGRuntime,
		SharedFiles:        adaptSharedFileReader(p.SharedFiles),
		SharedFilesDeleter: adaptSharedFileDeleter(p.SharedFilesDeleter),
		Sections:           p.Sections,
		Logger:             p.Logger,
		DreamExecutor:      p.DreamExecutor,
		Dispatcher:         p.Dispatcher,
	}
}

type sharedFileReaderAdapter struct {
	reader sharedfilestore.Reader
}

func adaptSharedFileReader(reader sharedfilestore.Reader) sharedfileport.Reader {
	if reader == nil {
		return nil
	}
	return sharedFileReaderAdapter{reader: reader}
}

// Get 读取单个 shared file，并把 store DTO 转成模块可消费的 port DTO。
func (a sharedFileReaderAdapter) Get(ctx context.Context, path string) (*sharedfileport.File, error) {
	file, err := a.reader.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, errors.New("shared file store returned nil file")
	}
	converted := toSharedFilePortFile(*file)
	return &converted, nil
}

// List 列出 shared file，并在装配边界完成 filter 和结果 DTO 转换。
func (a sharedFileReaderAdapter) List(ctx context.Context, filter sharedfileport.ListFilter) ([]sharedfileport.File, error) {
	files, err := a.reader.List(ctx, sharedfilestore.ListFilter{
		Prefix: filter.Prefix,
		Limit:  filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]sharedfileport.File, 0, len(files))
	for _, file := range files {
		out = append(out, toSharedFilePortFile(file))
	}
	return out, nil
}

type sharedFileDeleterAdapter struct {
	deleter sharedfilestore.Deleter
}

func adaptSharedFileDeleter(deleter sharedfilestore.Deleter) sharedfileport.Deleter {
	if deleter == nil {
		return nil
	}
	return sharedFileDeleterAdapter{deleter: deleter}
}

// Delete 删除指定 shared file，保留 store 的行数与错误语义。
func (a sharedFileDeleterAdapter) Delete(ctx context.Context, path string) (int64, error) {
	return a.deleter.Delete(ctx, path)
}

func toSharedFilePortFile(file sharedfilestore.SharedFile) sharedfileport.File {
	return sharedfileport.File{
		Path:      file.Path,
		Content:   file.Content,
		UpdatedBy: file.UpdatedBy,
		CreatedAt: file.CreatedAt,
		UpdatedAt: file.UpdatedAt,
	}
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

// provideMemoryExtractionDrainer 把 runtime 停止前的记忆抽取 drain 接到 app 根图。
func provideMemoryExtractionDrainer(hooks *MemoryLifecycleHooks) contract.MemoryExtractionDrainer {
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
	maps.Copy(handlers, registerUIMemoryHandlers(p))
	maps.Copy(handlers, registerUIMemoryMutationHandlers(p))
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
