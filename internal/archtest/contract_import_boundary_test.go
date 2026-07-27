package archtest

import (
	"strings"
	"testing"
)

func TestContractImportsStayOnDTOWhitelist(t *testing.T) {
	t.Parallel()

	evaluation, err := EvaluateBackendBoundary(
		repoRootForGuardTests(t),
		DefaultBackendBoundaryRegistry(),
		"contract_reverse_pollution",
	)
	if err != nil {
		t.Fatalf("evaluate contract import boundary: %v", err)
	}
	if len(evaluation.Violations) > 0 {
		t.Fatalf(
			"contract import boundary violations (%d):\n%s",
			len(evaluation.Violations),
			strings.Join(evaluation.Violations, "\n"),
		)
	}
}
