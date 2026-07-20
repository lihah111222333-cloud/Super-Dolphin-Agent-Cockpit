package localci

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestLoadReadOnlyGitTreeLoadsLargeDuplicateBlobTreeWithinBound(t *testing.T) {
	repo := newSourceTestRepository(t)
	blob := repo.outputLine(t, strings.NewReader("shared content"), "hash-object", "-w", "--stdin")
	var treeInput strings.Builder
	for index := range 2_500 {
		fmt.Fprintf(&treeInput, "100644 blob %s\tfile-%04d.txt\n", blob, index)
	}
	treeOID := repo.outputLine(t, strings.NewReader(treeInput.String()), "mktree")
	commitOID := repo.commitTree(t, treeOID, "")
	spec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commitOID}, SourceTreeSHA: treeOID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := time.Now()
	tree, err := LoadReadOnlyGitTree(ctx, repo.root, spec)
	if err != nil {
		t.Fatalf("LoadReadOnlyGitTree() error = %v", err)
	}
	if len(tree.Entries) != 2_500 {
		t.Fatalf("entries = %d, want 2500", len(tree.Entries))
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("large tree load took %s", elapsed)
	}
}

func TestParseReadOnlyTreeBlobBatchRejectsOrderAndTrailingOutput(t *testing.T) {
	first := strings.Repeat("1", 40)
	second := strings.Repeat("2", 40)
	for _, output := range [][]byte{
		[]byte(second + " blob 3\ntwo\n" + first + " blob 3\none\n"),
		[]byte(first + " blob 3\none\n" + second + " blob 3\ntwo\ntrailing"),
	} {
		if _, err := parseReadOnlyTreeBlobBatch(output, []string{first, second}); err == nil {
			t.Fatal("parseReadOnlyTreeBlobBatch() accepted protocol drift")
		}
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
