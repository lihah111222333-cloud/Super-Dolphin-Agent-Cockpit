package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMergedSourceFileRejectsWorkspaceSymlinkEscape(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	outside := t.TempDir()
	writeWorkspaceTestFile(t, sourceRoot, "reports/final.md", "baseline")
	if err := os.WriteFile(filepath.Join(outside, "final.md"), []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(workspacePath, "reports")); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	run := &Run{RunKey: "run-link-escape", SourceRoot: sourceRoot, WorkspacePath: workspacePath}
	file := RunFile{
		RunKey:       run.RunKey,
		RelativePath: "reports/final.md",
		State:        fileStateMerged,
	}

	updated, item := writeMergedSourceFile(run, file, MergeFileResult{Path: file.RelativePath, Action: "merged"})

	if updated.State != fileStateError || item.Action != "error" {
		t.Fatalf("writeMergedSourceFile() = state %q action %q, want error", updated.State, item.Action)
	}
	if !strings.Contains(item.Reason, "escapes workspace root") {
		t.Fatalf("error reason = %q, want workspace escape rejection", item.Reason)
	}
	if got := readWorkspaceTestFile(t, sourceRoot, "reports/final.md"); got != "baseline" {
		t.Fatalf("source content = %q, want baseline", got)
	}
}
