// Package classifier picks a prompt_template for a new conversation's first
// user input.
//
// Why it exists:
//   - SystemPromptPage lets the user pin exactly one "launch prompt"; the
//     router's prior keyword-routing layer was removed because it conflicted
//     with upstream CLIs' own intent handling (see router_resolve.go).
//   - For users who want "smart auto-pick across the whole library" without
//     the old keyword-match footgun, a small LLM call is the cleanest handoff:
//     deterministic-enough at top-1 granularity, cheaper than duplicating
//     intent classification in Go, and short-circuits cleanly when the user
//     has already pinned a prompt (the pin wins, classifier is skipped).
package classifier

import (
	"context"
	"time"
)

// Candidate is one prompt_template exposed to the classifier for ranking.
// Only the human-readable metadata is sent; PromptText is deliberately
// omitted to keep the classifier prompt under control and cost low.
type Candidate struct {
	PromptKey   string
	Title       string
	Description string
	Tags        []string
}

// Input is a classification request.
type Input struct {
	UserInput  string
	Candidates []Candidate
}

// Result is the classifier's top-1 pick.
//
// An empty PromptKey is the explicit "no strong match" signal; callers should
// fall through to default routing in that case rather than pick arbitrarily.
type Result struct {
	PromptKey string
	Reason    string
	Latency   time.Duration
	// Model records which model answered, for observability. Empty when the
	// concrete implementation does not surface it.
	Model string
}

// Classifier is the narrow contract the thread router depends on. The thread
// module takes this as optional fx dependency; when nil (or a noop), the
// router behaves exactly like the previous "single-pin only" path.
type Classifier interface {
	Classify(ctx context.Context, in Input) (Result, error)
	// Enabled reports whether the classifier will make a real attempt on
	// Classify. Callers can use this to short-circuit candidate collection
	// and logging when the feature is off.
	Enabled() bool
}
