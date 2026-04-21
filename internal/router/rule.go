package router

import (
	"context"
	"strings"
)

// RuleRouter matches user input against each candidate's Tags via
// case-insensitive substring check. It is deterministic, has no external
// dependencies, and costs microseconds — suitable for MVP and as a fallback
// when LLM backends are unavailable.
//
// Candidate priority is the caller's responsibility: RuleRouter returns the
// first Candidate whose tag matches, so the caller should present candidates
// in preferred-first order (e.g. latest updated, or explicitly pinned).
type RuleRouter struct{}

// NewRuleRouter returns a zero-value RuleRouter. It is stateless and safe
// for concurrent use.
func NewRuleRouter() *RuleRouter { return &RuleRouter{} }

func (r *RuleRouter) Classify(_ context.Context, userInput string, candidates []Candidate) (Decision, error) {
	trimmed := strings.TrimSpace(userInput)
	if trimmed == "" {
		return Decision{}, nil
	}
	lowered := strings.ToLower(trimmed)
	for _, c := range candidates {
		for _, tag := range c.Tags {
			t := strings.ToLower(strings.TrimSpace(tag))
			if t == "" {
				continue
			}
			if strings.Contains(lowered, t) {
				return Decision{
					Matched:    true,
					PromptKey:  c.PromptKey,
					AgentKey:   c.AgentKey,
					Reason:     "rule: tag=" + t,
					Confidence: 1.0,
				}, nil
			}
		}
	}
	return Decision{}, nil
}
