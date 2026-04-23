// Package turndedupe persists the dedupe_key -> local_turn_id mapping
// that lets cron crash-recovery survive a mcp-orch process restart.
//
// Scope: this is the smallest durable shim that complements the
// in-memory tracker in internal/module/turn. The tracker handles the
// happy path; the registry is consulted only when the tracker
// misses — which is exactly the post-restart case per the P21 P1b
// plan.
//
// See migrations/0060_turn_dedupe_registry.sql for the schema and
// lifetime contract.
package turndedupe

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by GetLive when no live row matches the
// requested dedupe_key. Callers MUST treat this as "never submitted"
// per the plan.
var ErrNotFound = errors.New("turndedupe: no live registry row")

// Entry is the domain DTO for a turn_dedupe_registry row. TerminalAt
// is zero when the row is still live; callers that care about dead
// vs. missing rows check ErrNotFound first.
type Entry struct {
	DedupeKey      string
	LocalTurnID    string
	ProviderTurnID string
	ThreadID       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	TerminalAt     time.Time
}

// Store is the persistence surface. The default production wiring
// uses the sqlc-backed implementation in this package; tests that
// don't want a real DB use noopStore via NewNoop.
type Store interface {
	// Upsert writes or refreshes the registry row for dedupeKey. A
	// conflict resets terminal_at to NULL so a re-used key that was
	// previously marked terminal is "resurrected" — mirrors the
	// tracker's last-wins RegisterDedupeKey semantics.
	Upsert(ctx context.Context, params UpsertParams) error
	// BindProviderTurnID updates the provider_turn_id on an existing
	// row, leaving local_turn_id / terminal_at untouched.
	BindProviderTurnID(ctx context.Context, params BindProviderTurnIDParams) error
	// MarkTerminal stamps terminal_at = now on the row so subsequent
	// GetLive calls skip it. A row that is already terminal is left
	// alone (no-op).
	MarkTerminal(ctx context.Context, dedupeKey string, now time.Time) error
	// GetLive returns the live row for dedupeKey or ErrNotFound when
	// nothing matches / all matching rows are terminal.
	GetLive(ctx context.Context, dedupeKey string) (Entry, error)
	// Sweep deletes rows whose updated_at is older than cutoff.
	// Returned error is surfaced up for logging; the scheduler keeps
	// running regardless.
	Sweep(ctx context.Context, cutoff time.Time) error
}

// UpsertParams drives Upsert. Empty ThreadID is treated as "leave
// existing value alone" at the SQL layer so a lookup-then-register
// flow that doesn't know the thread id can safely call Upsert
// repeatedly.
type UpsertParams struct {
	DedupeKey   string
	LocalTurnID string
	ThreadID    string
	Now         time.Time
}

// BindProviderTurnIDParams drives BindProviderTurnID.
type BindProviderTurnIDParams struct {
	DedupeKey      string
	ProviderTurnID string
	Now            time.Time
}
