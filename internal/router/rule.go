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
//
// Structural tags (e.g. "scope.cwd:.", "scope.*") are UI-maintained routing
// metadata, not content keywords. RuleRouter treats them as inert: they are
// skipped during keyword match AND do not count toward the "has tags" check
// that opts a candidate out of the fallback pool. Without this, a prompt
// auto-tagged by the UI with scope directives would never be selectable via
// classifier input and would also be excluded from fallback — effectively
// orphaned.
type RuleRouter struct{}

// isStructuralTag reports whether a tag is UI-maintained routing metadata
// rather than a content keyword. Current convention: any tag starting with
// "scope." (e.g. scope.cwd:., scope.project:foo) is structural.
func isStructuralTag(tag string) bool {
	return strings.HasPrefix(tag, "scope.")
}

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
			if t == "" || isStructuralTag(t) {
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
