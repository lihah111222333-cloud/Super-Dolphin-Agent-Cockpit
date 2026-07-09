package archtest_test

import (
	"go/ast"
	"strings"
	"testing"
)

func TestOrchestrationServiceConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	failIfViolations(t, collectWideOrchestrationProductionViolationMessages(t, root))
}

func isAllowedWideOrchestrationFacadeUse(relPath, kind, name string) bool {
	return false
}

func fieldName(field *ast.Field) string {
	if len(field.Names) == 0 {
		return "(anonymous)"
	}
	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ", ")
}

func valueSpecNames(spec *ast.ValueSpec) string {
	names := make([]string, 0, len(spec.Names))
	for _, name := range spec.Names {
		names = append(names, name.Name)
	}
	return strings.Join(names, ", ")
}

func containsViolation(violations []string, want string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, want) {
			return true
		}
	}
	return false
}
