package archtest

import (
	"strings"
	"testing"
)

func TestCrossDomainGuard(t *testing.T) {
	t.Parallel()

	ruleIDs := CrossDomainBoundaryRuleIDs()
	evaluation, err := EvaluateBackendBoundary(repoRootForGuardTests(t), DefaultBackendBoundaryRegistry(), ruleIDs...)
	if err != nil {
		t.Fatalf("evaluate cross-domain boundary: %v", err)
	}
	assertBackendBoundaryGuardCoverage(t, "cross-domain", evaluation, ruleIDs)
	if len(evaluation.Violations) > 0 {
		t.Fatalf("Cross-Domain violations (%d):\n  %s", len(evaluation.Violations), strings.Join(evaluation.Violations, "\n  "))
	}
}

func crossDomainFileViolations(path, rel string) ([]string, error) {
	return EvaluateBackendBoundaryFile(path, rel, DefaultBackendBoundaryRegistry(), CrossDomainBoundaryRuleIDs()...)
}
