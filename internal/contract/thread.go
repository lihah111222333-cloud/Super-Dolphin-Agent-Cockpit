package contract

import (
	"context"
	"encoding/json"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ---------------------------------------------------------------------------
// ThreadMetadata (was thread_metadata.go)
// ---------------------------------------------------------------------------

// ThreadMetadata carries the read-only thread fields consumed outside store/thread.
type ThreadMetadata struct {
	ThreadID         string
	ParentAgentID    string
	AgentMemoryScope string
	Cwd              string
	CreatedAt        int64
	UpdatedAt        int64
	FinishedAt       *int64
	OwnerThreadID    string
	ConfigOverride   json.RawMessage
}

// ThreadMetadataStore provides the minimal thread lookup surface needed by memory.
type ThreadMetadataStore interface {
	GetByThreadID(ctx context.Context, threadID string) (*ThreadMetadata, error)
	ListAll(ctx context.Context) ([]ThreadMetadata, error)
}

// ---------------------------------------------------------------------------
// ThreadService contracts (was thread_service.go)
// ---------------------------------------------------------------------------

// ThreadRef is the narrow projection of thread.Ref that consumers outside
// the thread module need (list / summarize). Keeping it in contract avoids
// a lateral module→module import.
type ThreadRef struct {
	ID      string
	Name    string
	AgentID string
	Status  string
}

// ThreadLister is the read-only subset of thread.Service that the uistate
// module uses to build the initial sidebar state.
// Satisfied by the concrete thread.Service implementation.
type ThreadLister interface {
	List(ctx context.Context) ([]ThreadRef, error)
}

// ThreadConfigReader is the narrow subset of thread.Service that the
// uistate config-read handler depends on: reading the per-thread
// effective config (model / approvals) and optional runtime config map.
// Satisfied by the concrete thread.Service implementation via an adapter.
type ThreadConfigReader interface {
	GetConfig(ctx context.Context, threadID string) (dto.ThreadConfig, error)
}

// ThreadRuntimeConfigReader is the optional extension of ThreadConfigReader
// that also supports reading the raw runtime config map. The uistate
// config handler probes for this via type assertion.
type ThreadRuntimeConfigReader interface {
	ReadRuntimeConfig(ctx context.Context, threadID string) (map[string]any, error)
}
