package thread

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

// SpawnRouting re-exports the shared dto type so in-package call sites can
// keep using the short `thread.SpawnRouting` name. The canonical definition
// lives in internal/dto/thread/event.go to avoid a thread↔turn import cycle.
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
	// SpawnIfNeeded forks the provider CLI for a thread that was created
	// in pending_launch state (see Start with empty prompt). Safe to call
	// concurrently; only the first caller per thread actually spawns.
	// Returns launched=true iff this call performed the spawn. When
	// launched=true the returned SpawnRouting carries the routing decision
	// (agent_key / prompt_key / prompt_version_id)
	// so callers such as turn/start can forward it to the UI — pending-spawn
	// threads never surface this on thread/start since routing runs lazily.
	// requestCWD is validation-only; the pending row's stored cwd remains
	// authoritative and mismatches fail before provider launch side effects.
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
	// Deprecated: use Name for display-name semantics; Prompt is kept only for legacy callers.
	Prompt, BaseInstructions string
	// BaseInstructionBlocks is populated by resolveRoutedPrompt when the
	// picked prompt_template has rows in prompt_template_sections. It flows
	// through buildStartAssemblyInput into contract.StartInput so the
	// assembler can merge the blocks into resolved sections (region-aware
	// cached/uncached split). Empty means legacy monolithic-text behavior.
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
	// LaunchSkillNames / ForceLaunchSkills are legacy additive launch-time
	// skill selection fields. They remain on the public payload for backward
	// compatibility; V1 skill runtime uses provider-native mirrors instead of
	// prompt catalog injection.
	LaunchSkillNames  []string
	LaunchSkillRefs   []dto.SkillRef
	ForceLaunchSkills bool

	// AgentKey is optional. When non-empty, the service skips router
	// classification and uses this agent_key directly. Empty + empty
	// BaseInstructions triggers router.
	AgentKey string
	// PromptVersionID is filled by the service after it materializes a
	// prompt_versions row for this thread start; it is not an input.
	PromptVersionID *int64
	// PromptKey may be supplied by the caller as an explicit "use this exact
	// prompt_template" pin (UI's launch-prompt preference). When non-empty
	// and pointing at an enabled row, it takes precedence over AgentKey
	// routing. When the caller leaves it empty, resolveRoutedPrompt fills it
	// with the picked template's key for downstream observability and UI
	// display (agent_key is the role slug; prompt_key is the specific row).
	PromptKey string
	// AgentTitle is filled by resolveRoutedPrompt from picked.Title once the
	// router settles on a template. Not an input. Surfaced to the UI so the
	// sidebar can show a human-readable persona label ("SQL 与数据建模专家")
	// next to the thread name instead of the opaque agent_key slug.
	AgentTitle string
	// PromptKeyStale is set to true by pickRoutedTemplate when the caller
	// supplied a non-empty PromptKey that does not resolve to any enabled
	// prompt_template row (either the row was deleted or its Enabled flag
	// was flipped off). It propagates through newStartResult into the RPC
	// response so the UI can self-clean its activePromptKey preference and
	// notify the user. Not an input. Must remain false for the wire-degrade
	// path (no promptStore wired) — that case is a transient backend issue
	// rather than a stale pin and the UI must not clear the user's pref.
	PromptKeyStale                bool
	OwnerThreadID, LaunchIntentID string

	// DeferSpawn opts into the C1 "pending_launch" flow: the service writes
	// the agent_threads row without forking the provider CLI and returns a
	// StartResult with PendingLaunch=true. The real spawn happens lazily in
	// SpawnIfNeeded once turn/start arrives with real user input that router
	// can classify. Legacy callers (Handoff, Resume, tests) leave this false
	// and get the eager fork path unchanged.
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
	// Routing metadata surfaced to the UI so the sidebar can show which agent
	// the router picked, which prompt_template hit, and which prompt_versions
	// row was injected.
	AgentKey string `json:"agent_key,omitempty"`
	// AgentTitle is the human-readable persona label ("SQL 与数据建模专家") that
	// the UI shows on the routing badge. Equal to the picked template's
	// Title. Empty when the caller took a path that did not touch the router.
	AgentTitle      string `json:"agent_title,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	// PromptKeyStale mirrors StartRequest.PromptKeyStale: when true, the
	// caller-supplied prompt_key did not resolve to an enabled template row
	// and the UI should clear its activePromptKey preference. False (zero
	// value) means the pin resolved successfully or the caller never pinned
	// anything; either way the UI must preserve the pref unchanged.
	PromptKeyStale bool `json:"prompt_key_stale,omitempty"`
	// PendingLaunch=true means the backend wrote the thread row but did not
	// fork the provider CLI yet. The real spawn happens on the first turn,
	// once router has a real user input to classify. UI should render such
	// threads with a "pending" marker and flip to running on thread.launched.
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

// HandoffRequest moves the active conversation from one agent to another.
// The service keeps the source thread running (user may return) and starts
// a fresh thread for the target agent, linked back via OwnerThreadID.
type HandoffRequest struct {
	SourceThreadID, TargetAgentKey string
	// Optional initial message to seed the new thread's display name.
	// Empty falls back to "handoff from <source>".
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
