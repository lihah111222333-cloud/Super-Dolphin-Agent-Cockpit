package sharedfile

import (
	"context"
	"time"
)

type Reader interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter ListFilter) ([]SharedFile, error)
}

type ListFilter struct {
	Prefix string
	Limit  int32
}

type SharedFile struct {
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedBy string    `json:"updated_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store interface {
	Reader
	Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error)
	Delete(ctx context.Context, path string) (int64, error)
}

type UpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

type Importer interface {
	ImportLocalFile(ctx context.Context, params ImportLocalFileParams) (*SharedFile, error)
}

type ImportLocalFileParams struct {
	SourcePath         string
	TargetPath         string
	ContentType        string
	AllowedExtensions  []string
	AllowedSourceRoots []string
	MaxBytes           int64
	Overwrite          string
	UpdatedBy          string
}
