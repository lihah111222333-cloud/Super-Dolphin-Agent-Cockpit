package binding

import (
	"context"
)

type Store interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error)
	Upsert(ctx context.Context, params UpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error
	SetArchived(ctx context.Context, params SetArchivedParams) error
	GetByAgentID(ctx context.Context, agentID string) (*Binding, error)
	BindAgentThread(ctx context.Context, params BindAgentThreadParams) error
	UnbindAgentThread(ctx context.Context, agentID string) error
	ListAgentThreadBindings(ctx context.Context) ([]Binding, error)
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
	UpdateAgentCwd(ctx context.Context, params UpdateAgentCwdParams) error
}

type UpsertParams struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	Cwd              string
	CreatedAt        int64
	UpdatedAt        int64
}

type UpdateSessionUUIDParams struct {
	SessionUUID string
	UpdatedAt   int64
	AgentID     string
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
	Archived         bool
	CreatedAt        int64
	UpdatedAt        int64
	SessionUUID      string
}
