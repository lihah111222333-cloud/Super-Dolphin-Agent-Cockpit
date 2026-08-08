package archtest_test

import (
	"go/ast"
	"strings"
	"testing"
)

type orchestrationServiceBoundaryCheck struct {
	name string
	run  func(*testing.T, []*orchestrationServiceCheckedPackage)
}

func TestOrchestrationServiceConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	pkgs := loadWideOrchestrationTypeGuardPackages(t, root)
	for _, check := range orchestrationServiceBoundaryChecks() {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, pkgs)
		})
	}
}

func orchestrationServiceBoundaryChecks() []orchestrationServiceBoundaryCheck {
	return []orchestrationServiceBoundaryCheck{
		{name: "wide-consumers", run: runOrchestrationServiceWideConsumersCheck},
		{name: "type-consumers", run: runOrchestrationServiceTypeConsumersCheck},
		{name: "ssa-consumers", run: runOrchestrationServiceSSAConsumersCheck},
	}
}

func runOrchestrationServiceWideConsumersCheck(t *testing.T, pkgs []*orchestrationServiceCheckedPackage) {
	t.Helper()
	failIfViolations(t, collectWideOrchestrationProductionViolationMessagesFromPackages(pkgs))
}

func TestOrchestrationServiceBoundarySubtestsCoverAllRules(t *testing.T) {
	want := []string{"wide-consumers", "type-consumers", "ssa-consumers"}
	checks := orchestrationServiceBoundaryChecks()
	if len(checks) != len(want) {
		t.Fatalf("boundary subtest count = %d, want %d", len(checks), len(want))
	}
	for index, check := range checks {
		if check.name != want[index] {
			t.Errorf("boundary subtest %d = %q, want %q", index, check.name, want[index])
		}
		if check.run == nil {
			t.Errorf("boundary subtest %q has no check implementation", check.name)
		}
	}
}

func isAllowedWideOrchestrationFacadeUse(_, _, _ string) bool {
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
