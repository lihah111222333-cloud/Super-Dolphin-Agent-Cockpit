package main

import "testing"

func TestMakefileBroadGoTargetsAvoidGeneratedPackageArtifacts(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "GO_PACKAGE_PATTERNS := ./cmd/... ./internal/... ./pkg/... ./scripts/...")
	assertScriptContains(t, makefile, "go list $(GO_PACKAGE_PATTERNS)")
	assertScriptDoesNotContain(t, makefile, "go list ./...")
	assertScriptDoesNotContain(t, makefile, "go build ./...")
	assertScriptDoesNotContain(t, makefile, "go vet ./...")
}

func TestMakefileBuildPeerBinariesUsesAtomicReplace(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, makefile, "tmp=\"$$(mktemp \"bin/.mcp-orch.XXXXXX\")\"")
	assertScriptContains(t, makefile, "go build -o \"$$tmp\" ./cmd/mcp-orch")
	assertScriptContains(t, makefile, "mv -f \"$$tmp\" bin/mcp-orch")
	assertScriptContains(t, makefile, "tmp=\"$$(mktemp \"bin/.mcp-lsp.XXXXXX\")\"")
	assertScriptContains(t, makefile, "go build -o \"$$tmp\" ./cmd/mcp-lsp")
	assertScriptContains(t, makefile, "mv -f \"$$tmp\" bin/mcp-lsp")
	assertScriptDoesNotContain(t, makefile, "\n\tgo build -o bin/mcp-orch ./cmd/mcp-orch")
	assertScriptDoesNotContain(t, makefile, "\n\tgo build -o bin/mcp-lsp ./cmd/mcp-lsp")
}
