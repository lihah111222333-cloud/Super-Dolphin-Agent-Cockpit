package localci

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestLoadReadOnlyGitTreeReadsSourceSpecObjectTree(t *testing.T) {
	repo := newSourceTestRepository(t)
	treeOID := repo.writeTree(t, "trusted object content")
	commitOID := repo.commitTree(t, treeOID, "")
	spec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commitOID}, SourceTreeSHA: treeOID,
	}

	tree, err := LoadReadOnlyGitTree(context.Background(), repo.root, spec)
	if err != nil {
		t.Fatalf("LoadReadOnlyGitTree() error = %v", err)
	}
	if tree.Source.SourceTreeSHA != treeOID || len(tree.Entries) != 1 {
		t.Fatalf("tree = %#v", tree)
	}
	if got := string(tree.Entries[0].Data); got != "trusted object content" {
		t.Fatalf("tree content = %q", got)
	}
}

func TestLoadReadOnlyGitTreeRejectsSourceTreeMismatch(t *testing.T) {
	repo := newSourceTestRepository(t)
	treeOID := repo.writeTree(t, "content")
	commitOID := repo.commitTree(t, treeOID, "")
	spec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commitOID}, SourceTreeSHA: repo.writeTree(t, "other"),
	}
	if _, err := LoadReadOnlyGitTree(context.Background(), repo.root, spec); err == nil {
		t.Fatal("LoadReadOnlyGitTree() accepted mismatched SourceSpec tree")
	}
}
