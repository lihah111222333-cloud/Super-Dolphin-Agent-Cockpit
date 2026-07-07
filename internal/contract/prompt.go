package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

// PromptRegion 复用 provider DTO 中的 prompt 分区标记，保证 contract 与 provider wire 形状一致。
type PromptRegion = dto.PromptRegion

const (
	// PromptRegionStatic 表示可进入缓存前缀的稳定 prompt 片段。
	PromptRegionStatic PromptRegion = dto.PromptRegionStatic
	// PromptRegionDynamic 表示每轮需要重新拼接的动态 prompt 片段。
	PromptRegionDynamic PromptRegion = dto.PromptRegionDynamic
)

// MCPSnapshot 是 prompt 拼装时看到的 MCP 服务、工具和指令快照。
// 该结构只承载已解析结果，调用方负责在进入 prompt 边界前完成发现和过滤。
type MCPSnapshot struct {
	Servers                  []string
	Tools                    []string
	Instructions             map[string]string
	ServerConfigs            map[string]MCPServerConfig
	InstructionsDeltaEnabled bool
	InstructionAttachments   []MCPAttachmentRef
}

// DatasourceDocument 是数据源文件落库后的可检索内容。
type DatasourceDocument struct {
	WorkspaceRoot string `json:"workspaceRoot"`
	Name          string `json:"name"`
	Extension     string `json:"extension"`
	Size          int64  `json:"size"`
	StoredPath    string `json:"storedPath"`
	Content       string `json:"content"`
}

// UpsertDatasourceDocumentParams 是新增或覆盖数据源文档时需要的字段。
type UpsertDatasourceDocumentParams struct {
	WorkspaceRoot string
	Name          string
	Extension     string
	Size          int64
	StoredPath    string
	Content       string
}

// DatasourceDocumentStore 只暴露数据源服务需要的文档持久化能力。
type DatasourceDocumentStore interface {
	UpsertDocument(context.Context, UpsertDatasourceDocumentParams) error
	ListDocuments(context.Context, string) ([]DatasourceDocument, error)
	ListPromptDocuments(context.Context, string, int, int64, int64) ([]DatasourceDocument, error)
	DeleteDocument(context.Context, string, string) error
}

// MCPAttachmentRef 描述 MCP 指令附件的名称与 URI，prompt 层只透传引用不读取正文。
type MCPAttachmentRef struct {
	Name string
	URI  string
}

// OutputStyleConfig 保存 provider 输出风格的提示词和来源信息。
// KeepCodingInstructions 为指针是为了区分未设置与显式关闭。
type OutputStyleConfig struct {
	Name                   string
	Description            string
	Prompt                 string
	Source                 string
	KeepCodingInstructions *bool
}

// BuildCtx 是 prompt 拼装的主上下文，汇总工作区、provider、工具、MCP 和会话状态。
// 字段来自多个模块，新增字段时要确认快照缓存、恢复和动态 section 是否都能看到同一份数据。
type BuildCtx struct {
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	Provider                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	ClaudeMdExcludes             []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
	Summary                      string
	OutputStyleConfig            *OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *FRCConfig
	KeepCodingInstructions       *bool
	// LaunchSkill* 字段只承载启动请求中的技能选择元数据。
	// 生产技能正文由 provider 原生镜像发现，prompt assembler 不通过这里注入技能内容。
	LaunchSkillNames  []string
	LaunchSkillRefs   []dto.SkillRef
	ForceLaunchSkills bool
	// SuppressedTools 是被 SkillMeta.ReplacesNative 声明替代的原生工具名列表。
	// prompt assembler 在 tool_preferences section 中渲染为 "Do NOT use..." 指令，
	// 引导所有模型优先使用项目 MCP 等价工具。
	SuppressedTools []string
}

// ClaudeMdSource 是从 CLAUDE.md / AGENTS.md 等规则文件解析出的 prompt 来源。
// Origin、RuleScope 和 Digest 用于后续去重、条件规则判断和缓存失效。
type ClaudeMdSource struct {
	Path        string
	Content     string
	Type        string
	Description string
	Origin      string
	Conditional bool
	Globs       []string
	BaseDir     string
	RuleScope   string
	Digest      string
}

// ResolvedPromptSection 复用 provider DTO 的已解析 section 形状。
// contract 层只重新导出类型别名，避免 prompt 组装结果在跨模块传递时重复定义。
type ResolvedPromptSection = dto.ResolvedPromptSection

// SystemContext 复用 provider DTO 的系统上下文快照。
// 调用方通过 contract 包依赖该别名，避免直接耦合 provider 子包。
type SystemContext = dto.SystemContext

// PromptAssemblyBoundary 复用 provider DTO 的缓存边界描述。
// start/resume 快照通过该别名保持 provider wire 和 store 快照形状一致。
type PromptAssemblyBoundary = dto.PromptAssemblyBoundary

// PrefixShape 复用 provider DTO 的 start prompt 形状摘要，跨模块只传元数据不传正文。
type PrefixShape = dto.PrefixShape

// InvalidateReason 标识 prompt 缓存失效的触发来源。
type InvalidateReason string

const (
	// prompt 缓存失效原因常量，值需要保持稳定以便日志和测试做精确比对。
	InvalidateClear          InvalidateReason = "clear"
	InvalidateCompact        InvalidateReason = "compact"
	InvalidateWorktree       InvalidateReason = "worktree"
	InvalidateResumeRestore  InvalidateReason = "resume_restore"
	InvalidateProviderSwitch InvalidateReason = "provider_switch"
	InvalidateMemoryWrite    InvalidateReason = "memory_write"
)

const (
	// 动态 section 名称常量对应 prompt assembler 中可独立失效和重算的片段。
	DynamicSectionSessionGuidance        = "session_guidance"
	DynamicSectionProjectDefaultRules    = "project_default_rules"
	DynamicSectionAvailableExperts       = "available_experts"
	DynamicSectionRecallCatalog          = "recall_catalog"
	DynamicSectionPersonalizationProfile = "personalization_profile"
	DynamicSectionMemory                 = "memory"
	DynamicSectionMemoryContext          = "memory_context"
	DynamicSectionMemoryEntrypoint       = "memory_entrypoint"
	DynamicSectionEnvInfoSimple          = "env_info_simple"
	DynamicSectionDatasource             = "datasource"
	DynamicSectionDatasourceV2           = "datasource_v2"
	DynamicSectionLanguage               = "language"
	DynamicSectionMCPInstructions        = "mcp_instructions"
	DynamicSectionOutputStyle            = "output_style"
	DynamicSectionScratchpad             = "scratchpad"
	DynamicSectionFRC                    = "frc"
	DynamicSectionSummarizeToolResults   = "summarize_tool_results"
	DynamicSectionNumericLengthAnchors   = "numeric_length_anchors"
	DynamicSectionTokenBudget            = "token_budget"
	DynamicSectionBrief                  = "brief"
)

// PromptAssemblySnapshotVersion 标记 prompt 快照的缓存布局版本。
// 当前布局要求用户元信息和 System Context 由 assembler 重新拼装，
// 读取版本不匹配的快照时必须失效重算，不能复用旧 hash。
const PromptAssemblySnapshotVersion = 2

// StartInput 是创建 agent 时进入 prompt assembler 的启动参数。
// BaseInstructions 承载单块输入，BaseInstructionBlocks 承载分区输入；
// assembler 必须按二者的兼容规则生成同一个 provider wire 结果。
type StartInput struct {
	ThreadID         string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Name             string
	Prompt           string
	PromptKey        string
	BaseInstructions string
	// BaseInstructionBlocks 承载有序、带 region 的 prompt 片段。
	// 非空时 assembler 将其合并进 resolved sections；空切片表示只使用 BaseInstructions。
	BaseInstructionBlocks        []BaseInstructionBlock
	DeveloperInstructions        string
	Summary                      string
	Provider                     string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	ClaudeMdExcludes             []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
	OutputStyleConfig            *OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *FRCConfig
	KeepCodingInstructions       *bool
	// LaunchSkill* 保留 StartRequest 中的技能选择元数据，供审计和诊断读取。
	// provider 驱动负责协调 provider-native mirrors，prompt 层不读取技能正文。
	LaunchSkillNames  []string
	LaunchSkillRefs   []dto.SkillRef
	ForceLaunchSkills bool
}

// TurnInput 是用户后续 turn 进入 prompt assembler 的输入。
// 它只描述本轮消息和运行时上下文，不能承担 start-only 的持久状态初始化。
type TurnInput struct {
	ThreadID                     string
	Provider                     string
	UserText                     string
	PromptKey                    string
	Attachments                  []string
	CurrentDate                  string
	RuntimeUserContext           map[string]string
	Summary                      string
	CWD                          string
	GitRoot                      string
	IsWorktree                   bool
	Language                     string
	Model                        string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	ClaudeMdExcludes             []string
	MCPSnapshot                  MCPSnapshot
	SessionFlags                 map[string]bool
	OutputStyleConfig            *OutputStyleConfig
	ScratchpadDir                string
	FRCConfig                    *FRCConfig
	KeepCodingInstructions       *bool
}

// SectionContext 将通用 BuildCtx 与可选 start/turn 输入绑在一起供动态 section 读取。
// 动态 section 必须按 nil 判断区分启动拼装和 turn 拼装。
type SectionContext struct {
	BuildCtx BuildCtx
	Start    *StartInput
	Turn     *TurnInput
}

// SectionContextCWD 按 BuildCtx、StartInput、TurnInput 的优先级解析 section 工作目录。
// 动态 section 通过它取得当前轮次的工作区，避免自行猜测 start/turn 来源。
func SectionContextCWD(input SectionContext) string {
	if cwd := strings.TrimSpace(input.BuildCtx.CWD); cwd != "" {
		return cwd
	}
	if input.Start != nil {
		if cwd := strings.TrimSpace(input.Start.CWD); cwd != "" {
			return cwd
		}
	}
	if input.Turn != nil {
		return strings.TrimSpace(input.Turn.CWD)
	}
	return ""
}

// SectionComputeFunc 是动态 prompt section 的计算函数签名。
// 返回 nil 表示本 section 在当前上下文下不注入内容，错误会阻断关键 section。
type SectionComputeFunc func(context.Context, SectionContext) (*string, error)

// CachePolicy 定义动态 section 的缓存粒度。
type CachePolicy int

const (
	// CacheByName 表示同名 section 共享缓存。
	CacheByName CachePolicy = iota
	// Uncached 表示每次拼装都重新计算。
	Uncached
	// InputScoped 表示缓存随输入上下文变化而隔离。
	InputScoped
)

// PromptSection 描述 prompt assembler 可排序、可缓存、可计算的一个 section。
type PromptSection struct {
	Name        string
	Order       int
	Region      PromptRegion
	Volatile    bool
	CachePolicy CachePolicy
	StartOnly   bool
	Compute     SectionComputeFunc
}

// BaseInstructionBlock 是 prompt_template_sections 输入到 assembler 的有序片段。
// assembler 会把它转成 ResolvedPromptSection；Region 决定片段进入缓存前缀还是动态尾部。
//
// EnableWhen 保留原始 JSONB 条件表达式。nil 或空对象表示始终注入；
// 任一条件不匹配都会在合并阶段丢弃该块，因为 BuildCtx 只有在 assembler 内才完整。
type BaseInstructionBlock struct {
	Key        string
	Region     PromptRegion
	Ordinal    int
	Body       string
	EnableWhen []byte
}

// BuiltinPromptTemplate 是内置 prompt 模板的只读注册表记录。
// 字段保持数据库 seed 的 wire 形状，启动时据此对比并补齐缺失模板。
type BuiltinPromptTemplate struct {
	ID          int64
	PromptKey   string
	Kind        string
	Title       string
	AgentKey    string
	ToolName    string
	PromptText  string
	WhenToUse   string
	Description string
	Tags        []string
	Enabled     bool
	Scope       string
	MatchWhen   json.RawMessage
	Priority    int
}

// BuiltinPromptSection 是内置模板下的分区 prompt 片段。
// EnableWhen 和触发字段保持原始形态，避免 contract 层提前绑定 prompt 模块实现。
type BuiltinPromptSection struct {
	ID          int64
	TemplateID  int64
	SectionKey  string
	Region      string
	Ordinal     int
	Body        string
	EnableWhen  json.RawMessage
	Enabled     bool
	TriggerType string
	RecallTopic string
}

// BuiltinPromptRegistry 暴露内置 prompt 模板与 section 的只读查询能力。
type BuiltinPromptRegistry interface {
	ListTemplates() []BuiltinPromptTemplate
	GetTemplate(promptKey string) (BuiltinPromptTemplate, bool)
	SectionsByTemplateID(templateID int64) []BuiltinPromptSection
}

// CriticalPromptSectionError 包装关键 section 的计算失败。
// 调用方用该错误区分“必须阻断启动”和普通可跳过 section 的错误。
type CriticalPromptSectionError struct {
	Section string
	Err     error
}

// NewCriticalPromptSectionError 为关键 prompt section 失败补充 section 名称。
// err 为 nil 时返回 nil，避免调用方为无错误路径额外分支。
func NewCriticalPromptSectionError(section string, err error) error {
	if err == nil {
		return nil
	}
	return CriticalPromptSectionError{Section: strings.TrimSpace(section), Err: err}
}

// Error 返回包含 section 名称的错误文本，便于启动失败时定位阻断来源。
func (e CriticalPromptSectionError) Error() string {
	if e.Section == "" {
		return fmt.Sprintf("critical prompt section failed: %v", e.Err)
	}
	return fmt.Sprintf("critical prompt section %q failed: %v", e.Section, e.Err)
}

// Unwrap 暴露底层错误，允许上层通过 errors.Is/As 保留原始失败分类。
func (e CriticalPromptSectionError) Unwrap() error {
	return e.Err
}

// IsCriticalPromptSectionError 判断错误链中是否包含关键 section 失败。
func IsCriticalPromptSectionError(err error) bool {
	var target CriticalPromptSectionError
	return errors.As(err, &target)
}

// PrepareBaseInstructionBlocks 按 assembler 规则排序、裁剪并应用 EnableWhen 条件。
// 这里返回新的 slice，避免调用方传入的模板缓存被本轮用户输入污染。
func PrepareBaseInstructionBlocks(blocks []BaseInstructionBlock, buildCtx BuildCtx, userPrompt string, enableWhenEval EnableWhenEvaluator) []BaseInstructionBlock {
	sorted := make([]BaseInstructionBlock, len(blocks))
	copy(sorted, blocks)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Region != sorted[j].Region {
			return sorted[i].Region < sorted[j].Region
		}
		return sorted[i].Ordinal < sorted[j].Ordinal
	})
	out := make([]BaseInstructionBlock, 0, len(sorted))
	for _, block := range sorted {
		block.Body = strings.TrimSpace(block.Body)
		if enableWhenEval != nil && !enableWhenEval(block.EnableWhen, buildCtx, userPrompt) {
			continue
		}
		if block.Body != "" {
			out = append(out, block)
		}
	}
	return out
}

// TextFromBaseInstructionBlocks 渲染分区块的纯文本快照。
// 它只拼接非空正文，供缓存 hash 和兼容路径比较最终 provider 文本。
func TextFromBaseInstructionBlocks(blocks []BaseInstructionBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if body := strings.TrimSpace(block.Body); body != "" {
			parts = append(parts, body)
		}
	}
	return strings.Join(parts, "\n\n")
}

// StartAssembly 是 provider DTO 中启动 prompt 拼装结果的 contract 别名。
type StartAssembly = dto.StartAssembly

// TurnAssembly 是 provider DTO 中 turn prompt 拼装结果的 contract 别名。
type TurnAssembly = dto.TurnAssembly

// PromptAssemblySnapshot 是 provider DTO 中可持久化 prompt 快照的 contract 别名。
type PromptAssemblySnapshot = dto.PromptAssemblySnapshot

// DynamicSectionProvider 是可注册到 prompt assembler 的动态 section 提供者。
type DynamicSectionProvider interface {
	SectionName() string
	Resolve(ctx context.Context, input SectionContext) (*string, error)
}

// InvalidationAwareProvider 可在 prompt 缓存失效时同步清理自身内部缓存。
type InvalidationAwareProvider interface {
	OnPromptInvalidate(reason InvalidateReason)
}

// SectionInvalidator 清理指定 section 的缓存并返回新的 generation。
// 实现必须并发安全：auto-dream、extractor、turn-tracking 等后台 goroutine
// 会无外部锁地并发触发失效，替换实现也必须保留这个保证。
type SectionInvalidator interface {
	InvalidateSections(reason InvalidateReason, names ...string) uint64
}

// DynamicSectionRegistrar 注册动态 section provider。
type DynamicSectionRegistrar interface {
	RegisterDynamicProvider(provider DynamicSectionProvider) error
}

// ClaudeMdSourceProviderRegistrar 注册 CLAUDE.md / AGENTS.md 来源提供者。
type ClaudeMdSourceProviderRegistrar interface {
	RegisterClaudeMdSourceProvider(provider ClaudeMdSourceProvider) error
}

// ClaudeMdSourceProvider 解析当前 BuildCtx 下适用的规则文件来源。
type ClaudeMdSourceProvider interface {
	ResolveClaudeMdSources(ctx context.Context, buildCtx BuildCtx) ([]ClaudeMdSource, error)
}

// TurnAttachmentProvider 基于本轮输入和规则来源解析 provider 附件。
type TurnAttachmentProvider interface {
	ResolveTurnAttachments(ctx context.Context, buildCtx BuildCtx, turn TurnInput, baseSources []ClaudeMdSource) ([]dto.AttachmentEnvelope, error)
}

// TurnContextPayload 是 turn 上下文拼装后的 provider 输入和附件集合。
type TurnContextPayload struct {
	Inputs      []shareddto.InputItem
	Attachments []dto.AttachmentEnvelope
}

// TurnContextProvider 为指定会话和查询准备本轮 provider 上下文。
type TurnContextProvider interface {
	PrepareTurnContext(ctx context.Context, session Session, buildCtx BuildCtx, threadID, query string) TurnContextPayload
}

// preferredUserContextKeys 控制用户上下文块的稳定渲染顺序。
var preferredUserContextKeys = []string{
	"claudeMd",
	"currentDate",
	"workerToolsContext",
	"terminalFocus",
	"runtimeExtras",
}

// FormatUserContextText 将 runtime user context 渲染为稳定顺序的文本块。
// 空值会被丢弃，未知 key 会排在已知 key 后面，保证同一 payload 生成稳定 prompt。
func FormatUserContextText(payload map[string]string) string {
	normalized := normalizeUserContext(payload)
	if len(normalized) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(normalized))
	for _, key := range orderedUserContextKeys(normalized) {
		if block := renderUserContextSection(key, normalized[key]); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

// RenderUserContextMessage 生成 turn prompt 中的 system-reminder 用户上下文块。
// 优先使用结构化 UserContext，缺失时才使用已拼好的 UserContextText。
func RenderUserContextMessage(assembly TurnAssembly) string {
	if text := FormatUserContextText(assembly.UserContext); text != "" {
		return WrapSystemReminder(text)
	}
	return WrapSystemReminder(assembly.UserContextText)
}

// RenderStartRuntimeContext 生成 start prompt 中的 runtime context 块。
// 当 runtimeExtras 已由静态 section 渲染时，会从用户上下文中剔除它以避免重复注入。
func RenderStartRuntimeContext(assembly StartAssembly) string {
	payload := assembly.UserContext
	userContextText := assembly.UserContextText
	if startRuntimeExtrasAlreadyRendered(assembly) {
		payload = cloneUserContextWithout(payload, "runtimeExtras")
		userContextText = FormatUserContextText(payload)
	}
	userContext := RenderUserContextMessage(TurnAssembly{
		UserContext:     payload,
		UserContextText: userContextText,
	})
	return appendPromptBlock(userContext, FormatSystemContextBlock(assembly.SystemContext))
}

// startRuntimeExtrasAlreadyRendered 判断 runtimeExtras 是否已经出现在 resolved section 尾部。
// start prompt 通过它避免把同一运行时信息重复写入 system-reminder。
func startRuntimeExtrasAlreadyRendered(assembly StartAssembly) bool {
	if strings.TrimSpace(assembly.UserContext["runtimeExtras"]) == "" {
		return false
	}
	if assembly.Boundary != nil && strings.TrimSpace(assembly.Boundary.UncachedTail) != "" {
		return true
	}
	return assembly.Snapshot.Boundary != nil && strings.TrimSpace(assembly.Snapshot.Boundary.UncachedTail) != ""
}

// cloneUserContextWithout 复制用户上下文并移除指定 key。
// 返回 nil 表示删除后没有可渲染内容，调用方可直接跳过该 prompt 块。
func cloneUserContextWithout(in map[string]string, dropKey string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if key == dropKey {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AppendStartRuntimeContext 将 start runtime context 安全追加到已有 prompt。
// appendPromptBlock 会做去重，避免重复注入相同 system-reminder。
func AppendStartRuntimeContext(base string, assembly StartAssembly) string {
	return appendPromptBlock(base, RenderStartRuntimeContext(assembly))
}

// appendPromptBlock 追加非空 prompt 块。
// 若 base 已包含完整 block，则保持原文，避免重复拼接同一系统上下文。
func appendPromptBlock(base, block string) string {
	base = strings.TrimSpace(base)
	block = strings.TrimSpace(block)
	if block == "" {
		return base
	}
	if base == "" {
		return block
	}
	if strings.Contains(base, block) {
		return base
	}
	return base + "\n\n" + block
}

// orderedUserContextKeys 固定用户上下文的渲染顺序。
// 已知 key 先按 preferredUserContextKeys 输出，额外 key 按字典序输出。
func orderedUserContextKeys(payload map[string]string) []string {
	seen := make(map[string]struct{}, len(payload))
	ordered := make([]string, 0, len(payload))
	for _, key := range preferredUserContextKeys {
		if _, ok := payload[key]; ok {
			ordered = append(ordered, key)
			seen[key] = struct{}{}
		}
	}
	extra := make([]string, 0, len(payload))
	for key := range payload {
		if _, ok := seen[key]; ok {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	return append(ordered, extra...)
}

// normalizeUserContext 清理用户上下文 payload。
// key 和正文都会 trim，空 key 或空正文不进入 prompt，避免生成无意义 section。
func normalizeUserContext(payload map[string]string) map[string]string {
	if len(payload) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(payload))
	for key, value := range payload {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// renderUserContextSection 将单个用户上下文条目渲染成标题块。
// 空 key 或空正文返回空串，由上层负责跳过。
func renderUserContextSection(key, body string) string {
	key = strings.TrimSpace(key)
	body = strings.TrimSpace(body)
	if key == "" || body == "" {
		return ""
	}
	return "# " + key + "\n" + body
}

// WrapSystemReminder 用 provider 约定的 system-reminder 标签包裹文本。
// 空文本直接返回空串，已包裹文本保持原样，方便 prompt assembler 幂等调用。
func WrapSystemReminder(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "<system-reminder>") {
		return text
	}
	return strings.Join([]string{"<system-reminder>", text, "</system-reminder>"}, "\n\n")
}

// AppendSystemContextTail 将 System Context 追加到 prompt 尾部。
// 空 base 或空 context 都按原值返回，避免调用方重复判断。
func AppendSystemContextTail(base string, ctx SystemContext) string {
	block := FormatSystemContextBlock(ctx)
	base = strings.TrimSpace(base)
	if base == "" {
		return block
	}
	if block == "" {
		return base
	}
	return base + "\n\n" + block
}

// FormatSystemContextBlock 按 provider 可读格式渲染 System Context。
// gitStatus 和 cacheBreaker 使用固定标签，其余非空 key 保留原名输出。
func FormatSystemContextBlock(ctx SystemContext) string {
	if len(ctx) == 0 {
		return ""
	}
	lines := []string{"# System Context"}
	for _, key := range orderedSystemContextKeys(ctx) {
		value := strings.TrimSpace(ctx[key])
		if value == "" {
			continue
		}
		switch key {
		case "gitStatus":
			lines = append(lines, "Git status:", value)
		case "cacheBreaker":
			lines = append(lines, "Cache breaker: "+value)
		default:
			lines = append(lines, key+":", value)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// orderedSystemContextKeys 固定 System Context 的渲染顺序。
// 未知 key 排在已知 key 之后并按字典序输出，保证 prompt 快照稳定。
func orderedSystemContextKeys(ctx SystemContext) []string {
	keys := make([]string, 0, len(ctx))
	for _, key := range []string{"gitStatus", "cacheBreaker"} {
		if value := strings.TrimSpace(ctx[key]); value != "" {
			keys = append(keys, key)
		}
	}
	extra := make([]string, 0, len(ctx))
	for key, value := range ctx {
		if key == "gitStatus" || key == "cacheBreaker" || strings.TrimSpace(value) == "" {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	return append(keys, extra...)
}

// AgentType 标识 subagent 的 prompt 拼装类别。
// 未知类型不触发 Explore/Plan 专属后处理，按主线程 AssembleStart 路径生成 provider prompt。
type AgentType string

const (
	AgentTypeDefault AgentType = ""
	AgentTypeExplore AgentType = "Explore"
	AgentTypePlan    AgentType = "Plan"
)

var readOnlyAgentDeniedTools = []string{
	"edit", "lsp_edit", "shared_file_write", "memory_write",
	"task_create_dag", "task_dag_apply_ops", "task_update_node", "task_dispatch_node",
	"task_start_dag", "task_terminate_dag", "task_delete_dag", "task_workflow_recovery_action",
	"workspace_create_run", "workspace_merge_run", "workspace_abort_run",
	"workflow_template_save", "workflow_template_rollback",
	"wait", "bash_output", "BashOutput", "update_plan", "todo_write", "TodoWrite", "complete_step",
	"multi_agent", "multi_tool_use.parallel", "spawn_agent", "send_input",
	"resume_agent", "wait_agent", "close_agent",
	"launch_agent", "send_message", "stop_agent", "recover_agent", "interrupt_agent",
	"list_agents", "get_agent_report", "get_agent_reports",
	"orchestration_launch_agent", "orchestration_send_message", "orchestration_stop_agent",
	"orchestration_recover_agent", "orchestration_interrupt_agent", "orchestration_list_agents",
	"orchestration_get_agent_report", "orchestration_get_agent_reports",
	"connect_tool_source",
}

// ReadOnlyAgentDeniedTools 返回只读/规划子 agent 必须禁用的精确工具名。
// 返回副本避免调用方修改共享 deny list，launch env 和 reviewer preset 共用这份名单。
func ReadOnlyAgentDeniedTools() []string {
	return append([]string(nil), readOnlyAgentDeniedTools...)
}

// AgentInput 汇总 AssembleAgent 所需的 subagent prompt 参数。
// OverrideSystemPrompt 非空时直接作为最终系统 prompt；否则先执行 AssembleStart，
// 再按 AgentType 应用 Explore/Plan 的规则文件、git 状态和环境信息处理。
type AgentInput struct {
	StartInput           StartInput
	AgentType            AgentType
	OverrideSystemPrompt string
}

// PromptAssemblyService 是 thread、provider 与 prompt 模块之间的组装边界。
// Start/Turn/Agent 分别生成 provider wire prompt，Invalidate 用于清理动态 section 缓存。
type PromptAssemblyService interface {
	AssembleStart(ctx context.Context, in StartInput) (StartAssembly, error)
	AssembleTurn(ctx context.Context, in TurnInput) (TurnAssembly, error)
	AssembleAgent(ctx context.Context, in AgentInput) (StartAssembly, error)
	Invalidate(ctx context.Context, reason InvalidateReason) error
}

// MatchWhenEvaluator 判断 prompt_template 的 match_when JSONB 是否匹配当前上下文。
// thread router 通过该函数做自动路由判断，同时避免直接导入 prompt 模块实现。
type MatchWhenEvaluator func(raw []byte, buildCtx BuildCtx, userPrompt string) bool

// EnableWhenEvaluator 判断 prompt_template_section 的 enable_when JSONB 是否允许注入。
// thread router 用它生成与 assembler 注入规则一致的 prompt_versions 快照。
type EnableWhenEvaluator func(raw []byte, buildCtx BuildCtx, userPrompt string) bool
