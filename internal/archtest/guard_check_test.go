package archtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckGuardGeneratedFilesDriftRejectsUnstagedFreezeChange(t *testing.T) {
	root := t.TempDir()
	freezePath := filepath.Join(root, "internal/archtest/freeze_baseline.json")
	if err := os.MkdirAll(filepath.Dir(freezePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freezePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGuardCheckGit(root, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if err := runGuardCheckGit(root, "add", "internal/archtest/freeze_baseline.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(freezePath, []byte("{\"changed\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT", "1")
	err := CheckGuardGeneratedFilesDrift(CheckOptions{RepoRoot: root})
	if err == nil {
		t.Fatal("drift check unexpectedly passed")
	}
	if _, ok := errors.AsType[*GuardFreezeViolationError](err); !ok {
		t.Fatalf("drift error type = %T, want GuardFreezeViolationError", err)
	}
	if !strings.Contains(err.Error(), "internal/archtest/freeze_baseline.json") {
		t.Fatalf("drift error = %q, want freeze path", err)
	}
}

func runGuardCheckGit(root string, args ...string) error {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	return command.Run()
}
