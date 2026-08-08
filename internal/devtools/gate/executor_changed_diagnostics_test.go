package gate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTrustedChangedDiagnosticsUsesSnapshotBaseAndFiltersUnsupportedFiles(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"base.go": "package base\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	writeTestFile(t, filepath.Join(source, "internal", "changed.go"), "package changed\n", 0o600)
	writeTestFile(t, filepath.Join(source, "internal", "notes.md"), "notes\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "asset.png"), "png\n", 0o600)
	commitExecutorSnapshot(t, source, "changed")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	selection, err := trustedChangedDiagnostics(context.Background(), gitPath, source, os.Environ())
	if err != nil {
		t.Fatalf("trustedChangedDiagnostics: %v", err)
	}
	if !slices.Equal(selection.files, []string{"internal/changed.go"}) {
		t.Fatalf("changed files = %v, want [internal/changed.go]", selection.files)
	}
	if selection.unsupported != 2 {
		t.Fatalf("unsupported files = %d, want 2", selection.unsupported)
	}
}

func TestTrustedChangedDiagnosticsDeletionOnlyIsLegalSkip(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"internal/deleted.go": "package deleted\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	if err := os.Remove(filepath.Join(source, "internal", "deleted.go")); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "delete")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	selection, err := trustedChangedDiagnostics(context.Background(), gitPath, source, os.Environ())
	if err != nil {
		t.Fatalf("trustedChangedDiagnostics: %v", err)
	}
	if len(selection.files) != 0 || selection.deleted != 1 {
		t.Fatalf("deletion selection = %+v, want one deleted and no live files", selection)
	}
}

func TestRunChangedDiagnosticsDeletionOnlySkipsBeforeToolResolution(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"internal/deleted.go": "package deleted\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	if err := os.Remove(filepath.Join(source, "internal", "deleted.go")); err != nil {
		t.Fatal(err)
	}
	commitExecutorSnapshot(t, source, "delete")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	var stderr bytes.Buffer
	err = runChangedDiagnostics(context.Background(), gitPath, source, os.Environ(), filepath.Join(source, "missing-bin"), ioDiscard{}, &stderr)
	if err != nil {
		t.Fatalf("runChangedDiagnostics deletion-only skip: %v", err)
	}
	if !strings.Contains(stderr.String(), "deleted=1") {
		t.Fatalf("skip audit = %q, want deleted=1", stderr.String())
	}
}

func TestRunChangedDiagnosticsEmptyTrustedRangeIsLegalSkip(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{"internal/unchanged.go": "package unchanged\n"})
	runGit(t, source, "update-ref", baseSourceRef, "HEAD")
	runGit(t, source, "-c", "user.name=executor-test", "-c", "user.email=executor@example.invalid", "commit", "-q", "--allow-empty", "-m", "transport candidate")
	runGit(t, source, "update-ref", materializedSourceRef, "HEAD")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	var stderr bytes.Buffer
	err = runChangedDiagnostics(context.Background(), gitPath, source, os.Environ(), filepath.Join(source, "missing-bin"), ioDiscard{}, &stderr)
	if err != nil {
		t.Fatalf("runChangedDiagnostics empty trusted range: %v", err)
	}
	if !strings.Contains(stderr.String(), "candidates=0") {
		t.Fatalf("skip audit = %q, want candidates=0", stderr.String())
	}
}

func TestTrustedChangedDiagnosticsMissingBaseFailsFast(t *testing.T) {
	source := newExecutorGitSnapshot(t, map[string]string{
		"internal/kept.go":  "package kept\n",
		"internal/notes.md": "notes\n",
		"config.json":       "{}\n",
	})
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required")
	}
	if _, err := trustedChangedDiagnostics(context.Background(), gitPath, source, os.Environ()); err == nil || !strings.Contains(err.Error(), "materialized source base ref is required") {
		t.Fatalf("trustedChangedDiagnostics missing base error = %v", err)
	}
}
