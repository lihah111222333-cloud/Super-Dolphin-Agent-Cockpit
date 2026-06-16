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

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// ErrNotFound is returned by GetLive when no live row matches the
// requested dedupe_key. Callers MUST treat this as "never submitted"
// per the plan.
var ErrNotFound = contract.ErrTurnDedupeNotFound

// Entry is the domain DTO for a turn_dedupe_registry row. TerminalAt
// is zero when the row is still live; callers that care about dead
// vs. missing rows check ErrNotFound first.
type Entry = contract.TurnDedupeEntry

// Store is the persistence surface. The default production wiring
// uses the sqlc-backed implementation in this package; tests that
// don't want a real DB use noopStore via NewNoop.
type Store = contract.TurnDedupeStore

// UpsertParams drives Upsert. Empty ThreadID is treated as "leave
// existing value alone" at the SQL layer so a lookup-then-register
// flow that doesn't know the thread id can safely call Upsert
// repeatedly.
type UpsertParams = contract.TurnDedupeUpsertParams

// BindProviderTurnIDParams drives BindProviderTurnID.
type BindProviderTurnIDParams = contract.TurnDedupeBindProviderTurnIDParams
