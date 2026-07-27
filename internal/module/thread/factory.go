package thread

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

type threadStateKind string

const (
	threadStateStartKind   threadStateKind = "start"
	threadStateResumeKind  threadStateKind = "resume"
	threadStateForkKind    threadStateKind = "fork"
	threadStateRecoverKind threadStateKind = "recover"
)

type threadStateFields struct {
	RequestedThreadID, PublicThreadID, ProviderThreadID, OwnerThreadID string
	AgentID, ParentAgentID, AgentType, AgentMemoryScope                string
	Provider, CWD, Model, Name, Prompt, RolloutPath, SessionUUID       string
	CodexHome, CodexInstanceKey, CodexModelProvider, AgentKey          string
	ConfigOverride                                                     json.RawMessage
	CreatedAt                                                          int64
	PromptVersionID                                                    *int64
	PendingLaunch                                                      bool
}

type threadBindingRecord struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	RolloutPath        string
	Cwd                string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	Archived           bool
	CreatedAt          int64
	UpdatedAt          int64
	SessionUUID        string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

type threadBindingUpsertParams struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	RolloutPath        string
	SessionUUID        string
	Cwd                string
	ParentAgentID      string
	AgentType          string
	AgentMemoryScope   string
	CreatedAt          int64
	UpdatedAt          int64
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

type threadBindingSessionUUIDUpdate struct {
	SessionUUID string
	UpdatedAt   int64
	AgentID     string
}

type threadBindingProviderThreadIDUpdate struct {
	ProviderThreadID string
	UpdatedAt        int64
	AgentID          string
}

type threadBindingCWDUpdate struct {
	AgentID   string
	Cwd       string
	UpdatedAt int64
}

type threadBindingStorePort interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*threadBindingRecord, error)
	Upsert(ctx context.Context, params threadBindingUpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params threadBindingSessionUUIDUpdate) error
	UpdateProviderThreadID(ctx context.Context, params threadBindingProviderThreadIDUpdate) error
	GetByAgentID(ctx context.Context, agentID string) (*threadBindingRecord, error)
	ListAgentThreadBindings(ctx context.Context) ([]threadBindingRecord, error)
	UpdateAgentCwd(ctx context.Context, params threadBindingCWDUpdate) error
}

type threadConfigRecord struct {
	ThreadID         string
	AgentID          string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Name             string
	Prompt           string
	Model            string
	Cwd              string
	Status           string
	Port             int32
	PID              int32
	CreatedAt        int64
	UpdatedAt        int64
	FinishedAt       *int64
	LastEventType    string
	ErrorMessage     string
	WorkspaceRunKey  string
	OwnerThreadID    string
	ConfigOverride   json.RawMessage
	AgentKey         string
	PromptVersionID  *int64
	PendingLaunch    bool
	ManuallyRenamed  bool
}

type threadConfigStorePort interface {
	GetByThreadID(ctx context.Context, threadID string) (*threadConfigRecord, error)
	ListConfigsByIDs(ctx context.Context, threadIDs []string) ([]threadConfigRecord, error)
}

// PromptCatalog 是 thread/start 路由消费的运行时 prompt 端口。
// 外层 catalog 必须显式报告写能力，builtin-only catalog 可以保持只读。
type PromptCatalog interface {
	ListTemplates(ctx context.Context, filter PromptListFilter) ([]PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	CanInsertPromptVersion() bool
}

// PromptListFilter 描述 Thread 路由的模板过滤条件。
type PromptListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// PromptTemplate 是 Thread 路由使用的 prompt 模板快照。
type PromptTemplate struct {
	ID             int64
	PromptKey      string
	Title          string
	AgentKey       string
	ToolName       string
	PromptText     string
	WhenToUse      string
	Variables      json.RawMessage
	Tags           json.RawMessage
	Enabled        bool
	ManuallyEdited bool
	MatchWhen      json.RawMessage
	Priority       int
	CreatedBy      string
	UpdatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Description    string
}

// PromptTemplateSection 是 Thread 路由转换为基础指令块的 section 快照。
type PromptTemplateSection struct {
	ID                  int64
	TemplateID          int64
	SectionKey          string
	Region              string
	Ordinal             int
	Body                string
	EnableWhen          json.RawMessage
	Enabled             bool
	TriggerType         string
	RecallTopic         string
	TemplatePromptKey   string
	TemplateTitle       string
	TemplateDescription string
	TemplateWhenToUse   string
	TemplateTags        json.RawMessage
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// PromptTemplateVersion 是 Thread 路由写入版本归档的输入。
type PromptTemplateVersion struct {
	ID              int64
	PromptKey       string
	Title           string
	AgentKey        string
	ToolName        string
	PromptText      string
	Variables       json.RawMessage
	Tags            json.RawMessage
	Description     string
	Enabled         bool
	CreatedBy       string
	UpdatedBy       string
	SourceUpdatedAt *time.Time
	CreatedAt       time.Time
	ArchivedAt      time.Time
}

type promptSnapshotRecord struct {
	DisplayName           string
	BaseInstructions      string
	Boundary              *promptBoundaryRecord
	DeveloperInstructions string
	Provider              string
	Version               int
	Hash                  string
	SectionSnapshot       map[string]string
	Generation            uint64
}

type promptBoundaryRecord struct {
	CachedPrefix string
	UncachedTail string
}

// newThreadState 组装 thread store 的状态快照。
// 不同来源的 threadID 优先级在这里统一，避免 start、fork、pending 路径写出互相不兼容的公共 ID。
func newThreadState(kind threadStateKind, fields threadStateFields) threadState {
	displayName := strings.TrimSpace(util.FirstNonEmpty(fields.Name, fields.Prompt))
	state := threadState{
		OwnerThreadID:    fields.OwnerThreadID,
		AgentID:          fields.AgentID,
		ParentAgentID:    strings.TrimSpace(fields.ParentAgentID),
		AgentType:        strings.TrimSpace(fields.AgentType),
		AgentMemoryScope: strings.TrimSpace(fields.AgentMemoryScope),
		Provider:         fields.Provider,
		CWD:              fields.CWD,
		Model:            fields.Model,
		Name:             displayName,
		Prompt:           displayName,
	}
	switch kind {
	case threadStateStartKind:
		state.PublicThreadID = util.FirstNonEmpty(fields.PublicThreadID, fields.AgentID)
	case threadStateForkKind:
		state.PublicThreadID = util.FirstNonEmpty(fields.PublicThreadID, fields.ProviderThreadID, fields.AgentID)
	default:
		state.PublicThreadID = util.FirstNonEmpty(fields.PublicThreadID, fields.RequestedThreadID, fields.AgentID)
	}
	// provider_thread_id 只有 provider 返回真实 UUID 后才写入；启动早期允许为空。
	state.ProviderThreadID = strings.TrimSpace(fields.ProviderThreadID)
	state.RolloutPath = fields.RolloutPath
	state.SessionUUID = fields.SessionUUID
	state.ConfigOverride = clone.RawMessage(fields.ConfigOverride)
	state.CodexHome = strings.TrimSpace(fields.CodexHome)
	state.CodexInstanceKey = strings.TrimSpace(fields.CodexInstanceKey)
	state.CodexModelProvider = strings.TrimSpace(fields.CodexModelProvider)
	state.CreatedAt = firstNonZero(fields.CreatedAt)
	state.AgentKey = strings.TrimSpace(fields.AgentKey)
	state.PromptVersionID = fields.PromptVersionID
	state.PendingLaunch = fields.PendingLaunch
	return state
}

func newBindingUpsertParams(binding threadBindingRecord) threadBindingUpsertParams {
	return threadBindingUpsertParams{
		AgentID:            strings.TrimSpace(binding.AgentID),
		Provider:           strings.TrimSpace(binding.Provider),
		ProviderThreadID:   strings.TrimSpace(binding.ProviderThreadID),
		CodexThreadID:      strings.TrimSpace(binding.CodexThreadID),
		RolloutPath:        strings.TrimSpace(binding.RolloutPath),
		SessionUUID:        strings.TrimSpace(binding.SessionUUID),
		Cwd:                strings.TrimSpace(binding.Cwd),
		ParentAgentID:      strings.TrimSpace(binding.ParentAgentID),
		AgentType:          strings.TrimSpace(binding.AgentType),
		AgentMemoryScope:   strings.TrimSpace(binding.AgentMemoryScope),
		CreatedAt:          binding.CreatedAt,
		UpdatedAt:          binding.UpdatedAt,
		CodexHome:          strings.TrimSpace(binding.CodexHome),
		CodexInstanceKey:   strings.TrimSpace(binding.CodexInstanceKey),
		CodexModelProvider: strings.TrimSpace(binding.CodexModelProvider),
	}
}

// buildBatchOfflineRuntimeConfig 用本地 DTO 合成批量读取的离线 runtime 配置。
// 这里不调用 store DTO 版本，避免 history 业务路径重新依赖 store 类型。
func buildBatchOfflineRuntimeConfig(stored storedThreadConfig, thread *threadConfigRecord, binding *threadBindingRecord) map[string]any {
	cfg := map[string]any{
		"approvalPolicy": offlineApprovalPolicy,
		"toolRouting": map[string]any{
			"mode":                offlineToolMode,
			"routerModel":         "",
			"routerProvider":      offlineToolProvider,
			"routerBaseURL":       "",
			"routerHasAPIKey":     false,
			"confidenceThreshold": 0.65,
			"timeoutSec":          8,
		},
	}
	cfg = mergeRuntimeConfig(cfg, clone.RuntimeConfigMap(stored.Runtime))
	if thread != nil && strings.TrimSpace(thread.Cwd) != "" {
		cfg["cwd"] = strings.TrimSpace(thread.Cwd)
	} else if binding != nil && strings.TrimSpace(binding.Cwd) != "" {
		cfg["cwd"] = strings.TrimSpace(binding.Cwd)
	}
	if value := strings.TrimSpace(stored.Approvals); value != "" {
		cfg["approvalPolicy"] = value
	}
	if value := strings.TrimSpace(stored.Personality); value != "" {
		cfg["personality"] = value
	}
	if value := strings.TrimSpace(stored.PromptKey); value != "" {
		cfg["promptKey"] = value
		cfg["prompt_key"] = value
	}
	if model := util.FirstNonEmpty(stored.Model, batchThreadModel(thread)); model != "" {
		cfg["model"] = model
	}
	return cfg
}

func batchThreadModel(thread *threadConfigRecord) string {
	if thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.Model)
}

func batchThreadConfigRaw(thread *threadConfigRecord) json.RawMessage {
	if thread == nil {
		return nil
	}
	return thread.ConfigOverride
}

func newStartResult(
	req StartRequest,
	publicThreadID, agentID, providerUUID, providerThreadID, effectiveModel, effectiveCWD string,
) StartResult {
	return StartResult{
		ThreadID:        publicThreadID,
		AgentID:         agentID,
		SessionID:       util.FirstNonEmpty(providerUUID, providerThreadID, publicThreadID),
		Status:          "running",
		Model:           effectiveModel,
		Provider:        req.Provider,
		ModelProvider:   req.ModelProvider,
		CWD:             effectiveCWD,
		ApprovalPolicy:  req.ApprovalPolicy,
		AgentKey:        req.AgentKey,
		AgentTitle:      req.AgentTitle,
		PromptKey:       req.PromptKey,
		PromptVersionID: req.PromptVersionID,
		PromptKeyStale:  req.PromptKeyStale,
	}
}

type threadEventKind string

const (
	threadEventStartedKind      threadEventKind = "started"
	threadEventStoppedKind      threadEventKind = "stopped"
	threadEventMessagesPageKind threadEventKind = "messages_page"
	threadEventCompactedKind    threadEventKind = "compacted"
	threadEventLaunchedKind     threadEventKind = "launched"
)

type threadEventFields struct {
	State                            threadState
	AgentID, Status, Reason, Command string
	TotalCount, Pages                int
	BeforeTokens, AfterTokens        int
	Compacted, Estimated             bool
}

// newThreadEvent 根据线程状态字段构造对外事件 DTO。
// threadID 为空时返回 nil，让调用方在发布前自然跳过无效事件。
func newThreadEvent(kind threadEventKind, threadID string, fields threadEventFields) any {
	header := shareddto.EventHeader{Timestamp: time.Now()}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	switch kind {
	case threadEventStartedKind:
		state := fields.State
		return threaddto.Started{
			EventHeader:      header,
			ThreadID:         threadID,
			AgentID:          strings.TrimSpace(state.AgentID),
			Provider:         strings.TrimSpace(state.Provider),
			ProviderThreadID: strings.TrimSpace(state.ProviderThreadID),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
			Name:             strings.TrimSpace(state.Name),
			PendingLaunch:    state.PendingLaunch,
		}
	case threadEventLaunchedKind:
		state := fields.State
		return threaddto.Launched{
			EventHeader:      header,
			ThreadID:         threadID,
			AgentID:          strings.TrimSpace(state.AgentID),
			Provider:         strings.TrimSpace(state.Provider),
			ProviderThreadID: strings.TrimSpace(state.ProviderThreadID),
			CWD:              strings.TrimSpace(state.CWD),
			Model:            strings.TrimSpace(state.Model),
			Name:             strings.TrimSpace(state.Name),
			AgentKey:         strings.TrimSpace(state.AgentKey),
			PromptVersionID:  state.PromptVersionID,
		}
	case threadEventStoppedKind:
		return threaddto.Stopped{
			EventHeader: header,
			ThreadID:    threadID,
			AgentID:     strings.TrimSpace(fields.AgentID),
			Status:      strings.TrimSpace(fields.Status),
			Reason:      strings.TrimSpace(fields.Reason),
		}
	case threadEventMessagesPageKind:
		return threaddto.MessagesPage{
			EventHeader: header,
			ThreadID:    threadID,
			TotalCount:  fields.TotalCount,
			Pages:       fields.Pages,
		}
	case threadEventCompactedKind:
		return threaddto.Compacted{
			EventHeader:  header,
			ThreadID:     threadID,
			Command:      strings.TrimSpace(fields.Command),
			BeforeTokens: fields.BeforeTokens,
			AfterTokens:  fields.AfterTokens,
			Compacted:    fields.Compacted,
			Estimated:    fields.Estimated,
		}
	default:
		return nil
	}
}

// NewService 构造最小 thread 服务。
// 该入口只装配 store、session、starter、turn 清理和 orchestration，适合不需要 prompt assembly 的测试或轻量运行时。
func NewService(
	logger *slog.Logger,
	threadStore ThreadStore,
	bindingStore BindingStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
) Service {
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, nil, nil, nil, nil, nil, nil, nil, nil)
}

// NewServiceWithPromptAssembly 构造带 prompt assembly 的 thread 服务。
// 它额外接入配置和工具 registry，使 thread/start 可以把 prompt、MCP 和工具上下文交给 prompt 模块组装。
func NewServiceWithPromptAssembly(
	logger *slog.Logger,
	threadStore ThreadStore,
	bindingStore BindingStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
) Service {
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, nil, nil, nil, nil, nil)
}

// NewServiceWithPromptAssemblyAndSharedFiles 构造完整 thread 服务。
// 除 prompt assembly 外，它还接入 runtime prompt catalog、match/enable_when 评估器和可选 tracing。
func NewServiceWithPromptAssemblyAndSharedFiles(
	logger *slog.Logger,
	threadStore ThreadStore,
	bindingStore BindingStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptCatalog PromptCatalog,
	matchWhenEval contract.MatchWhenEvaluator,
	enableWhenEval contract.EnableWhenEvaluator,
	tracingOpt ...*platformobs.Service,
) Service {
	var tracing *platformobs.Service
	if len(tracingOpt) > 0 {
		tracing = tracingOpt[0]
	}
	return newService(logger, threadStore, bindingStore, sessions, starter, turns, orchestration, threadEvents, promptAssembly, cfg, toolRegistry, mcpServers, promptCatalog, matchWhenEval, enableWhenEval, tracing)
}

// newService 统一完成 thread service wiring。
// 构造阶段会创建事件 emitter、后台 worker 和进程内缓存；外层构造器只负责选择依赖集合。
func newService(
	logger *slog.Logger,
	threadStore ThreadStore,
	bindingStore BindingStore,
	sessions SessionProvider,
	starter SessionStarter,
	turns contract.TurnThreadCleaner,
	orchestration OrchestrationFacade,
	threadEvents *bus.ThreadEmitters,
	promptAssembly contract.PromptAssemblyService,
	cfg *contract.Config,
	toolRegistry contract.ToolRegistry,
	mcpServers contract.MCPServerConfigProvider,
	promptCatalog PromptCatalog,
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
	archiveStateStore, _ := threadStore.(ArchiveStateStore)
	s := &service{
		logger:                  logger,
		threadStore:             threadStore,
		archiveStateStore:       archiveStateStore,
		bindingStore:            bindingStore,
		sessions:                sessions,
		starter:                 starter,
		promptAssembly:          promptAssembly,
		cfg:                     cfg,
		toolRegistry:            toolRegistry,
		mcpServers:              mcpServers,
		turns:                   turns,
		orchestration:           orchestration,
		sessionGenerationBinder: sessionGenerationBinderFromOrchestration(orchestration),
		tracing:                 tracing,
		bus:                     dispatcher,
		promptCatalog:           promptCatalog,
		matchWhenEval:           matchWhenEval,
		enableWhenEval:          enableWhenEval,
		emitStarted:             contract.NewEmitter[threaddto.Started](dispatcher),
		emitStopped:             contract.NewEmitter[threaddto.Stopped](dispatcher),
		emitUpdated:             contract.NewEmitter[threaddto.Updated](dispatcher),
		emitMessagesPage:        contract.NewEmitter[threaddto.MessagesPage](dispatcher),
		emitCompacted:           contract.NewEmitter[threaddto.Compacted](dispatcher),
		emitLaunched:            contract.NewEmitter[threaddto.Launched](dispatcher),
		threadAgents:            make(map[string]string),
		reconnectDelay:          sessionRecoveryReconnectDelay,
	}
	// Workers live beside service methods they call; bus callbacks only enqueue.
	s.agentLaunchedWorker = newAgentLaunchedWorker(s, logger)
	s.sessionRecoveryWorker = newSessionRecoveryWorker(s, logger)
	return s
}

// sessionGenerationBinderFromOrchestration 从兼容 facade 里提取独立 generation 绑定端口。
func sessionGenerationBinderFromOrchestration(orchestration OrchestrationFacade) SessionGenerationBinder {
	binder, _ := orchestration.(SessionGenerationBinder)
	return binder
}
