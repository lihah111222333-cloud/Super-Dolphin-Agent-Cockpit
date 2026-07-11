package sourceexport

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLoadGitTreeReadsCommittedBlobsAndModes(t *testing.T) {
	repo := newGitTreeFixture(t)
	writeGitTreeFile(t, repo, "README.md", "committed\n", 0o644)
	writeGitTreeFile(t, repo, "scripts/run.sh", "#!/bin/sh\n", 0o755)
	commitGitTreeFixture(t, repo)

	commit, entries, err := loadGitTree(context.Background(), execGitRunner{}, repo, "HEAD")
	if err != nil {
		t.Fatalf("loadGitTree() error = %v", err)
	}
	if len(commit) != 40 {
		t.Fatalf("commit length = %d, want 40", len(commit))
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Path != "README.md" || string(entries[0].Data) != "committed\n" || entries[0].Mode != "100644" {
		t.Fatalf("README entry = %#v", entries[0])
	}
	if entries[1].Path != "scripts/run.sh" || entries[1].Mode != "100755" {
		t.Fatalf("script entry = %#v", entries[1])
	}
}

func TestEnsureSourceCleanRejectsTrackedChanges(t *testing.T) {
	repo := newGitTreeFixture(t)
	writeGitTreeFile(t, repo, "README.md", "committed\n", 0o644)
	commitGitTreeFixture(t, repo)
	writeGitTreeFile(t, repo, "README.md", "dirty\n", 0o644)

	err := ensureSourceClean(context.Background(), execGitRunner{}, repo)
	assertErrorCode(t, err, CodeSourceDirty)
}

func TestLoadGitTreeRejectsSymlink(t *testing.T) {
	repo := newGitTreeFixture(t)
	writeGitTreeFile(t, repo, "target.txt", "target\n", 0o644)
	if err := os.Symlink("target.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	commitGitTreeFixture(t, repo)

	_, _, err := loadGitTree(context.Background(), execGitRunner{}, repo, "HEAD")
	assertErrorCode(t, err, CodeSymlinkRejected)
}

func TestValidateTreeEntriesRejectsCaseCollisionAndSubmodule(t *testing.T) {
	tests := []struct {
		name    string
		entries []TreeEntry
		code    Code
	}{
		{name: "case collision", entries: []TreeEntry{{Path: "README.md", Mode: "100644"}, {Path: "readme.md", Mode: "100644"}}, code: CodeCaseCollision},
		{name: "submodule", entries: []TreeEntry{{Path: "vendor/repo", Mode: "160000"}}, code: CodeForbiddenPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertErrorCode(t, validateTreeEntries(tt.entries), tt.code)
		})
	}
}

func newGitTreeFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitTreeCommand(t, repo, "init", "-q")
	runGitTreeCommand(t, repo, "config", "user.email", "test@example.com")
	runGitTreeCommand(t, repo, "config", "user.name", "Source Export Test")
	return repo
}

func writeGitTreeFile(t *testing.T, repo string, name string, content string, mode os.FileMode) {
	t.Helper()
	filePath := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func commitGitTreeFixture(t *testing.T, repo string) {
	t.Helper()
	runGitTreeCommand(t, repo, "add", "--all")
	runGitTreeCommand(t, repo, "commit", "-qm", "fixture")
}

func runGitTreeCommand(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
