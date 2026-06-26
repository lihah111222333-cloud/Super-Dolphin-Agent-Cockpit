package provider

import "encoding/json"

// PromptRegion 标识 prompt section 在缓存边界中的位置。
type PromptRegion int

// PromptRegion* 枚举值。
const (
	PromptRegionStatic  PromptRegion = iota // 静态前缀，可被缓存。
	PromptRegionDynamic                     // 动态尾部，每次 turn 重建。
)

// ResolvedPromptSection 是已解析的 prompt section，包含名称、区域和内容。
type ResolvedPromptSection struct {
	Name     string       `json:"name,omitempty"`
	Region   PromptRegion `json:"region,omitempty"`
	Volatile bool         `json:"volatile,omitempty"` // 易变内容不参与缓存。
	Content  string       `json:"content,omitempty"`
}

// SystemContext 是每次 start/turn 携带的系统级键值上下文（如 git status、cache breaker）。
type SystemContext map[string]string

// PromptAssemblyBoundary 描述 prompt 的缓存分界点。
type PromptAssemblyBoundary struct {
	CachedPrefix string `json:"cachedPrefix,omitempty"` // 可复用的缓存前缀内容。
	UncachedTail string `json:"uncachedTail,omitempty"` // 每次重建的动态尾部内容。
}

// PromptAssemblySnapshot 是 prompt 组装的不可变快照，用于 resume 时的对比与调试。
type PromptAssemblySnapshot struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	Boundary              *PromptAssemblyBoundary `json:"boundary,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	Provider              string                  `json:"provider,omitempty"`
	Version               int                     `json:"version,omitempty"`
	Hash                  string                  `json:"hash,omitempty"`
	SectionSnapshot       map[string]string       `json:"sectionSnapshot,omitempty"`
	Generation            uint64                  `json:"generation,omitempty"`
}

// StartAssembly 是 StartSessionRequest 中的 prompt 组装载荷。
// UserContext/SystemContext 是结构化上下文载体；BaseInstructions 仍保留兼容内容，消费者不能只依赖单一路径。
type StartAssembly struct {
	DisplayName           string                  `json:"displayName,omitempty"`
	BaseInstructions      string                  `json:"baseInstructions,omitempty"`
	Boundary              *PromptAssemblyBoundary `json:"boundary,omitempty"`
	DeveloperInstructions string                  `json:"developerInstructions,omitempty"`
	ResolvedSections      []ResolvedPromptSection `json:"resolvedSections,omitempty"`
	Snapshot              PromptAssemblySnapshot  `json:"snapshot"`
	SuppressedTools       []string                `json:"suppressedTools,omitempty"`

	// UserContext 是 start 阶段用户上下文的结构化 map。
	// provider bridge 可将它路由到非缓存用户上下文消息；兼容期内 BaseInstructions 仍可能携带同类内容。
	UserContext map[string]string `json:"userContext,omitempty"`
	// UserContextText 是 UserContext 的渲染文本，供只接收字符串的 provider 路径复用。
	UserContextText string `json:"userContextText,omitempty"`
	// SystemContext 携带 start 阶段系统上下文，如 git status、cache breaker 等。
	SystemContext SystemContext `json:"systemContext,omitempty"`
}

// TurnAssembly 是每次 turn 请求携带的 prompt 组装增量，包含用户上下文和附件。
type TurnAssembly struct {
	UserContext      map[string]string       `json:"userContext,omitempty"`
	UserContextText  string                  `json:"userContextText,omitempty"`
	SystemContext    SystemContext           `json:"systemContext,omitempty"`
	Attachments      []AttachmentEnvelope    `json:"attachments,omitempty"`
	ResolvedSections []ResolvedPromptSection `json:"resolvedSections,omitempty"`
}

// StartSessionRequest 是新建 provider session 的请求 DTO。
// LaunchSkillNames/LaunchSkillRefs 仅保留线上兼容性，V1 provider-native 链路不再注入正文。
type StartSessionRequest struct {
	Provider        string         `json:"provider"`
	AgentID         string         `json:"agentId"`
	CWD             string         `json:"cwd"`
	Model           string         `json:"model,omitempty"`
	Instructions    string         `json:"instructions,omitempty"`
	StartAssembly   StartAssembly  `json:"startAssembly"`
	Config          map[string]any `json:"config,omitempty"`
	ToolSurfaceMode string         `json:"toolSurfaceMode,omitempty"`

	// LaunchSkillNames 是旧客户端仍会发送的启动 skill 名称列表。
	// 当前 provider-native 路径不把它注入正文或 manifest，只作为兼容 wire 字段保留。
	LaunchSkillNames []string `json:"launchSkillNames,omitempty"`
	// LaunchSkillRefs 携带 UI 选中的精确 skill 引用，用于诊断和同名保真，不作为 prompt 注入路径。
	LaunchSkillRefs []SkillRef `json:"launchSkillRefs,omitempty"`
	// ForceLaunchSkills 保留旧客户端手动选择 skill 的 wire 兼容标志。
	ForceLaunchSkills bool `json:"forceLaunchSkills,omitempty"`
}

// ResumeSessionRequest 是恢复已有 provider session 的请求 DTO。
type ResumeSessionRequest struct {
	Provider                 string                 `json:"provider"`
	AgentID                  string                 `json:"agentId"`
	ThreadID                 string                 `json:"threadId"`
	ProviderThreadID         string                 `json:"providerThreadId,omitempty"`
	Path                     string                 `json:"path,omitempty"`
	CWD                      string                 `json:"cwd,omitempty"`
	Model                    string                 `json:"model,omitempty"`
	Effort                   string                 `json:"effort,omitempty"`
	Config                   map[string]any         `json:"config,omitempty"`
	PromptSnapshot           PromptAssemblySnapshot `json:"promptSnapshot"`
	ConfigOverride           ThreadConfigPatch      `json:"configOverride"`
	ClaudeHome               string                 `json:"claudeHome,omitempty"`
	CodexHome                string                 `json:"codexHome,omitempty"`
	CodexInstanceKey         string                 `json:"codexInstanceKey,omitempty"`
	CodexModelProvider       string                 `json:"codexModelProvider,omitempty"`
	CodexDisabledNativeTools []string               `json:"codexDisabledNativeTools,omitempty"`
}

var _ json.Marshaler = json.RawMessage(nil)
