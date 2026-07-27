package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCodexStopGatePlanHasIndependentGitAndCIOwner(t *testing.T) {
	root := scriptRepoRoot(t)
	command := exec.Command("bash", filepath.Join(root, "scripts", "tests", "test_codex_stop_gate_plan.sh"))
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("codex stop gate plan self-test: %v\n%s", err, output)
	}
}
