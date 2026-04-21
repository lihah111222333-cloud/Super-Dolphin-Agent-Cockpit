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
//
// Fallback behaviour: if no candidate's tag matches the user input, the
// router returns the FIRST candidate that has no tags at all (len(Tags)==0
// after trimming empty strings). This lets operators declare a "default"
// prompt template (tags: []) that ships an always-on baseline persona when
// no specialist agent fires — preferable to silently leaving
// BaseInstructions empty and letting the provider CLI pick its own default.
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
	var fallback *Candidate
	for i := range candidates {
		c := &candidates[i]
		effectiveTagCount := 0
		for _, tag := range c.Tags {
			t := strings.ToLower(strings.TrimSpace(tag))
			if t == "" {
				continue
			}
			effectiveTagCount++
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
		if effectiveTagCount == 0 && fallback == nil {
			fallback = c
		}
	}
	if fallback != nil {
		return Decision{
			Matched:    true,
			PromptKey:  fallback.PromptKey,
			AgentKey:   fallback.AgentKey,
			Reason:     "rule: fallback (no tags)",
			Confidence: 0.2,
		}, nil
	}
	return Decision{}, nil
}
