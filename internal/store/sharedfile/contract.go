package sharedfile

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Store interface {
	Get(ctx context.Context, path string) (*SharedFile, error)
	List(ctx context.Context, filter ListFilter) ([]SharedFile, error)
}

type ListFilter struct {
	Prefix string
	Limit  int32
}

type SharedFile = sqlc.SharedFile
