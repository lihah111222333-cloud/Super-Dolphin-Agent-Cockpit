package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type Service interface {
	Start(ctx context.Context, req StartRequest) (StartResult, error)
	Stop(ctx context.Context, threadID string) error
	Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error)
	Fork(ctx context.Context, threadID string) (ForkResult, error)
	Recover(ctx context.Context, threadID string) (RecoverResult, error)
	Handoff(ctx context.Context, req HandoffRequest) (HandoffResult, error)

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

type StartRequest struct {
	Provider         string
	AgentID          string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	CWD              string
	Model            string
	ModelProvider    string
	Name             string
	// Deprecated: use Name for display-name semantics; Prompt is kept only for legacy callers.
	Prompt                       string
	BaseInstructions             string
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
	EnabledTools                 []string
	AdditionalWorkingDirectories []string
	MCPSnapshot                  contract.MCPSnapshot
	SessionFlags                 map[string]bool
	Config                       map[string]any
	// LaunchSkillNames / ForceLaunchSkills p20.3 §4.3：public payload 投影的
	// launch skill 载荷。additive optional：nil/false 时下游行为与旧 payload
	// 完全一致。本字段只负责运输，真正消费由 p20.4 / p20.7 接盘。
	LaunchSkillNames  []string
	ForceLaunchSkills bool

	// AgentKey is optional. When non-empty, the service skips router
	// classification and uses this agent_key directly. Empty + empty
	// BaseInstructions triggers router.
	AgentKey string
	// PromptVersionID is filled by the service after it materializes a
	// prompt_versions row for this thread start; it is not an input.
	PromptVersionID *int64
	// OwnerThreadID links this thread back to a predecessor (e.g. the source
	// thread in a handoff). Empty for brand-new top-level threads.
	OwnerThreadID string
}

type StartResult struct {
	ThreadID        string `json:"thread_id"`
	AgentID         string `json:"agent_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Status          string `json:"status,omitempty"`
	Model           string `json:"model,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ModelProvider   string `json:"modelProvider,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	ApprovalPolicy  string `json:"approvalPolicy,omitempty"`
	// Routing metadata surfaced to the UI so the sidebar can show which agent
	// the router picked and which prompt_versions row was injected.
	AgentKey        string `json:"agent_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
}

type ResumeRequest struct {
	Provider         string
	AgentID          string
	ThreadID         string
	ProviderThreadID string
	Path             string
	CWD              string
	Model            string
	Effort           string
	PromptSnapshot   contract.PromptAssemblySnapshot
	ConfigOverride   dto.ThreadConfigPatch
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

// HandoffRequest moves the active conversation from one agent to another.
// The service keeps the source thread running (user may return) and starts
// a fresh thread for the target agent, linked back via OwnerThreadID.
type HandoffRequest struct {
	SourceThreadID string
	TargetAgentKey string
	// Optional initial message to seed the new thread's display name.
	// Empty falls back to "handoff from <source>".
	InitialMessage string
}

type HandoffResult struct {
	SourceThreadID  string `json:"source_thread_id"`
	NewThreadID     string `json:"new_thread_id"`
	AgentID         string `json:"agent_id,omitempty"`
	AgentKey        string `json:"agent_key,omitempty"`
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
	AgentID     string
	Name        string
	ParentID    string
	AgentType   string
	MemoryScope string
	Cwd         string
	Command     []string
	Env         []string
}

type Ref struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

type ReadHistoryThread struct {
	ThreadID string `json:"thread_id"`
}

type ReadHistoryResult struct {
	History []ReadHistoryThread `json:"history"`
}
