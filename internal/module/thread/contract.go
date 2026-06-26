package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

// SpawnRouting 复用 shared dto 中的 pending spawn 路由结果。
// canonical 类型放在 dto/thread/event.go，避免 thread 与 turn 包互相导入。
type SpawnRouting = threaddto.SpawnRouting

// Service 是 thread 模块对外的入口。
// Start/Resume/Fork/Recover 负责 provider session 和 thread 记录；turn、memory、prompt 各做自己的事。
type Service interface {
	Start(ctx context.Context, req StartRequest) (StartResult, error)
	Stop(ctx context.Context, threadID string) error
	Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error)
	Fork(ctx context.Context, threadID string) (ForkResult, error)
	Recover(ctx context.Context, threadID string) (RecoverResult, error)
	Handoff(ctx context.Context, req HandoffRequest) (HandoffResult, error)
	// SpawnIfNeeded 为 pending_launch 线程在首个 turn 到达时 fork provider CLI。
	// 同一 thread 可并发调用，但只有第一个调用会真正启动；launched=true 时返回路由结果，
	// 供 turn/start 转发给 UI。requestCWD 只参与校验，pending row 里的 CWD 才是权威值。
	SpawnIfNeeded(ctx context.Context, threadID, userInputForRouter, requestCWD string) (launched bool, routing threaddto.SpawnRouting, err error)

	List(ctx context.Context) ([]Ref, error)
	Get(ctx context.Context, id string) (*Ref, error)
	ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)
	ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error)
	GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error)
	SetConfig(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) (dto.ThreadConfig, error)
	SetModel(ctx context.Context, threadID, model string) (dto.ThreadConfig, error)
	Compact(ctx context.Context, threadID, args string) (dto.ThreadCompactResult, error)
	Archive(ctx context.Context, threadID string) error
	Unarchive(ctx context.Context, threadID string) error
	ListByStatus(ctx context.Context, status string) ([]Ref, error)
	ListByCWD(ctx context.Context, cwdPrefix string) ([]Ref, error)
	SendCommand(ctx context.Context, threadID, command, args string) (any, error)
	SetName(ctx context.Context, threadID, name string) error
	Delete(ctx context.Context, threadID string) error
}

// StartRequest 是新 thread 的启动输入。
// provider、cwd 和 config 会一路进入 prompt 组装和 provider 启动；snapshot 也从这里开始生成。
type StartRequest struct {
	Provider, AgentID, ParentAgentID, AgentType, AgentMemoryScope string
	CWD, Model, ModelProvider, Name                               string
	// Prompt 只保留给旧调用方作为 display name 别名，新入口应使用 Name。
	Prompt, BaseInstructions string
	// BaseInstructionBlocks 由路由命中的 prompt_template_sections 填充。
	// 它会进入 prompt assembler 做 region-aware 合并；为空时表示仍使用单段 BaseInstructions。
	BaseInstructionBlocks        []contract.BaseInstructionBlock
	DeveloperInstructions        string
	ApprovalPolicy               string
	Sandbox                      json.RawMessage
	Summary                      string
	Effort                       string
	Personality                  string
	PromptAssemblyRef            contract.PromptAssemblyService
	Language                     string
	GitRoot                      string
	IsWorktree                   bool
	ToolSurfaceMode              string
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  contract.MCPSnapshot
	SessionFlags                 map[string]bool
	Config                       map[string]any
	// LaunchSkillNames / ForceLaunchSkills 保留启动 payload 的旧 skill 选择字段。
	// 当前 skill runtime 通过 provider-native mirror 注入；这些字段只用于兼容已有调用方。
	LaunchSkillNames  []string
	LaunchSkillRefs   []dto.SkillRef
	ForceLaunchSkills bool

	// AgentKey 非空时直接使用指定 agent_key，跳过自动路由；为空且 BaseInstructions 为空时才触发路由。
	AgentKey string
	// PromptVersionID 由 service 在 materialize prompt_versions 后填入，不是调用方输入。
	PromptVersionID *int64
	// PromptKey 是调用方显式 pin 的 prompt_template。
	// 命中启用模板时优先级高于 AgentKey；为空时由路由填入命中的模板 key，供观测和 UI 展示。
	PromptKey string
	// AgentTitle 由路由命中的模板 Title 填入，用于 UI 显示人类可读的 agent 标签。
	AgentTitle string
	// PromptKeyStale 表示调用方 pin 的 PromptKey 未命中启用模板。
	// 该信号会进入 RPC 响应，UI 据此清理本地 activePromptKey；prompt store 未装配的降级路径不能置 true。
	PromptKeyStale                bool
	OwnerThreadID, LaunchIntentID string

	// DeferSpawn 表示只写 pending_launch 线程，不立即 fork provider CLI。
	// 首个 turn 带真实用户输入进入 SpawnIfNeeded 后才路由和启动；普通调用方保持 false 走立即启动路径。
	DeferSpawn bool
}

type StartResult struct {
	ThreadID       string `json:"thread_id"`
	AgentID        string `json:"agent_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	Status         string `json:"status,omitempty"`
	Model          string `json:"model,omitempty"`
	Provider       string `json:"provider,omitempty"`
	ModelProvider  string `json:"modelProvider,omitempty"`
	CWD            string `json:"cwd,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	// 路由元数据供 UI 展示命中的 agent、prompt_template 和 prompt_versions 快照。
	AgentKey string `json:"agent_key,omitempty"`
	// AgentTitle 是 UI badge 展示的人类可读 persona 名称；未走路由时为空。
	AgentTitle      string `json:"agent_title,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	// PromptKeyStale 镜像 StartRequest.PromptKeyStale，true 时 UI 应清理失效 prompt pin。
	// false 表示 pin 有效或调用方未 pin，UI 必须保留原偏好。
	PromptKeyStale bool `json:"prompt_key_stale,omitempty"`
	// PendingLaunch=true 表示后端已写 thread row 但尚未 fork provider CLI。
	// UI 应展示 pending 状态，并在 thread.launched 到达后切换为 running。
	PendingLaunch bool `json:"pending_launch,omitempty"`
}

// ResumeRequest 是恢复旧 thread 的输入。
// 调用方可以传覆盖项，但 thread/binding/config 里的状态才是准绳；PromptSnapshot 不要随便伪造。
type ResumeRequest struct {
	Provider                 string
	AgentID                  string
	ThreadID                 string
	ProviderThreadID         string
	Path                     string
	CWD                      string
	Model                    string
	Effort                   string
	Config                   map[string]any
	PromptSnapshot           contract.PromptAssemblySnapshot
	ConfigOverride           dto.ThreadConfigPatch
	ClaudeHome               string
	CodexHome                string
	CodexInstanceKey         string
	CodexModelProvider       string
	CodexDisabledNativeTools []string
}

type ResumeResult struct {
	ThreadID  string `json:"thread_id"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status"`
	Model     string `json:"model,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

type ForkResult struct {
	NewThreadID string `json:"new_thread_id"`
	ForkedFrom  string `json:"forked_from,omitempty"`
}

// HandoffRequest 描述从源 thread 切到目标 agent 的交接请求。
// 源 thread 保持可返回状态，新 thread 通过 OwnerThreadID 关联回源 thread。
type HandoffRequest struct {
	SourceThreadID, TargetAgentKey string
	// InitialMessage 用作新 thread 初始展示名；为空时由 service 生成交接标题。
	InitialMessage string
}

type HandoffResult struct {
	SourceThreadID  string `json:"source_thread_id"`
	NewThreadID     string `json:"new_thread_id"`
	AgentID         string `json:"agent_id,omitempty"`
	AgentKey        string `json:"agent_key,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	Status          string `json:"status,omitempty"`
}

type RecoverResult struct {
	ThreadID  string `json:"thread_id"`
	Status    string `json:"status,omitempty"`
	Recovered bool   `json:"recovered"`
	Mode      string `json:"mode,omitempty"`
}
type LaunchAgentRequest struct {
	AgentID, Name, ParentID, AgentType, MemoryScope, Cwd string
	Command, Env                                         []string
}
type Ref struct {
	ID               string `json:"id"`
	Name             string `json:"name,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	Status           string `json:"status,omitempty"`
	CreatedAt        int64  `json:"created_at,omitempty"`
	UpdatedAt        int64  `json:"updated_at,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderThreadID string `json:"providerThreadId,omitempty"`
	SessionID        string `json:"sessionId,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
	Port             int    `json:"port,omitempty"`
}

type ReadHistoryThread struct {
	ThreadID string `json:"thread_id"`
}

type ReadHistoryResult struct {
	History []ReadHistoryThread `json:"history"`
}
