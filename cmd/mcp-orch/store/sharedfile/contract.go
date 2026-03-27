package sharedfile

import (
	"context"

	sf "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

// Re-export shared types from internal/store/sharedfile.
type SharedFile = sf.SharedFile
type ListFilter = sf.ListFilter

// Store extends the shared Reader with write operations.
type Store interface {
	sf.Reader
	Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error)
	Delete(ctx context.Context, path string) (int64, error)
}

type UpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}
