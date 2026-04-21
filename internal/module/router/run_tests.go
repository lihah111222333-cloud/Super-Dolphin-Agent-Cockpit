package router

import (
	"context"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// RunTests iterates every enabled prompt_routing_tests row, runs each input
// through the live RuleRouter + prompt_templates, and reports mismatches. A
// "pass" means Classify returns the expected_prompt_key (specific match OR
// fallback-to-main/default when that's the expectation). Missing routingTestRead
// or store degrades gracefully: returns a zero-value result rather than erroring.
func (s *service) RunTests(ctx context.Context) (RunTestsResult, error) {
	ctx = shared.NonNilContext(ctx)
	if s == nil || s.routingTestRead == nil {
		return RunTestsResult{}, nil
	}
	tests, err := s.routingTestRead.ListEnabled(ctx)
	if err != nil {
		s.logger.Warn("router/runTests: list tests failed", slog.String("error", err.Error()))
		return RunTestsResult{}, err
	}
	result := RunTestsResult{Total: len(tests)}
	if s.backend == nil || s.store == nil {
		result.Skipped = len(tests)
		return result, nil
	}
	templates, err := s.store.List(ctx, promptstore.ListFilter{Limit: 200})
	if err != nil {
		s.logger.Warn("router/runTests: list prompt_templates failed", slog.String("error", err.Error()))
		return RunTestsResult{}, err
	}
	candidates := toCandidates(templates)
	for _, t := range tests {
		decision, derr := s.backend.Classify(ctx, t.Input, candidates)
		if derr != nil {
			result.Failed++
			result.Failures = append(result.Failures, TestFailure{
				ID:       t.ID,
				Input:    t.Input,
				Expected: t.ExpectedPromptKey,
				Matched:  false,
				Reason:   "backend error: " + derr.Error(),
				Note:     t.Note,
			})
			continue
		}
		if decision.PromptKey == t.ExpectedPromptKey {
			result.Passed++
			continue
		}
		result.Failed++
		result.Failures = append(result.Failures, TestFailure{
			ID:           t.ID,
			Input:        t.Input,
			Expected:     t.ExpectedPromptKey,
			Actual:       decision.PromptKey,
			Matched:      decision.Matched,
			RouterReason: decision.Reason,
			Note:         t.Note,
		})
	}
	return result, nil
}
