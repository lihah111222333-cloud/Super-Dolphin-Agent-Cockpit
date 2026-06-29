package main

import "testing"

func TestMakefileBuildsCurrentFrontendAppByDefault(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	packageJSON := readRepoFile(t, "../frontend-app/package.json")

	assertScriptContains(t, makefile, "FRONTEND_APP_DIR := frontend-app")
	assertScriptContains(t, makefile, "EMBEDDED_FRONTEND_DIR := cmd/agent-terminal/web-dist")
	assertScriptContains(t, makefile, "frontend-app-deps:")
	assertScriptContains(t, makefile, "frontend-app-build: frontend-app-deps")
	assertScriptContains(t, makefile, "frontend-build: frontend-app-build")
	assertScriptContains(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build")
	assertScriptContains(t, makefile, "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
	assertScriptContains(t, makefile, "test -f $(EMBEDDED_FRONTEND_DIR)/index.html")
	assertScriptDoesNotContain(t, makefile, "frontend-legacy-build")
	assertScriptDoesNotContain(t, makefile, "LEGACY_FRONTEND_DIR")
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build")
	assertScriptOrder(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build", "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
	assertScriptContains(t, packageJSON, `"build": "vite build && node scripts/sync-frontend-dist.mjs"`)
}

func TestMakefileGuardRunsFullArchtestAndFrontendEmbedVerify(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	workflow := readRepoFile(t, "../.github/workflows/ci.yml")

	assertScriptContains(t, makefile, "frontend-embed-verify: frontend-app-build")
	assertScriptContains(t, makefile, "./scripts/frontend_embed_verify.sh")
	assertScriptContains(t, makefile, "guard:\n\t$(TEST_WITH_GUARD) ./internal/archtest -count=1")
	assertScriptContains(t, makefile, "code-size-guard:\n\t$(TEST_WITH_GUARD) --guard-only")

	assertScriptContains(t, workflow, "Frontend embed verify")
	assertScriptContains(t, workflow, "make frontend-embed-verify")
	assertScriptContains(t, workflow, "Validate skills")
}
