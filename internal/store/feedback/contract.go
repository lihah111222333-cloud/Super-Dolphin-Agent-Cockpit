// Package feedback persists append-only user/system feedback events tagged
// with the agent_key and prompt_version_id that were active at the moment of
// the event. Aggregations of these rows are the raw signal for "独立优化每个
// agent" (per-agent metrics, prompt A/B comparison).
package feedback

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store is the write + read surface the feedback module uses.
type Store = contract.FeedbackEventStore

// Event is the domain DTO for an agent_feedback_events row.
//
// EventType is a free-form string but common values (documented for UI/
// analysis consumers, not enforced by the store) are:
//
//	thumbs_up, thumbs_down  — explicit user satisfaction signal
//	retry                   — user re-ran same turn; signal of partial failure
//	edit                    — user edited prompt then re-sent
//	handoff_out             — this thread was superseded by a handoff
//	user_override_route     — user manually pinned agent_key, overriding router
type Event = contract.FeedbackEvent
