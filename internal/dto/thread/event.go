package thread

import shared "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"

// Started reports a thread becoming active and routable.
// PendingLaunch=true means the backend created a placeholder row but has not
// forked the provider CLI yet; the actual spawn happens on the first turn via
// SpawnIfNeeded and is reported as a separate Launched event.
type Started struct {
	shared.EventHeader
	ThreadID         string `json:"thread_id"`
	AgentID          string `json:"agent_id,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
	Name             string `json:"name,omitempty"`
	PendingLaunch    bool   `json:"pending_launch,omitempty"`
}

// Launched reports that a previously pending_launch thread has successfully
// spawned its provider CLI. Carries the router decision made at spawn time.
type Launched struct {
	shared.EventHeader
	ThreadID         string `json:"thread_id"`
	AgentID          string `json:"agent_id,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
	Name             string `json:"name,omitempty"`
	AgentKey         string `json:"agent_key,omitempty"`
	PromptVersionID  *int64 `json:"prompt_version_id,omitempty"`
}

// Stopped reports a thread becoming inactive.
type Stopped struct {
	shared.EventHeader
	ThreadID string `json:"thread_id"`
	AgentID  string `json:"agent_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// MessagesPage reports a thread message page refresh.
type MessagesPage struct {
	shared.EventHeader
	ThreadID   string `json:"thread_id"`
	TotalCount int    `json:"total_count"`
	Pages      int    `json:"pages"`
}

// Compacted reports a thread compact lifecycle completion.
type Compacted struct {
	shared.EventHeader
	ThreadID     string `json:"thread_id"`
	Command      string `json:"command,omitempty"`
	BeforeTokens int    `json:"before_tokens,omitempty"`
	AfterTokens  int    `json:"after_tokens,omitempty"`
	Compacted    bool   `json:"compacted"`
	Estimated    bool   `json:"estimated,omitempty"`
}

// Updated reports a thread modification such as a name or model change.
type Updated struct {
	shared.EventHeader
	ThreadID string  `json:"thread_id"`
	Name     string  `json:"name"`
	Model    *string `json:"model,omitempty"`
}

// SpawnRouting carries the router decision made inside the lazy
// SpawnIfNeeded path for a pending-launch thread. It lives in this shared
// dto package so both the thread module (which produces it) and the turn
// module (whose turn/start handler forwards it to the UI) can reference
// the same type without re-introducing a thread↔turn import cycle.
//
// Empty SpawnRouting means the SpawnIfNeeded call was a no-op (thread was
// already running, stopped, or archived). Non-empty means a fresh spawn
// just ran and these are its routing outputs; the UI uses them to fill
// the per-thread routing badge that thread/start could not surface, since
// pending_launch threads defer routing to the first turn.
type SpawnRouting struct {
	AgentKey string `json:"agent_key,omitempty"`
	// AgentTitle is the human-readable persona label ("SQL 与数据建模专家") so
	// the UI does not have to re-map slugs to names.
	AgentTitle      string `json:"agent_title,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	PromptKeyStale  bool   `json:"prompt_key_stale,omitempty"`
}

// Type 返回事件分发用的类型编号。
func (Started) Type() uint32 { return shared.EventTypeThreadStarted }

// Type 返回事件分发用的类型编号。
func (Stopped) Type() uint32 { return shared.EventTypeThreadStopped }

// Type 返回事件分发用的类型编号。
func (MessagesPage) Type() uint32 { return shared.EventTypeThreadMessagesPage }

// Type 返回事件分发用的类型编号。
func (Compacted) Type() uint32 { return shared.EventTypeThreadCompacted }

// Type 返回事件分发用的类型编号。
func (Updated) Type() uint32 { return shared.EventTypeThreadUpdated }

// Type 返回事件分发用的类型编号。
func (Launched) Type() uint32 { return shared.EventTypeThreadLaunched }
