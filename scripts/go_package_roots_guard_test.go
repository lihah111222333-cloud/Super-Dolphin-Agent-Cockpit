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
