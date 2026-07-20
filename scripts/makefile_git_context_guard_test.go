package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedArtifactMakeTargetsDoNotRequireGitMetadata(t *testing.T) {
	makefile := filepath.Join(scriptRepoRoot(t), "Makefile")
	cmd := exec.Command("make", "--no-print-directory", "-n", "-f", makefile, "DEFERRED_TEST_PKGS=", "capcontract-refresh")
	cmd.Dir = t.TempDir()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run capcontract refresh outside a Git worktree: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "fatal: not a git repository") {
		t.Fatalf("generated artifact target eagerly queried Git metadata:\n%s", output)
	}
}

func TestSchemaBuildIdentityFlagExpandsAppCommitLazily(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	assertScriptContains(t, makefile, "SCHEMA_BUILD_IDENTITY_LDFLAG = -X github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema.buildAppCommit=$(APP_COMMIT)")
	assertScriptDoesNotContain(t, makefile, "SCHEMA_BUILD_IDENTITY_LDFLAG :=")
}
