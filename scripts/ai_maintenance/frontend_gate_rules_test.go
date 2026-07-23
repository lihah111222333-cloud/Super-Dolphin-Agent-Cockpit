package main

import "testing"

func TestBuildGatePlanRoutesFrontendChangedTestsWithoutFullNpmTest(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"frontend-app/src/mainReadiness.js"})

	assertStringSetContains(t, plan.RequiredGates, "frontend:changed-tests")
	assertStringSetOmits(t, plan.RequiredGates, "frontend:test")
}

func TestBuildGatePlanOmitsDesktopSmokeFromPreCommitChangedTests(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{
		"frontend-app/scripts/delivery-smoke-runner.mjs",
		"frontend-app/scripts/delivery-smoke-runner.test.mjs",
	})

	assertStringSetOmits(t, plan.RequiredGates, "frontend:changed-tests", "frontend:test")
	assertStringSetContains(t, plan.RequiredGates, "frontend:lint", "frontend:embed-verify")
}

func TestBuildGatePlanRoutesFrontendPerformanceContractsToVerification(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{name: "baseline", file: "frontend-app/scripts/frontend-maintainability-baseline.json"},
		{name: "package entry", file: "frontend-app/package.json"},
		{name: "vite execution config", file: "frontend-app/vite.config.js"},
		{name: "runner", file: "frontend-app/scripts/performance-budget-runner.mjs"},
		{name: "runner contract", file: "frontend-app/scripts/performance-budget-runner.test.mjs"},
		{name: "model", file: "frontend-app/scripts/performance-budget-model.mjs"},
		{name: "model contract", file: "frontend-app/scripts/performance-budget-model.test.mjs"},
		{name: "cases", file: "frontend-app/scripts/frontend-performance-cases.json"},
		{name: "provenance", file: "frontend-app/scripts/evidence-provenance.mjs"},
		{name: "provenance contract", file: "frontend-app/scripts/evidence-provenance.test.mjs"},
		{name: "managed command runner dependency", file: "frontend-app/scripts/managed-command.mjs"},
		{name: "baseline provenance", file: "frontend-app/scripts/performance-baseline-provenance.mjs"},
		{name: "baseline provenance contract", file: "frontend-app/scripts/performance-baseline-provenance.test.mjs"},
		{name: "history benchmark", file: "frontend-app/scripts/chat-history-benchmark.mjs"},
		{name: "history benchmark contract", file: "frontend-app/scripts/chat-history-benchmark.test.mjs"},
		{name: "feedback benchmark", file: "frontend-app/scripts/stop-feedback-benchmark.mjs"},
		{name: "feedback benchmark contract", file: "frontend-app/scripts/stop-feedback-benchmark.test.mjs"},
		{name: "resource benchmark", file: "frontend-app/scripts/resource-budget.mjs"},
		{name: "resource benchmark contract", file: "frontend-app/scripts/resource-budget.test.mjs"},
		{name: "render benchmark", file: "frontend-app/scripts/render-isolation-probe.test.jsx"},
		{name: "contract store subject", file: "frontend-app/src/entities/client/model/contractStoreModel.js"},
		{name: "thread lifecycle subject", file: "frontend-app/src/entities/client/model/threadLifecycleRuntime.js"},
		{name: "feedback subject", file: "frontend-app/src/pages/chat/components/ChatActionFeedback.js"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.file})
			assertStringSetContains(t, plan.RequiredGates, "frontend:performance-verify")
		})
	}
}
