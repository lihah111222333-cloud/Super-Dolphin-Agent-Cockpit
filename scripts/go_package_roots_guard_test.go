package main

import "testing"

func TestMakefileBroadGoTargetsCoverTheWholeRootModule(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	guardScript := readRepoFile(t, "test_with_guard.sh")

	assertScriptContains(t, makefile, "GO_PACKAGE_PATTERNS := ./...")
	assertScriptContains(t, makefile, "$(TEST_WITH_GUARD) --make-test-suite")
	assertScriptContains(t, guardScript, `"$real_go" list ./...`)
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
