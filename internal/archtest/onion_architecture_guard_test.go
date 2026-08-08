package archtest

import (
	"strings"
	"testing"
)

func onionArchitectureViolationsForFile(path, rel string) ([]string, error) {
	return EvaluateBackendBoundaryFile(path, rel, DefaultBackendBoundaryRegistry(), OnionBoundaryRuleIDs()...)
}

func assertBackendBoundaryGuardCoverage(
	t *testing.T,
	guardName string,
	evaluation BoundaryEvaluation,
	ruleIDs []BoundaryRuleID,
) {
	t.Helper()
	if evaluation.CandidateFiles == 0 {
		t.Fatalf("%s guard zero production Go candidates", guardName)
	}
	if evaluation.MatchedFiles == 0 {
		t.Fatalf("%s guard zero matched production files", guardName)
	}
	var missing []string
	for _, ruleID := range ruleIDs {
		if evaluation.ByRule[ruleID] == 0 {
			missing = append(missing, string(ruleID))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s guard zero coverage for canonical rules: %s", guardName, strings.Join(missing, ", "))
	}
}

// isMatchedLayer 保留给 stateless 守卫的通用层路径匹配，不参与洋葱边界求值。
func isMatchedLayer(path, layer string) bool {
	return strings.Contains(path, layer+"/") || strings.HasSuffix(path, layer)
}
