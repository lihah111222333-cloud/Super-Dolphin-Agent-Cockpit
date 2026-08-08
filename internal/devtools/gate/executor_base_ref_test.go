package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustedSnapshotBaseRejectsBaseParentDrift(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"base.txt": "base\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	writeTestFile(t, filepath.Join(source, "middle.txt"), "middle\n", 0o600)
	commitExecutorSnapshot(t, source, "unexpected middle parent")
	writeTestFile(t, filepath.Join(source, "candidate.txt"), "candidate\n", 0o600)
	commitExecutorSnapshot(t, source, "transport candidate")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	_, err = trustedSnapshotBase(context.Background(), gitPath, source, os.Environ())
	if err == nil || !strings.Contains(err.Error(), "must match the unique transport parent") {
		t.Fatalf("trustedSnapshotBase() error = %v", err)
	}
}
