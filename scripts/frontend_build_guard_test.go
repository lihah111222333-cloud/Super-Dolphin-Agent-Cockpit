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
