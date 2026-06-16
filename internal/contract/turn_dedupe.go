package contract

import (
	"context"
	"errors"
	"time"
)

// ErrTurnDedupeNotFound is returned when no live row matches a turn
// dedupe key.
var ErrTurnDedupeNotFound = errors.New("turn dedupe: no live registry row")

// TurnDedupeEntry is the durable projection of a dedupe_key ->
// local_turn_id registry row.
type TurnDedupeEntry struct {
	DedupeKey      string
	LocalTurnID    string
	ProviderTurnID string
	ThreadID       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TerminalAt     time.Time
}

// TurnDedupeStore is the persistence port used by the turn module to mirror
// in-memory dedupe tracking into durable storage.
type TurnDedupeStore interface {
	// Upsert writes or refreshes the registry row for a dedupe key.
	Upsert(ctx context.Context, params TurnDedupeUpsertParams) error
	// BindProviderTurnID records the provider turn id for an existing row.
	BindProviderTurnID(ctx context.Context, params TurnDedupeBindProviderTurnIDParams) error
	// MarkTerminal marks the row terminal so later live lookups skip it.
	MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error
	// GetLive returns the live row for a dedupe key or ErrTurnDedupeNotFound.
	GetLive(ctx context.Context, dedupeKey string) (TurnDedupeEntry, error)
	// Sweep deletes rows whose update timestamp is older than cutoff.
	Sweep(ctx context.Context, cutoff time.Time) error
}

// TurnDedupeUpsertParams drives TurnDedupeStore.Upsert.
type TurnDedupeUpsertParams struct {
	DedupeKey   string
	LocalTurnID string
	ThreadID    string
	Now         time.Time
}

// TurnDedupeBindProviderTurnIDParams drives
// TurnDedupeStore.BindProviderTurnID.
type TurnDedupeBindProviderTurnIDParams struct {
	DedupeKey      string
	ProviderTurnID string
	Now            time.Time
}
