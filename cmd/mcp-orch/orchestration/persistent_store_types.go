package orchestration

import "context"

// PersistedThread is a local DTO that mirrors the subset of fields
// this package needs from the thread store. Decouples orchestration
// from internal/store/thread per modularity-convention §2.4.
type PersistedThread struct {
	ThreadID      string
	AgentID       string
	ParentAgentID string
	Name          string
	Prompt        string
	Cwd           string
	Status        string
	Port          int32
	PID           int32
	CreatedAt     int64
	UpdatedAt     int64
	PendingLaunch bool
}

// PersistedBinding is a local DTO that mirrors the subset of fields
// this package needs from the binding store. Decouples orchestration
// from internal/store/binding per modularity-convention §2.4.
type PersistedBinding struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	Cwd              string
	Archived         bool
	CreatedAt        int64
	UpdatedAt        int64
}

// AgentThreadStore abstracts thread persistence. The adapter is wired
// in cmd/mcp-orch/runtime.go so this package never imports internal/store/*.
type AgentThreadStore interface {
	ListAll(ctx context.Context) ([]PersistedThread, error)
	GetByThreadID(ctx context.Context, threadID string) (*PersistedThread, error)
}

// AgentBindingStore abstracts binding persistence. The adapter is wired
// in cmd/mcp-orch/runtime.go so this package never imports internal/store/*.
type AgentBindingStore interface {
	GetByAgentID(ctx context.Context, agentID string) (*PersistedBinding, error)
}
