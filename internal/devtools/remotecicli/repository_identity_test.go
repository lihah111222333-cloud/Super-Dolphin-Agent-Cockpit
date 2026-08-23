package remotecicli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryStorageIdentityTracksGitDirAndCommonDir(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(root, "worktree.git")
	commonOne := filepath.Join(root, "common-one")
	commonTwo := filepath.Join(root, "common-two")
	for _, directory := range []string{gitDir, commonOne, commonTwo} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../common-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: worktree.git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := repositoryStorageIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../common-two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := repositoryStorageIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("repositoryStorageIdentity ignored common-dir replacement")
	}
}

func TestValidateStableBoundedReadRejectsDescriptorMutationSignals(t *testing.T) {
	root := t.TempDir()
	firstPath := filepath.Join(root, "first")
	secondPath := filepath.Join(root, "second")
	if err := os.WriteFile(firstPath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                       string
		readErr, statErr, closeErr error
		opened, final              os.FileInfo
	}{
		{name: "read", readErr: errors.New("read"), opened: first, final: first},
		{name: "stat", statErr: errors.New("stat"), opened: first, final: first},
		{name: "close", closeErr: errors.New("close"), opened: first, final: first},
		{name: "replacement", opened: first, final: second},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateStableBoundedRead(testCase.readErr, testCase.statErr, testCase.closeErr, testCase.opened, testCase.final); err == nil {
				t.Fatal("stable bounded read accepted mutation signal")
			}
		})
	}
}
