package main

import "testing"

// TestFrontendGatePlanUsesRiskSpecificRouting 锁定前端路径类别与日常门禁的最小风险集合。
func TestFrontendGatePlanUsesRiskSpecificRouting(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
		omit []string
	}{
		{name: "production source", path: "frontend-app/src/App.jsx", want: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}, omit: []string{"frontend:embed-verify"}},
		{name: "stylesheet", path: "frontend-app/src/styles.css", omit: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "frontend:typecheck-contracts", "frontend:embed-verify", "lsp:changed-diagnostics"}},
		{name: "unit test", path: "frontend-app/src/App.test.jsx", want: []string{"frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}, omit: []string{"frontend:static-guards", "frontend:embed-verify", "frontend:typecheck-contracts"}},
		{name: "gate script", path: "frontend-app/scripts/frontend-state-ownership-guard.mjs", want: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}, omit: []string{"frontend:embed-verify"}},
		{name: "package manifest", path: "frontend-app/package.json", want: []string{"frontend:static-guards"}, omit: []string{"frontend:lint", "frontend:changed-tests", "frontend:embed-verify", "frontend:performance-verify", "lsp:changed-diagnostics"}},
		{name: "frontend documentation", path: "frontend-app/README.md", omit: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "frontend:typecheck-contracts", "frontend:embed-verify", "frontend:performance-verify", "lsp:changed-diagnostics"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.path})
			assertStringSetContains(t, plan.RequiredGates, test.want...)
			assertStringSetOmits(t, plan.RequiredGates, test.omit...)
		})
	}
}

func TestPushGatePlanAddsFrontendBuildAndPerformanceChecks(t *testing.T) {
	files := []string{"frontend-app/package.json", "frontend-app/src/App.jsx"}
	commitPlan := mustGatePlanForScope(t, files, false)
	pushPlan := mustGatePlanForScope(t, files, true)
	assertStringSetOmits(t, commitPlan.RequiredGates, "frontend:embed-verify", "frontend:performance-verify")
	assertStringSetContains(t, pushPlan.RequiredGates, "frontend:embed-verify", "frontend:performance-verify")
}

func TestPushGatePlanRoutesVitestExcludedE2EToExplicitCommands(t *testing.T) {
	tests := []struct {
		file   string
		script string
	}{
		{"frontend-app/tests/e2e/business-flows.spec.js", "test:e2e:business"},
		{"frontend-app/playwright.business-flows.config.js", "test:e2e:business"},
		{"frontend-app/tests/e2e/desktop-wide.spec.js", "test:e2e:desktop-wide"},
		{"frontend-app/playwright.desktop-wide.config.js", "test:e2e:desktop-wide"},
		{"frontend-app/tests/e2e/desktop-failure.spec.js", "smoke:desktop:failure"},
		{"frontend-app/tests/e2e/desktop-ux.spec.js", "smoke:desktop:ux"},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			commitPlan := mustGatePlanForScope(t, []string{test.file}, false)
			pushPlan := mustGatePlanForScope(t, []string{test.file}, true)
			assertStringSetOmits(t, commitPlan.RequiredGates, "frontend:e2e", "frontend:changed-tests")
			assertStringSetContains(t, pushPlan.RequiredGates, "frontend:e2e")
			commands := frontendE2ECommands(pushPlan.ChangedFiles)
			if len(commands) != 1 || commands[0].script != test.script {
				t.Fatalf("frontend E2E commands = %#v, want %q", commands, test.script)
			}
		})
	}
}

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
	assertStringSetContains(t, plan.RequiredGates, "frontend:lint")
	assertStringSetOmits(t, plan.RequiredGates, "frontend:embed-verify")
}

func TestPushGatePlanRoutesFrontendPerformanceContractsToVerification(t *testing.T) {
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
			commitPlan := mustGatePlanForScope(t, []string{test.file}, false)
			pushPlan := mustGatePlanForScope(t, []string{test.file}, true)
			assertStringSetOmits(t, commitPlan.RequiredGates, "frontend:performance-verify")
			assertStringSetContains(t, pushPlan.RequiredGates, "frontend:performance-verify")
		})
	}
}

func TestFrontendChangedTestsDeferExcludedPerformanceClosureToPushVerifier(t *testing.T) {
	tests, err := frontendChangedTestFiles([]string{
		"frontend-app/scripts/performance-budget-runner.mjs",
		"frontend-app/scripts/performance-budget-runner.test.mjs",
	})
	if err != nil {
		t.Fatalf("frontendChangedTestFiles returned error: %v", err)
	}
	if len(tests) != 0 {
		t.Fatalf("performance runner tests are excluded by Vitest config and must not be scheduled, got %v", tests)
	}
	commitPlan := mustGatePlanForScope(t, []string{"frontend-app/scripts/performance-budget-runner.mjs"}, false)
	pushPlan := mustGatePlanForScope(t, []string{"frontend-app/scripts/performance-budget-runner.mjs"}, true)
	assertStringSetOmits(t, commitPlan.RequiredGates, "frontend:changed-tests", "frontend:performance-verify")
	assertStringSetContains(t, pushPlan.RequiredGates, "frontend:performance-verify")
}
