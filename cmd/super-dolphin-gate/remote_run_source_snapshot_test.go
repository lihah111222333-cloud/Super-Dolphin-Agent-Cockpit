package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBuildRemoteRunInventoryUsesResolvedTreeNotCommitRevision(t *testing.T) {
	repository := initRemoteRunGitFixture(t)
	revision := remoteRunGitOutput(t, repository, "rev-parse", "HEAD")
	packagePath := filepath.Join(repository, "internal", "tree-target")
	if err := os.MkdirAll(packagePath, 0o700); err != nil {
		t.Fatalf("create tree-target package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "tree.go"), []byte("package treetarget\n"), 0o600); err != nil {
		t.Fatalf("write tree-target package: %v", err)
	}
	runRemoteRunGit(t, repository, "add", "internal/tree-target/tree.go")
	runRemoteRunGit(t, repository, "commit", "--quiet", "-m", "树目标")
	candidateTree := remoteRunGitOutput(t, repository, "rev-parse", "HEAD^{tree}")

	target := remoteResolvedTarget{
		commit: revision, tree: candidateTree, revision: revision, sourceBase: revision,
	}
	inventory, snapshot, err := buildRemoteRunInventory(repository, target, "commit", nil, "linux/amd64")
	if err != nil {
		t.Fatalf("buildRemoteRunInventory() error = %v", err)
	}
	if snapshot == nil {
		t.Fatal("buildRemoteRunInventory() returned nil inventory snapshot")
	}
	if !slices.Contains(inventory.GoPackages, "./internal/tree-target") {
		t.Fatalf("inventory packages = %v, want tree-bound package from %s", inventory.GoPackages, candidateTree)
	}
}
