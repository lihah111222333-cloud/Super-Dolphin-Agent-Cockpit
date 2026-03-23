package sharedfile

import (
	"context"
	"time"
)

type Store interface {
	Upsert(ctx context.Context, params UpsertParams) (*SharedFile, error)
	Get(ctx context.Context, path string) (*SharedFile, error)
}

type UpsertParams struct {
	Path      string
	Content   string
	UpdatedBy string
}

type SharedFile struct {
	Path      string
	Content   string
	UpdatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}
