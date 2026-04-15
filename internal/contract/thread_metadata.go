package contract

import (
	"context"
	"encoding/json"
)

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
