package sharedfile

import (
	"context"
	"time"
)

type Store interface {
	Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error)
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter ListFilter) ([]SharedFile, error)
	Delete(ctx context.Context, path string) (int64, error)
}

type UpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

type ListFilter struct {
	Prefix string
	Limit  int32
}

type SharedFile struct {
	Path      string
	Content   string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
