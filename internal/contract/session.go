package contract

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// SessionStarter is the contract for starting and resuming provider sessions.
// The production implementation lives in provider/unified.Client.
type SessionStarter interface {
	StartSession(ctx context.Context, req dto.StartSessionRequest) (Session, error)
	ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (Session, error)
}

// SessionProvider narrows session lookup to keep consumer modules provider-neutral.
// Used by the thread module.
type SessionProvider interface {
	GetSession(agentID string) (Session, error)
	RemoveSession(agentID string)
}

// SessionResolver resolves a thread ID to its active session (was session_resolver.go).
type SessionResolver interface {
	ResolveSession(ctx context.Context, threadID string) (Session, error)
}

type SessionRecoveryReporter interface {
	ClearStaleProviderThreadID(ctx context.Context, agentID string) error
	RecordProviderSessionUUID(ctx context.Context, agentID, sessionUUID string) error
}

// ---------------------------------------------------------------------------
// SessionThreadRef / SessionBinding — narrow projections consumed by
// the session-resolver inside provider/unified so it never imports store.
// ---------------------------------------------------------------------------

// SessionThreadRef is the minimal thread projection the session resolver
// needs: just the thread-to-agent mapping.
type SessionThreadRef struct {
	ThreadID      string
	AgentID       string
	Status        string
	RuntimeConfig map[string]any
}

// SessionBinding is the minimal binding projection the session resolver
// needs for auto-resume after restart.
type SessionBinding struct {
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
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// SessionThreadLookup resolves a public thread ID to a SessionThreadRef.
// Satisfied by the store/thread adapter.
type SessionThreadLookup interface {
	GetByThreadID(ctx context.Context, threadID string) (*SessionThreadRef, error)
}

// SessionBindingLookup resolves bindings for session auto-resume.
// Satisfied by the store/binding adapter.
type SessionBindingLookup interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*SessionBinding, error)
	GetByAgentID(ctx context.Context, agentID string) (*SessionBinding, error)
}

// SessionBindingUpserter repairs durable binding projections after provider
// auto-resume has recovered missing legacy identity columns.
type SessionBindingUpserter interface {
	UpsertSessionBinding(ctx context.Context, binding SessionBinding) error
}

// ---------------------------------------------------------------------------
// TurnThreadCleaner (was turn_thread_cleaner.go)
// ---------------------------------------------------------------------------

// TurnThreadCleaner is the narrow contract consumed by the thread module to
// interrupt running turns and clean up turn state when a thread is
// stopped / archived / deleted.  The production implementation is
// turn.Service; the interface lives in contract so thread never imports
// internal/module/turn directly (onion-layer rule).
type TurnThreadCleaner interface {
	// InterruptActiveTurn cancels the in-flight turn (if any) for the
	// given session.  source identifies the caller for observability
	// (e.g. "thread_stopped", "thread_deleted").
	InterruptActiveTurn(ctx context.Context, session Session, source string) error
	// CleanupThread removes all tracked turn state for threadID.
	// reason is an observability tag (e.g. "thread_stopped").
	CleanupThread(ctx context.Context, threadID, reason string) error
}
