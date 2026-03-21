package uipreference

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	value, err := s.q.GetUIPreferenceValue(ctx, sqlc.GetUIPreferenceValueParams{
		Cwd: cwd,
		Key: key,
	})
	if err != nil {
		return nil, wrapUIPreferenceError(err, "get")
	}
	return value, nil
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	return wrapUIPreferenceError(s.q.UpsertUIPreference(ctx, sqlc.UpsertUIPreferenceParams{
		Cwd:   params.Cwd,
		Key:   params.Key,
		Value: params.Value,
	}), "upsert")
}

func (s *store) List(ctx context.Context, cwd string) ([]UIPreference, error) {
	rows, err := s.q.ListUIPreferences(ctx, cwd)
	if err != nil {
		return nil, wrapUIPreferenceError(err, "list")
	}
	result := make([]UIPreference, len(rows))
	for i, row := range rows {
		result[i] = UIPreference{
			Key:       row.Key,
			Value:     row.Value,
			UpdatedAt: row.UpdatedAt,
			Cwd:       row.Cwd,
		}
	}
	return result, nil
}

func wrapUIPreferenceError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "ui_preference")
}
