// Package observation owns the Canonical Turn Observation Contract for the
// P21 plans (P0b / P3). Observation normalizes the raw + typed event streams
// into six facts that downstream consumers read:
//
//  1. local turn id <-> provider turn id mapping
//  2. call id -> owning local turn id mapping
//  3. skills_selected (PrepareTurn resolver output; not "model used")
//  4. token snapshot with zero-event preservation
//  5. terminal precedence (interrupted/aborted is sticky)
//  6. raw vs typed event de-duplication
//
// Consumers (P3 session insights, P0b skill extractor, ...) must only read
// from this layer; they must not re-implement turn attribution on top of raw
// + typed events. Observation is wired as an independent fx.Invoke subscriber
// and pushes facts in one direction — turn/tracker must not depend on this
// package.
package observation

import "time"

// TerminalKind classifies how a turn ended.
type TerminalKind string

const (
	TerminalUnknown     TerminalKind = ""
	TerminalCompleted   TerminalKind = "completed"
	TerminalStalled     TerminalKind = "stalled"
	TerminalFailed      TerminalKind = "failed"
	TerminalInterrupted TerminalKind = "interrupted"
	TerminalAborted     TerminalKind = "aborted"
)

// precedence returns ordering used by RecordTerminal. Higher wins. Locked
// kinds (Interrupted / Aborted) are sticky even against same-precedence
// overwrites; see Memory.RecordTerminal.
func (k TerminalKind) precedence() int {
	switch k {
	case TerminalInterrupted, TerminalAborted:
		return 5
	case TerminalFailed:
		return 4
	case TerminalStalled:
		return 3
	case TerminalCompleted:
		return 2
	default:
		return 0
	}
}

// Terminal is one observed terminal event. Success is a pointer so callers
// can distinguish "unknown" from "false"; TurnCompleted.Success in the DTO
// layer is a non-pointer bool and must be unwrapped before reaching here.
type Terminal struct {
	Kind    TerminalKind
	Success *bool
	Reason  string
}

// TokenSnapshot is a normalized per-turn token accounting fact. Zero fields
// mean "not observed in this event" and must not overwrite previously
// observed non-zero values. Projection records the UI projection granularity
// of the source event ("thread" / "turn" / ...); consumers that want strict
// per-turn accounting should ignore snapshots with Projection="thread".
type TokenSnapshot struct {
	Input               int64
	Output              int64
	Total               int64
	ContextWindowTokens int64
	Projection          string
	Observed            bool
}

// DedupeKey identifies a single raw or typed event for de-duplication.
// By convention exactly one of RawEventID / CallID / Key is set.
type DedupeKey struct {
	RawEventID string
	CallID     string
	Key        string
}

// Counts is the per-turn aggregate of observed tool / approval activity.
// Observed flags distinguish "provider did not emit the event family"
// (false) from "observed and the count is legitimately zero" (true,
// value 0). ApprovalRequestsObserved flips true the first time an
// approval event is recorded — the Claude path never emits these so a
// collector reading Counts for a Claude turn sees observed=false.
type Counts struct {
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
}

// Timestamps records the first-observed start and the latest-observed
// completion for a turn. Either field can be zero when the corresponding
// event has not arrived yet.
type Timestamps struct {
	StartedAt   time.Time
	CompletedAt time.Time
}

// ObservationReader is the read-only facet consumed by trajectory collectors,
// insight flushers, and extractors. It deliberately excludes mutation and
// dedupe methods.
type ObservationReader interface {
	ResolveLocalTurn(providerID string) (localID string, ok bool)
	ResolveProviderTurn(localID string) (providerID string, ok bool)
	LookupCall(callID string) (localTurnID string, ok bool)
	Tokens(localTurnID string) (TokenSnapshot, bool)
	Terminal(localTurnID string) (Terminal, bool)
	SkillsSelected(localTurnID string) []string
	Counts(localTurnID string) (Counts, bool)
	Timestamps(localTurnID string) (Timestamps, bool)
}

// ObservationWriter is the mutation facet owned by observation subscribers and
// turn internals. Downstream consumers must not depend on this interface.
type ObservationWriter interface {
	MapTurn(localID, providerID string) (ok bool)
	AttributeCall(callID, localTurnID string) (ok bool)
	RecordTokens(localTurnID string, snap TokenSnapshot) TokenSnapshot
	RecordTerminal(localTurnID string, t Terminal) Terminal
	SetSkillsSelected(localTurnID string, slugs []string)
	Dedupe(key DedupeKey) bool
	IncrementToolCalls(localTurnID string) int32
	IncrementToolFailures(localTurnID string) int32
	IncrementApprovalRequests(localTurnID string) int32
	RecordStartedAt(localTurnID string, at time.Time)
	RecordCompletedAt(localTurnID string, at time.Time)
}

// Contract is the facade that observation owners write to and consumers read
// from. All methods are safe for concurrent use.
type Contract interface {
	ObservationReader
	ObservationWriter
}
