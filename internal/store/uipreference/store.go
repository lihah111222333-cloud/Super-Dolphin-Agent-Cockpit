package uipreference

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store {
	return &store{q: q}
}

func (s *store) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	return s.q.GetUIPreferenceValue(ctx, sqlc.GetUIPreferenceValueParams{
		Cwd: cwd,
		Key: key,
	})
}

func (s *store) Upsert(ctx context.Context, params UpsertParams) error {
	return s.q.UpsertUIPreference(ctx, sqlc.UpsertUIPreferenceParams{
		Cwd:   params.Cwd,
		Key:   params.Key,
		Value: params.Value,
	})
}

func (s *store) List(ctx context.Context, cwd string) ([]UIPreference, error) {
	rows, err := s.q.ListUIPreferences(ctx, cwd)
	if err != nil {
		return nil, err
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
