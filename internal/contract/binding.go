package contract

import "context"

// BindingStore persists agent/thread/provider bindings used to resume and
// route running sessions.
type BindingStore interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error)
	Upsert(ctx context.Context, params BindingUpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params BindingUpdateSessionUUIDParams) error
	UpdateProviderThreadID(ctx context.Context, params BindingUpdateProviderThreadIDParams) error
	SetArchived(ctx context.Context, params BindingSetArchivedParams) error
	GetByAgentID(ctx context.Context, agentID string) (*Binding, error)
	BindAgentThread(ctx context.Context, params BindingBindAgentThreadParams) error
	UnbindAgentThread(ctx context.Context, agentID string) error
	ListAgentThreadBindings(ctx context.Context) ([]Binding, error)
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
	UpdateAgentCwd(ctx context.Context, params BindingUpdateAgentCwdParams) error
	Rebind(ctx context.Context, params BindingRebindParams) error
	ListProviderMap(ctx context.Context) (map[string]string, error)
	ListCwdMap(ctx context.Context) (map[string]string, error)
}

// BindingRebindParams moves an agent binding to a new thread/cwd tuple.
type BindingRebindParams struct {
	AgentID   string
	ThreadID  string
	Cwd       string
	UpdatedAt int64
}

// BindingUpsertParams inserts or repairs a binding row.
type BindingUpsertParams struct {
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

	// CodexHome, CodexInstanceKey, and CodexModelProvider identify the local
	// Codex instance that owns the binding. Empty values mean "leave existing
	// value alone" on compatible repair paths.
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// BindingUpdateSessionUUIDParams updates the session UUID for an agent.
type BindingUpdateSessionUUIDParams struct {
	SessionUUID string
	UpdatedAt   int64
	AgentID     string
}

// BindingUpdateProviderThreadIDParams updates the provider thread id.
type BindingUpdateProviderThreadIDParams struct {
	ProviderThreadID string
	UpdatedAt        int64
	AgentID          string
}

// BindingSetArchivedParams archives or unarchives an agent binding.
type BindingSetArchivedParams struct {
	AgentID   string
	Archived  bool
	UpdatedAt int64
}

// BindingBindAgentThreadParams binds an agent id to a public thread id.
type BindingBindAgentThreadParams struct {
	AgentID   string
	ThreadID  string
	Cwd       string
	CreatedAt int64
	UpdatedAt int64
}

// BindingUpdateAgentCwdParams updates the persisted cwd for an agent binding.
type BindingUpdateAgentCwdParams struct {
	AgentID   string
	Cwd       string
	UpdatedAt int64
}

// Binding is the cross-layer projection of an agent/thread/provider binding.
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

	// CodexHome, CodexInstanceKey, and CodexModelProvider identify the local
	// Codex instance that owns the binding for restart-safe routing.
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}
