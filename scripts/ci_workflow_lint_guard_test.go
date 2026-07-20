package main

import "testing"

func TestCIWorkflowPinsActionlintAndPreservesPackageArguments(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	workflow := readRepoFile(t, "../.github/workflows/ci.yml")

	assertScriptContains(t, makefile, "ACTIONLINT_VERSION := v1.7.12")
	assertScriptContains(t, makefile, "actionlint:\n\tgo run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)")
	assertScriptContains(t, workflow, "name: Lint GitHub Actions workflows")
	assertScriptContains(t, workflow, "run: make actionlint")
	assertScriptContains(t, workflow, "mapfile -t go_packages < <(go list ./cmd/... ./internal/... ./pkg/... ./scripts/...)")
	assertScriptContains(t, workflow, `./scripts/test_with_guard.sh "${go_packages[@]}" -count=1 -timeout 180s`)
	assertScriptDoesNotContain(t, workflow, "./scripts/test_with_guard.sh $(go list ./...)")
}
