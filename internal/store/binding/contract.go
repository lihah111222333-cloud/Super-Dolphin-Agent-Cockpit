package binding

import (
	"context"
)

type Store interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error)
	Upsert(ctx context.Context, params UpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error
	UpdateProviderThreadID(ctx context.Context, params UpdateProviderThreadIDParams) error
	SetArchived(ctx context.Context, params SetArchivedParams) error
	GetByAgentID(ctx context.Context, agentID string) (*Binding, error)
	BindAgentThread(ctx context.Context, params BindAgentThreadParams) error
	UnbindAgentThread(ctx context.Context, agentID string) error
	ListAgentThreadBindings(ctx context.Context) ([]Binding, error)
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
	UpdateAgentCwd(ctx context.Context, params UpdateAgentCwdParams) error
	Rebind(ctx context.Context, params RebindParams) error
	ListProviderMap(ctx context.Context) (map[string]string, error)
	ListCwdMap(ctx context.Context) (map[string]string, error)
}

type RebindParams struct {
	AgentID   string
	ThreadID  string
	Cwd       string
	UpdatedAt int64
}

type UpsertParams struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	SessionUUID      string
	Cwd              string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	CreatedAt        int64
	UpdatedAt        int64

	// Codex instance identity (P21 P1a). Empty "" means "leave existing
	// value alone" on UPSERT. The tuple fields are immutable once non-empty;
	// non-empty CodexHome repair must be caller-validated as a same-tuple alias.
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

type UpdateSessionUUIDParams struct {
	SessionUUID string
	UpdatedAt   int64
	AgentID     string
}

type UpdateProviderThreadIDParams struct {
	ProviderThreadID string
	UpdatedAt        int64
	AgentID          string
}

type SetArchivedParams struct {
	AgentID   string
	Archived  bool
	UpdatedAt int64
}

type BindAgentThreadParams struct {
	AgentID   string
	ThreadID  string
	Cwd       string
	CreatedAt int64
	UpdatedAt int64
}

type UpdateAgentCwdParams struct {
	AgentID   string
	Cwd       string
	UpdatedAt int64
}

type Binding struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	Cwd              string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Archived         bool
	CreatedAt        int64
	UpdatedAt        int64
	SessionUUID      string

	// Codex instance identity (P21 P1a). Persisted canonicalized realpath
	// + explicit instance key + provider alias so auto-resume can route
	// back to the correct local app-server process after a restart.
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}
