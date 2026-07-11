package archtest_test

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func TestSQLCUsesCanonicalBoundaryRule(t *testing.T) {
	evaluation, err := archtest.EvaluateBackendBoundary(
		repoRoot(t),
		archtest.DefaultBackendBoundaryRegistry(),
		"store_sqlc_store_platform_only",
	)
	if err != nil {
		t.Fatalf("evaluate canonical SQLC boundary: %v", err)
	}
	if len(evaluation.Violations) > 0 {
		t.Fatalf("canonical SQLC boundary violations:\n%s", strings.Join(evaluation.Violations, "\n"))
	}
}
