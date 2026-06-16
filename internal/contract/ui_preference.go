package contract

import (
	"context"
	"encoding/json"
	"time"
)

// UIPreferenceStore is the persistence port for scoped UI preferences.
type UIPreferenceStore interface {
	// GetValue returns one preference value for a cwd/key pair.
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
	// Upsert saves one preference value for a cwd/key pair.
	Upsert(ctx context.Context, params UIPreferenceUpsertParams) error
	// List returns all preferences scoped to cwd.
	List(ctx context.Context, cwd string) ([]UIPreference, error)
}

// UIPreferenceUpsertParams drives UIPreferenceStore.Upsert.
type UIPreferenceUpsertParams struct {
	Cwd   string
	Key   string
	Value json.RawMessage
}

// UIPreference is a scoped UI preference row.
type UIPreference struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
	Cwd       string
}
