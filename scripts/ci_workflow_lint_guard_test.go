package main

import "testing"

func TestCIWorkflowPinsActionlintAndUsesSerializedSharedTestOwner(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	workflow := readRepoFile(t, "../.github/workflows/ci.yml")

	assertScriptContains(t, makefile, "ACTIONLINT_VERSION := v1.7.12")
	assertScriptContains(t, makefile, "actionlint:\n\tgo run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)")
	assertScriptContains(t, workflow, "name: Lint GitHub Actions workflows")
	assertScriptContains(t, workflow, "run: make actionlint")
	assertScriptContains(t, workflow, "run: make ci-l1")
	assertScriptContains(t, makefile, `$(TEST_WITH_GUARD) $(DEFERRED_TEST_PKGS) -count=1 -p 1 -timeout 120s`)
	assertScriptDoesNotContain(t, workflow, `./scripts/test_with_guard.sh "${go_packages[@]}"`)
	assertScriptContains(t, workflow, "go install golang.org/x/tools/gopls@v0.21.1")
	assertScriptDoesNotContain(t, workflow, "gopls@latest")
	assertScriptContains(t, workflow, "windows-core-tests:")
	assertScriptContains(t, workflow, "mapfile -t windows_packages < <(go list ./cmd/... ./internal/... ./pkg/... ./scripts/...)")
	assertScriptContains(t, workflow, `./scripts/test_with_guard.sh --quick-guard "$package" -count=1 -p 1`)
}
