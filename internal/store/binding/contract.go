package binding

import (
	"context"
)

// TODO(p7w2): migrate the remaining legacy threadbinding query surface into
// sql/queries once the compatibility shape is finalized.
type Store interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error)
	Upsert(ctx context.Context, params UpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error
	SetArchived(ctx context.Context, params SetArchivedParams) error
	GetByAgentID(ctx context.Context, agentID string) (*Binding, error)
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
