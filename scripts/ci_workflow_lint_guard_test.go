package main

import "testing"

func TestCIWorkflowKeepsPinnedActionlintWithoutHostCandidateExecution(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	workflow := readRepoFile(t, "../.github/workflows/ci.yml")
	executorMapping := readRepoFile(t, "../internal/devtools/gate/executor_mapping.go")
	runtimeDeps := readRepoFile(t, "../build/gate/runtime-deps.Dockerfile")

	assertScriptContains(t, makefile, "ACTIONLINT_VERSION := v1.7.12")
	assertScriptContains(t, makefile, "actionlint:\n\tgo run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)")
	assertScriptContains(t, workflow, "truth-image-gates:")
	assertScriptContains(t, workflow, "workflow-host")
	assertScriptDoesNotContain(t, workflow, "run: make actionlint")
	assertScriptDoesNotContain(t, workflow, "go list ./cmd/...")
	assertScriptContains(t, executorMapping, `[]string{"actionlint"}`)
	assertScriptContains(t, executorMapping, `"./scripts/test_with_guard.sh", "--canonical-backend"`)
	assertScriptContains(t, executorMapping, `return []string{"./cmd/...", "./internal/...", "./pkg/...", "./scripts/..."}`)
	assertScriptContains(t, runtimeDeps, `-o /out/actionlint github.com/rhysd/actionlint/cmd/actionlint`)
}
