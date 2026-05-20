package workspace

import (
	"path/filepath"
	"testing"
)

func TestCollectRemovedWorkspaceFilesTreatsMissingWorkspaceFileAsRemoved(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	workspacePath := t.TempDir()
	writeWorkspaceTestFile(t, sourceRoot, "removed.txt", "baseline")
	baseline := workspaceHashTestFile(t, filepath.Join(sourceRoot, "removed.txt"))
	run := &Run{RunKey: "run-removed", SourceRoot: sourceRoot, WorkspacePath: workspacePath}
	files := []RunFile{{
		RunKey:             run.RunKey,
		RelativePath:       "removed.txt",
		BaselineSHA256:     baseline,
		SourceSHA256Before: baseline,
		State:              fileStateTracked,
	}}

	removed, err := (&service{}).collectRemovedWorkspaceFiles(run, files, true)
	if err != nil {
		t.Fatalf("collectRemovedWorkspaceFiles() error = %v", err)
	}
	got, ok := removed["removed.txt"]
	if !ok {
		t.Fatalf("removed files = %#v, want removed.txt", removed)
	}
	if got.SourceSHA256Before != baseline {
		t.Fatalf("SourceSHA256Before = %q, want baseline %q", got.SourceSHA256Before, baseline)
	}
}
