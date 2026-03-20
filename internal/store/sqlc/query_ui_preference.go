package sqlc

import (
	"context"
	"encoding/json"
)

const (
	getUIPreferenceValueSQL = `SELECT value FROM ui_preferences WHERE cwd = $1 AND key = $2;`
	upsertUIPreferenceSQL   = `INSERT INTO ui_preferences (cwd, key, value, updated_at) VALUES ($1, $2, $3, NOW()) ON CONFLICT (cwd, key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();`
	listUIPreferencesSQL    = `SELECT key, value, cwd, updated_at FROM ui_preferences WHERE cwd = '' OR ($1::text <> '' AND cwd = $1) ORDER BY cwd ASC, key ASC;`
)

func scanUIPreference(row rowScanner) (UIPreference, error) {
	var item UIPreference
	err := row.Scan(&item.Key, &item.Value, &item.Cwd, &item.UpdatedAt)
	return item, err
}

func (q *Queries) GetUIPreferenceValue(ctx context.Context, arg GetUIPreferenceValueParams) (json.RawMessage, error) {
	return queryOne(ctx, q, getUIPreferenceValueSQL, scanValue[json.RawMessage], arg.Cwd, arg.Key)
}

func (q *Queries) UpsertUIPreference(ctx context.Context, arg UpsertUIPreferenceParams) error {
	return q.exec(ctx, upsertUIPreferenceSQL, arg.Cwd, arg.Key, arg.Value)
}

func (q *Queries) ListUIPreferences(ctx context.Context, cwd string) ([]UIPreference, error) {
	return queryMany(ctx, q, listUIPreferencesSQL, scanUIPreference, cwd)
}
