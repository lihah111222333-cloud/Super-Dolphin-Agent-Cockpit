package localci

import (
	"context"
	"path/filepath"
	"testing"

	gate "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestLoadReadOnlyBootstrapTreeReadsBareAuthorityAndRejectsWorktree(t *testing.T) {
	repository := newSourceTestRepository(t)
	treeOID := repository.writeTree(t, "trusted bootstrap content")
	commitOID := repository.commitTree(t, treeOID, "")
	spec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commitOID}, SourceTreeSHA: treeOID,
	}
	bare := filepath.Join(t.TempDir(), "trusted.git")
	repository.run(t, nil, "clone", "-q", "--bare", "--", repository.root, bare)
	canonicalBare, err := filepath.EvalSymlinks(bare)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := LoadReadOnlyBootstrapTree(context.Background(), canonicalBare, spec)
	if err != nil {
		t.Fatalf("LoadReadOnlyBootstrapTree() error = %v", err)
	}
	if tree.Source.SourceTreeSHA != treeOID || len(tree.Entries) == 0 {
		t.Fatalf("bootstrap tree = %#v", tree)
	}
	if _, err := LoadReadOnlyBootstrapTree(context.Background(), repository.root, spec); err == nil {
		t.Fatal("LoadReadOnlyBootstrapTree() accepted a worktree")
	}
}
