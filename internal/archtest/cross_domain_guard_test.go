package archtest

import (
	"strings"
	"testing"
)

func TestCrossDomainGuard(t *testing.T) {
	t.Parallel()

	evaluation, err := EvaluateBackendBoundary(repoRootForGuardTests(t), DefaultBackendBoundaryRegistry())
	if err != nil {
		t.Fatalf("evaluate combined backend boundary guards: %v", err)
	}
	t.Run("production-tree", func(t *testing.T) {
		t.Parallel()
		runBackendBoundaryProductionTreeCoverage(t, evaluation)
	})
	t.Run("cross-domain", func(t *testing.T) {
		t.Parallel()
		assertBackendBoundaryGuardCoverage(t, "cross-domain", evaluation, CrossDomainBoundaryRuleIDs())
		failBackendBoundaryGuardViolations(t, "Cross-Domain", evaluation.Violations, CrossDomainBoundaryRuleIDs())
	})
	t.Run("onion", func(t *testing.T) {
		t.Parallel()
		assertBackendBoundaryGuardCoverage(t, "onion", evaluation, OnionBoundaryRuleIDs())
		failBackendBoundaryGuardViolations(t, "Onion Architecture", evaluation.Violations, OnionBoundaryRuleIDs())
	})
}

func failBackendBoundaryGuardViolations(t *testing.T, label string, violations []string, ruleIDs []BoundaryRuleID) {
	t.Helper()
	wanted := make(map[BoundaryRuleID]bool, len(ruleIDs))
	for _, id := range ruleIDs {
		wanted[id] = true
	}
	var selected []string
	for _, violation := range violations {
		for id := range wanted {
			if strings.Contains(violation, "(rule="+string(id)+" ") {
				selected = append(selected, violation)
				break
			}
		}
	}
	if len(selected) > 0 {
		t.Fatalf("%s violations (%d):\n  %s", label, len(selected), strings.Join(selected, "\n  "))
	}
}

func crossDomainFileViolations(path, rel string) ([]string, error) {
	return EvaluateBackendBoundaryFile(path, rel, DefaultBackendBoundaryRegistry(), CrossDomainBoundaryRuleIDs()...)
}
