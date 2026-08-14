package main

import "testing"

// TestFrontendStaticGuardCommandUsesReadOnlyCodeSizeGate 锁定 hook/CI 的 canonical 静态门禁入口。
func TestFrontendStaticGuardCommandUsesReadOnlyCodeSizeGate(t *testing.T) {
	dir, name, args := frontendStaticGuardCommand()
	if dir != "frontend-app" || name != "npm" || len(args) != 2 || args[0] != "run" || args[1] != "guard:critical-skip" {
		t.Fatalf("frontend static guard command = dir:%q name:%q args:%v", dir, name, args)
	}
}

// TestFrontendGatePlanUsesRiskSpecificRouting 锁定前端路径类别与日常门禁的最小风险集合。
func TestFrontendGatePlanUsesRiskSpecificRouting(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
		omit []string
	}{
		{name: "production source", path: "frontend-app/src/App.jsx", want: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}, omit: []string{"frontend:embed-verify"}},
		{name: "stylesheet", path: "frontend-app/src/styles.css", want: []string{"frontend:static-guards"}, omit: []string{"frontend:lint", "frontend:changed-tests", "frontend:typecheck-contracts", "frontend:embed-verify", "lsp:changed-diagnostics"}},
		{name: "unit test", path: "frontend-app/src/App.test.jsx", want: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}, omit: []string{"frontend:embed-verify", "frontend:typecheck-contracts"}},
		{name: "gate script", path: "frontend-app/scripts/frontend-state-ownership-guard.mjs", want: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}, omit: []string{"frontend:embed-verify"}},
		{name: "remote performance runtime", path: "frontend-app/scripts/runtime/git-environment.mjs", want: []string{"frontend:static-guards", "frontend:lint", "lsp:changed-diagnostics"}, omit: []string{"frontend:changed-tests"}},
		{name: "performance subject with unit test", path: "frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js", want: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "lsp:changed-diagnostics"}},
		{name: "package manifest", path: "frontend-app/package.json", want: []string{"frontend:static-guards", "frontend:lint", "frontend:typecheck-contracts"}, omit: []string{"frontend:changed-tests", "frontend:embed-verify", "lsp:changed-diagnostics"}},
		{name: "package lock", path: "frontend-app/package-lock.json", want: []string{"frontend:static-guards", "frontend:lint", "frontend:typecheck-contracts"}, omit: []string{"frontend:changed-tests", "lsp:changed-diagnostics"}},
		{name: "action registry", path: "frontend-app/config/action-producer-registry.json", want: []string{"frontend:static-guards"}, omit: []string{"frontend:lint", "frontend:changed-tests"}},
		{name: "code size baseline", path: "frontend-app/.frontend_code_size_guard_baseline.json", want: []string{"frontend:static-guards"}, omit: []string{"frontend:lint", "frontend:changed-tests"}},
		{name: "root playwright config", path: "frontend-app/playwright.business-flows.config.js", want: []string{"frontend:lint", "lsp:changed-diagnostics"}, omit: []string{"frontend:changed-tests"}},
		{name: "public runtime", path: "frontend-app/public/wails/runtime.js", want: []string{"frontend:lint", "lsp:changed-diagnostics"}, omit: []string{"frontend:changed-tests"}},
		{name: "e2e spec", path: "frontend-app/tests/e2e/business-flows.spec.js", want: []string{"frontend:static-guards", "frontend:lint", "lsp:changed-diagnostics"}, omit: []string{"frontend:changed-tests"}},
		{name: "frontend documentation", path: "frontend-app/README.md", omit: []string{"frontend:static-guards", "frontend:lint", "frontend:changed-tests", "frontend:typecheck-contracts", "frontend:embed-verify", "lsp:changed-diagnostics"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.path})
			assertStringSetContains(t, plan.RequiredGates, test.want...)
			assertStringSetOmits(t, plan.RequiredGates, test.omit...)
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
