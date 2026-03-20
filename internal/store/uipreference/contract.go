package uipreference

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
	Upsert(ctx context.Context, params UpsertParams) error
	List(ctx context.Context, cwd string) ([]UIPreference, error)
}

type UpsertParams struct {
	Cwd   string
	Key   string
	Value json.RawMessage
}

type UIPreference struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
	Cwd       string
}
