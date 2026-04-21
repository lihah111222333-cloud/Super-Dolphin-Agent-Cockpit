// Package router selects which agent/prompt a new thread should run under,
// based on the user's initial input. The Backend interface is the swap
// boundary: RuleRouter is the zero-dependency default; later implementations
// (e.g. a Haiku-backed HaikuRouter) slot in without touching thread/start
// callers.
package router

import "context"

// Candidate describes one routing choice. The caller (thread.Service) builds
// this list by querying enabled prompt_templates rows and projecting the
// minimum surface the backend needs to decide.
//
// Granularity is one row per prompt_key — not per agent_key — because
// prompt_templates.agent_key is not unique; a single agent can have multiple
// prompts (different tool_name scopes, A/B variants, etc.). Routing at
// prompt_key granularity keeps that distinction visible downstream.
type Candidate struct {
	PromptKey   string
	AgentKey    string
	Title       string
	Description string
	// Tags are the `tags` jsonb column unmarshaled into a flat []string.
	// RuleRouter uses these as match keywords; HaikuRouter may pass them to
	// the classifier as hints.
	Tags []string
}

// Decision is the classifier's output. Matched=false means no candidate fit
// and the caller should fall back (e.g. leave BaseInstructions empty so the
// provider falls back to its own default).
type Decision struct {
	Matched    bool
	PromptKey  string
	AgentKey   string
	Reason     string  // human-readable explanation, stored for observability
	Confidence float64 // [0,1]; rule-based returns 1.0 on match, 0.0 otherwise
}

// Backend classifies a user input against a set of candidates.
//
// Contract notes:
//   - Implementations must not mutate `candidates`.
//   - Empty/whitespace `userInput` yields Matched=false; do not error.
//   - Error return is reserved for infrastructure failures (network, API);
//     "no match" is a Decision with Matched=false, not an error.
type Backend interface {
	Classify(ctx context.Context, userInput string, candidates []Candidate) (Decision, error)
}
