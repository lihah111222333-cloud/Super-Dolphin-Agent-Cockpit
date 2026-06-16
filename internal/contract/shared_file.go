package contract

import (
	"context"
	"time"
)

// SharedFileReader provides read-only access to shared files.
type SharedFileReader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter SharedFileListFilter) ([]SharedFile, error)
}

// SharedFileUpserter writes or overwrites a shared file by path.
type SharedFileUpserter interface {
	Upsert(ctx context.Context, params SharedFileUpsertParams) (*SharedFile, error)
}

// SharedFileDeleter removes a shared file by path and returns affected rows.
type SharedFileDeleter interface {
	Delete(ctx context.Context, path string) (int64, error)
}

// SharedFileStore combines read and mutation access to shared files.
type SharedFileStore interface {
	SharedFileReader
	SharedFileUpserter
	SharedFileDeleter
}

// SharedFileUpsertParams drives SharedFileUpserter.Upsert.
type SharedFileUpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

// SharedFileListFilter constrains shared-file list queries.
type SharedFileListFilter struct {
	Prefix string
	Limit  int32
}

// SharedFile is the cross-layer projection of a shared file.
type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
