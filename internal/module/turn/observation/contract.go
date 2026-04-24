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

// Contract is the facade that observation owners write to and consumers read
// from. All methods are safe for concurrent use.
type Contract interface {
	// MapTurn records the bidirectional local <-> provider turn id mapping.
	// Returns true if the mapping was accepted. An existing mapping cannot be
	// silently rewritten: attempting to re-bind either side to a different
	// value returns false.
	MapTurn(localID, providerID string) (ok bool)

	// ResolveLocalTurn returns the local id bound to a provider id.
	ResolveLocalTurn(providerID string) (localID string, ok bool)

	// ResolveProviderTurn returns the provider id bound to a local id.
	ResolveProviderTurn(localID string) (providerID string, ok bool)

	// AttributeCall binds a tool call id to its owning local turn id.
	// ToolDiffUpdated in particular has no turn_id field, so consumers must
	// consult this mapping instead of guessing.
	AttributeCall(callID, localTurnID string) (ok bool)

	// LookupCall returns the local turn id associated with a call id.
	LookupCall(callID string) (localTurnID string, ok bool)

	// RecordTokens merges snap with any prior snapshot for the turn.
	// Non-zero prior fields are preserved when snap's corresponding field is
	// zero. Returns the merged snapshot currently stored.
	RecordTokens(localTurnID string, snap TokenSnapshot) TokenSnapshot

	// Tokens reads the currently stored snapshot.
	Tokens(localTurnID string) (TokenSnapshot, bool)

	// RecordTerminal applies a terminal event under precedence rules:
	// interrupted/aborted is sticky and cannot be displaced by a later
	// completed/failed. Returns the effective terminal after applying.
	RecordTerminal(localTurnID string, t Terminal) Terminal

	// Terminal reads the effective terminal of a turn.
	Terminal(localTurnID string) (Terminal, bool)

	// SetSkillsSelected records the skill slugs chosen by the PrepareTurn
	// resolver. "Selected" means prepared for injection; it does not imply
	// the model actually invoked the skill.
	SetSkillsSelected(localTurnID string, slugs []string)

	// SkillsSelected returns a defensive copy of the recorded selection.
	SkillsSelected(localTurnID string) []string

	// Dedupe returns true the first time a key is seen (and records it),
	// false on subsequent calls. An empty key is treated as always unique:
	// observation refuses to swallow events that arrive without any
	// identifying field.
	Dedupe(key DedupeKey) bool

	// IncrementToolCalls bumps the per-turn tool_calls counter by one and
	// returns the post-increment value. Callers that need to avoid
	// double-counting a retried ToolCallBegin should gate the call with
	// Dedupe(DedupeKey{CallID: ...}) first.
	IncrementToolCalls(localTurnID string) int32

	// IncrementToolFailures bumps the per-turn tool_failures counter by one.
	IncrementToolFailures(localTurnID string) int32

	// IncrementApprovalRequests bumps the per-turn approval_requests counter
	// by one and flips ApprovalRequestsObserved to true. The Claude path
	// never emits these events, so absence of calls keeps observed=false —
	// which lets downstream queries filter unobserved turns out of average /
	// percentile metrics.
	IncrementApprovalRequests(localTurnID string) int32

	// Counts returns the observed counts for a turn. ok=false when no
	// counter has been recorded.
	Counts(localTurnID string) (Counts, bool)

	// RecordStartedAt stores the first non-zero start time for a turn.
	// Subsequent calls are no-ops so a late TurnInputReceived cannot drag
	// the recorded start forward.
	RecordStartedAt(localTurnID string, at time.Time)

	// RecordCompletedAt stores the most-recent non-zero completion time.
	// Latest-write-wins because terminal events can fire multiple times in
	// the race between TurnInterrupted and TurnCompleted.
	RecordCompletedAt(localTurnID string, at time.Time)

	// Timestamps returns the stored StartedAt / CompletedAt pair.
	Timestamps(localTurnID string) (Timestamps, bool)
}
