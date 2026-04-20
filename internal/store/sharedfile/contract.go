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

// Deleter removes a shared file by path. Returns the number of rows deleted.
type Deleter interface {
	Delete(ctx context.Context, path string) (int64, error)
}

// Store combines read and mutation access to shared files.
type Store interface {
	Reader
	Deleter
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
