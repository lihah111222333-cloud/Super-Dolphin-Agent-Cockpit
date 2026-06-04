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
	assertScriptContains(t, makefile, "cd $(FRONTEND_APP_DIR) && npm run build")
	assertScriptContains(t, makefile, "rsync -a --delete $(FRONTEND_APP_DIR)/dist/ $(LEGACY_FRONTEND_DIR)/dist/")
	assertScriptContains(t, makefile, "cd $(LEGACY_FRONTEND_DIR) && npm run build")
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build")
	assertScriptOrder(t, makefile, "cd $(FRONTEND_APP_DIR) && npm run build", "rsync -a --delete $(FRONTEND_APP_DIR)/dist/ $(LEGACY_FRONTEND_DIR)/dist/")
}
