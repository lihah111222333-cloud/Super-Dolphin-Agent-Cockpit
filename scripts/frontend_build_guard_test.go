package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const frontendNodeEngineContract = "^20.19.0 || ^22.13.0 || >=24"

func TestFrontendNodeRuntimeAndDocumentationContract(t *testing.T) {
	packageJSON := readRepoFile(t, "../frontend-app/package.json")
	var manifest struct {
		Engines struct {
			Node string `json:"node"`
		} `json:"engines"`
	}
	if err := json.Unmarshal([]byte(packageJSON), &manifest); err != nil {
		t.Fatalf("decode frontend-app/package.json: %v", err)
	}
	if manifest.Engines.Node != frontendNodeEngineContract {
		t.Fatalf("frontend-app/package.json engines.node = %q, want %q", manifest.Engines.Node, frontendNodeEngineContract)
	}

	documents := []string{
		"../README.md",
		"../README.zh-CN.md",
		"../README.ja.md",
		"../README.ko.md",
		"../README.es.md",
		"../README.de.md",
		"../CONTRIBUTING.md",
	}
	for _, path := range documents {
		text := readRepoFile(t, path)
		assertScriptContains(t, text, "`"+frontendNodeEngineContract+"`")
		assertScriptDoesNotContain(t, text, "Node.js 20.19+")
		assertScriptDoesNotContain(t, text, "Node.js 20.19 or newer")
	}
}

func TestMakefileBuildsCurrentFrontendAppByDefault(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	packageJSON := readRepoFile(t, "../frontend-app/package.json")

	assertScriptContains(t, makefile, "FRONTEND_APP_DIR := frontend-app")
	assertScriptContains(t, makefile, "EMBEDDED_FRONTEND_DIR := cmd/agent-terminal/web-dist")
	assertScriptContains(t, makefile, "FRONTEND_REQUIRED_ENTRIES_FILE := $(FRONTEND_APP_DIR)/required-dist-entries.txt")
	assertScriptContains(t, makefile, "frontend-app-deps:")
	assertScriptContains(t, makefile, "frontend-app-build: frontend-app-deps")
	assertScriptContains(t, makefile, "frontend-build: frontend-app-build")
	assertScriptContains(t, makefile, "cd $(FRONTEND_APP_DIR) && $(NPM) run build")
	assertScriptDoesNotContain(t, makefile, "node $(FRONTEND_APP_DIR)/scripts/sync-frontend-dist.mjs")
	assertScriptContains(t, makefile, "while IFS= read -r entry")
	assertScriptContains(t, makefile, "$(FRONTEND_REQUIRED_ENTRIES_FILE)")
	assertScriptContains(t, makefile, "frontend dist missing required entry")
	assertScriptContains(t, makefile, "embedded frontend dist missing required entry")
	assertScriptDoesNotContain(t, makefile, "frontend-legacy-build")
	assertScriptDoesNotContain(t, makefile, "LEGACY_FRONTEND_DIR")
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build build-peer-binaries")
	assertScriptContains(t, packageJSON, `"build": "vite build --configLoader runner && node scripts/sync-frontend-dist.mjs"`)
}

func TestDesktopBuildAndRunTargetsBuildCurrentPeerArtifacts(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "build-agent-terminal: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "build-agent-terminal-plain: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "run: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "run-plain: frontend-build build-peer-binaries")
	assertScriptContains(t, makefile, "build-peer-binaries:\n\t@mkdir -p bin")
	assertScriptContains(t, makefile, "SCHEMA_BUILD_IDENTITY_LDFLAG = -X github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema.buildAppCommit=$(APP_COMMIT)")
	assertScriptContains(t, makefile, "go build -ldflags \"$(SCHEMA_BUILD_IDENTITY_LDFLAG)\" -o \"$$tmp\" ./cmd/mcp-schema-compiler-helper")
	assertScriptContains(t, makefile, "go build -ldflags \"$(SCHEMA_BUILD_IDENTITY_LDFLAG)\" -o bin/agent-terminal ./cmd/agent-terminal")
	assertScriptContains(t, makefile, "-app-commit \"$(APP_COMMIT)\"")
	assertScriptContains(t, makefile, "--write-package-manifest")
	assertScriptDoesNotContain(t, makefile, "build-peer-binaries: build-agent-terminal")
	assertScriptDoesNotContain(t, makefile, "build-peer-binaries: run")
}

func TestMakefileGuardRunsFullArchtestAndFrontendEmbedVerify(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "frontend-embed-verify: frontend-app-build")
	assertScriptContains(t, makefile, "./scripts/frontend_embed_verify.sh")
	assertScriptContains(t, makefile, "frontend-gate-health:")
	assertScriptContains(t, makefile, "./scripts/test_with_guard.sh ./scripts/ai_maintenance ./scripts -run 'Frontend|GateInfrastructure|GateRunners' -count=1")
	assertScriptDoesNotContain(t, makefile, "./scripts/test_with_guard.sh ./scripts -run 'Frontend'")
	assertScriptContains(t, makefile, "guard:\n\t$(TEST_WITH_GUARD) --guard-only")
	assertScriptContains(t, makefile, "code-size-guard:\n\t$(TEST_WITH_GUARD) --guard-only")
}

func TestProtocolSyncReusesRPCFullArchtestWithoutRepeatingFreezeTests(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	protocolTarget := makefileTargetBlock(t, makefile, "protocol-sync-check")
	assertScriptContains(t, protocolTarget, "protocol-sync-check: rpc-regression-check")
	assertScriptDoesNotContain(t, protocolTarget, "$(TEST_WITH_GUARD)")

	rpcTarget := makefileTargetBlock(t, makefile, "rpc-regression-check")
	assertScriptContains(t, rpcTarget, "$(TEST_WITH_GUARD) ./internal/platform/rpc/... -count=1")
	guardScript := readRepoFile(t, "test_with_guard.sh")
	assertScriptContains(t, guardScript, `"$real_go" test ./internal/archtest -count=1`)

	for file, testName := range map[string]string{
		"../internal/archtest/orchestration_launcher_protocol_guard_test.go": "TestOrchestrationLauncherProtocolFreeze",
		"../internal/archtest/orchestration_report_protocol_guard_test.go":   "TestOrchestrationReportProtocolFreeze",
		"../internal/archtest/toolbridge_protocol_guard_test.go":             "TestToolbridgeProtocolFreezeContractGuard",
	} {
		assertScriptContains(t, readRepoFile(t, file), "func "+testName+"(")
	}
}

func makefileTargetBlock(t *testing.T, makefile, target string) string {
	t.Helper()
	marker := target + ":"
	start := strings.Index(makefile, marker)
	if start < 0 {
		t.Fatalf("Makefile target %q is missing", target)
	}
	block := makefile[start:]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	return block
}
