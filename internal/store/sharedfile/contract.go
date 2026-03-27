package sharedfile

import (
	"context"
	"time"
)

// Reader provides read-only access to shared files.
// This is the shared interface consumed by both internal modules and cmd/mcp-orch.
type Reader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter ListFilter) ([]SharedFile, error)
}

type ListFilter struct {
	Prefix string
	Limit  int32
}

// SharedFile is the shared domain DTO for shared files.
type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
