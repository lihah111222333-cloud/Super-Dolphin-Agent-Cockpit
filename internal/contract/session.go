package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// SessionStarter 是启动和恢复 provider session 的跨模块入口。
// 生产实现位于 provider/unified，调用方不直接依赖具体 provider driver。
type SessionStarter interface {
	StartSession(ctx context.Context, req dto.StartSessionRequest) (Session, error)
	ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (Session, error)
}

// SessionProvider 提供按 agent ID 查找/移除 session 的最小能力。
// thread 模块通过该端口保持 provider-neutral。
type SessionProvider interface {
	GetSession(agentID string) (Session, error)
	RemoveSession(agentID string)
}

// SessionResolver 将 thread ID 解析为当前活跃 session。
// 解析失败应由实现层返回明确错误，调用方不做静默兜底。
type SessionResolver interface {
	ResolveSession(ctx context.Context, threadID string) (Session, error)
}

// SessionRecoveryReporter 记录 auto-resume 过程中修复的 provider 身份字段。
type SessionRecoveryReporter interface {
	ClearStaleProviderThreadID(ctx context.Context, agentID string) error
	RecordProviderSessionUUID(ctx context.Context, agentID, sessionUUID string) error
}

// SessionStartRequest 是内部 session port 的稳定启动 DTO。
// 它覆盖 thread.StartRequest 中 adapter 必须透传的启动字段；PromptAssemblyRef、PromptVersionID、
// AgentTitle 和 PromptKeyStale 仍由 thread service 内部填充，不从该 port 入口传入。
type SessionStartRequest struct {
	Provider, AgentID, ParentAgentID, AgentType, AgentMemoryScope string
	CWD, Model, ModelProvider, Name                               string
	Prompt, BaseInstructions                                      string
	BaseInstructionBlocks                                         []BaseInstructionBlock
	DeveloperInstructions                                         string
	ApprovalPolicy                                                string
	Sandbox                                                       json.RawMessage
	Summary                                                       string
	Effort                                                        string
	Personality                                                   string
	Language                                                      string
	GitRoot                                                       string
	IsWorktree                                                    bool
	ToolSurfaceMode                                               string
	EnabledTools                                                  []string
	AdditionalWorkingDirectories                                  []string
	MCPSnapshot                                                   MCPSnapshot
	SessionFlags                                                  map[string]bool
	Config                                                        map[string]any
	LaunchSkillNames                                              []string
	LaunchSkillRefs                                               []dto.SkillRef
	ForceLaunchSkills                                             bool
	AgentKey                                                      string
	PromptKey                                                     string
	OwnerThreadID, LaunchIntentID                                 string
	DeferSpawn                                                    bool
}

// SessionStartResult 是内部 session port 的稳定启动结果。
type SessionStartResult struct {
	ThreadID        string `json:"thread_id"`
	AgentID         string `json:"agent_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Status          string `json:"status,omitempty"`
	Model           string `json:"model,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ModelProvider   string `json:"modelProvider,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	ApprovalPolicy  string `json:"approvalPolicy,omitempty"`
	AgentKey        string `json:"agent_key,omitempty"`
	AgentTitle      string `json:"agent_title,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	PromptKeyStale  bool   `json:"prompt_key_stale,omitempty"`
	PendingLaunch   bool   `json:"pending_launch,omitempty"`
}

// SessionThreadSummary 是 session status 端口暴露给 UI/RPC 的线程摘要。
type SessionThreadSummary struct {
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

// SessionLifecyclePort 收窄 session 创建、恢复、fork 和归档入口。
type SessionLifecyclePort interface {
	StartSession(ctx context.Context, req SessionStartRequest) (SessionStartResult, error)
	ResumeSession(ctx context.Context, threadID string) (SessionStartResult, error)
	ForkSession(ctx context.Context, threadID string) (SessionStartResult, error)
	ArchiveSession(ctx context.Context, threadID string) error
}

// SessionStatusPort 暴露 session 列表和消息读取入口。
type SessionStatusPort interface {
	ListSessions(ctx context.Context) ([]SessionThreadSummary, error)
	ReadMessages(ctx context.Context, threadID string, limit int, before string) (dto.ThreadMessagesResult, error)
}

// SessionPorts 聚合第一阶段 session lifecycle/read 端口。
type SessionPorts interface {
	SessionLifecyclePort
	SessionStatusPort
}

// SessionThreadRef / SessionBinding 是 provider/unified session resolver 使用的窄投影。
// 这些类型隔离 store 结构，避免 provider 反向导入持久化实现。

// SessionThreadRef 是 resolver 需要的最小 thread 投影，携带 thread 到 agent 的绑定信息。
type SessionThreadRef struct {
	ThreadID       string
	AgentID        string
	Status         string
	RuntimeConfig  map[string]any
	PromptSnapshot PromptAssemblySnapshot
}

// ValidateResumePromptSnapshot 校验 resume 请求必须携带可复用的启动提示快照。
// auto-resume 和 provider adapter 共用这道边界，避免空快照进入运行时后再隐式补默认提示。
func ValidateResumePromptSnapshot(snapshot PromptAssemblySnapshot) error {
	if snapshot.Version != PromptAssemblySnapshotVersion {
		return fmt.Errorf("resume prompt snapshot version %d is invalid", snapshot.Version)
	}
	if strings.TrimSpace(snapshot.Hash) == "" {
		return fmt.Errorf("resume prompt snapshot hash is required")
	}
	if strings.TrimSpace(snapshot.BaseInstructions) == "" &&
		strings.TrimSpace(snapshot.DeveloperInstructions) == "" &&
		resumePromptBoundaryBlank(snapshot.Boundary) &&
		len(snapshot.SectionSnapshot) == 0 {
		return fmt.Errorf("resume prompt snapshot content is empty")
	}
	return nil
}

func resumePromptBoundaryBlank(boundary *PromptAssemblyBoundary) bool {
	return boundary == nil ||
		(strings.TrimSpace(boundary.CachedPrefix) == "" &&
			strings.TrimSpace(boundary.UncachedTail) == "")
}

// SessionBinding 是重启后 auto-resume 所需的持久化绑定投影。
// 字段保留 provider/local/runtime 身份，缺失时由恢复流程显式修复或报错。
type SessionBinding struct {
	AgentID              string
	Provider             string
	ProviderThreadID     string
	CodexThreadID        string
	RolloutPath          string
	SessionUUID          string
	Cwd                  string
	ParentAgentID        string
	AgentType            string
	AgentMemoryScope     string
	Archived             bool
	CreatedAt            int64
	CodexHome            string
	ProviderRecoveryHome string
	CodexInstanceKey     string
	CodexModelProvider   string
}

// SessionThreadLookup 将公开 thread ID 解析为 resolver 所需的 thread 投影。
type SessionThreadLookup interface {
	GetByThreadID(ctx context.Context, threadID string) (*SessionThreadRef, error)
}

// SessionBindingLookup 读取 auto-resume 需要的 provider 绑定记录。
type SessionBindingLookup interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*SessionBinding, error)
	GetByAgentID(ctx context.Context, agentID string) (*SessionBinding, error)
}

// SessionBindingUpserter 在 auto-resume 恢复 provider 身份后回写持久化绑定。
// 该端口用于补齐历史缺失字段，但不改变当前会话的 provider 归属。
type SessionBindingUpserter interface {
	UpsertSessionBinding(ctx context.Context, binding SessionBinding) error
}

// TurnThreadCleaner 分组定义 thread 关闭/归档时清理 turn 状态的端口。

// TurnThreadCleaner 是 thread 模块中断运行中 turn 并清理 turn 状态的窄契约。
// 接口放在 contract 层，保证 thread 不直接导入 internal/module/turn。
type TurnThreadCleaner interface {
	// InterruptActiveTurn 取消给定 session 上正在执行的 turn。
	// source 是观测标签，用于区分停止、归档或删除等调用来源。
	InterruptActiveTurn(ctx context.Context, session Session, source string) error
	// CleanupThread 移除 threadID 关联的 turn 状态记录。
	// reason 是观测标签，调用方应传入明确的业务原因。
	CleanupThread(ctx context.Context, threadID, reason string) error
}
