package main

import "testing"

func TestMakefileBuildsCurrentFrontendAppByDefault(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "FRONTEND_APP_DIR := frontend-app")
	assertScriptContains(t, makefile, "LEGACY_FRONTEND_DIR := cmd/agent-terminal/frontend")
	assertScriptContains(t, makefile, "frontend-app-deps:")
	assertScriptContains(t, makefile, "frontend-app-build: frontend-app-deps")
	assertScriptContains(t, makefile, "frontend-legacy-build: frontend-legacy-deps")
	assertScriptContains(t, makefile, "frontend-build: frontend-app-build")
	assertScriptContains(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build")
	assertScriptContains(t, makefile, "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
	assertScriptContains(t, makefile, "cd $(LEGACY_FRONTEND_DIR) && $(NPM) run build")
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build")
	assertScriptOrder(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build", "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
}
