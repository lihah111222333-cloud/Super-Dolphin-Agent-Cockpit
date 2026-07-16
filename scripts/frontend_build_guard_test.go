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
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build build-peer-binaries")
	assertScriptOrder(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build", "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
	assertScriptContains(t, packageJSON, `"build": "vite build && node scripts/sync-frontend-dist.mjs"`)
}

func TestDesktopBuildAndRunTargetsBuildCurrentPeerArtifacts(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "build-agent-terminal: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "run: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "run-plain: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "build-peer-binaries:\n\t@mkdir -p bin")
	assertScriptContains(t, makefile, "SCHEMA_BUILD_IDENTITY_LDFLAG := -X github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema.buildAppCommit=$(APP_COMMIT)")
	assertScriptContains(t, makefile, "go build -ldflags \"$(SCHEMA_BUILD_IDENTITY_LDFLAG)\" -o \"$$tmp\" ./cmd/mcp-schema-compiler-helper")
	assertScriptContains(t, makefile, "go build -ldflags \"$(SCHEMA_BUILD_IDENTITY_LDFLAG)\" -o bin/agent-terminal ./cmd/agent-terminal")
	assertScriptContains(t, makefile, "-app-commit \"$(APP_COMMIT)\"")
	assertScriptContains(t, makefile, "--write-package-manifest")
	assertScriptDoesNotContain(t, makefile, "build-peer-binaries: build-agent-terminal")
	assertScriptDoesNotContain(t, makefile, "build-peer-binaries: run")
}

func TestMakefileGuardRunsFullArchtestAndFrontendEmbedVerify(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	workflow := readRepoFile(t, "../.github/workflows/ci.yml")

	assertScriptContains(t, makefile, "frontend-embed-verify: frontend-app-build")
	assertScriptContains(t, makefile, "./scripts/frontend_embed_verify.sh")
	assertScriptContains(t, makefile, "guard:\n\t$(TEST_WITH_GUARD) --guard-only")
	assertScriptContains(t, makefile, "code-size-guard:\n\t$(TEST_WITH_GUARD) --guard-only")

	assertScriptContains(t, workflow, "Frontend embed verify")
	assertScriptContains(t, workflow, "make frontend-embed-verify")
	assertScriptContains(t, workflow, "Validate skills")
}
