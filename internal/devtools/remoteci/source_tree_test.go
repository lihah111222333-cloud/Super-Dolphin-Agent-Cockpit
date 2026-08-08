package remoteci

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestVerifyObjectClosureIgnoresUnreachableObjects(t *testing.T) {
	ctx := context.Background()
	bareRoot := filepath.Join(t.TempDir(), "source.git")
	if err := initBareRepository(ctx, filepath.Dir(bareRoot), bareRoot, gate.GitObjectFormatSHA1); err != nil {
		t.Fatalf("initBareRepository() error = %v", err)
	}
	treeOutput, err := runGitOutput(ctx, bareRoot, strings.NewReader(""), "mktree")
	if err != nil {
		t.Fatalf("create empty tree: %v", err)
	}
	tree, err := strictGitLine(treeOutput)
	if err != nil {
		t.Fatalf("parse empty tree: %v", err)
	}
	commitOutput, err := runGitOutput(ctx, bareRoot, bytes.NewReader(deterministicSourceBaselinePayload(tree)), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		t.Fatalf("create reachable commit: %v", err)
	}
	commit, err := strictGitLine(commitOutput)
	if err != nil {
		t.Fatalf("parse reachable commit: %v", err)
	}
	if _, err := runGitOutput(ctx, bareRoot, strings.NewReader("unreachable\n"), "hash-object", "-w", "--stdin"); err != nil {
		t.Fatalf("create unreachable object: %v", err)
	}
	if err := verifyObjectClosure(ctx, bareRoot, commit); err != nil {
		t.Fatalf("verifyObjectClosure() rejected unrelated unreachable object: %v", err)
	}
}

func TestVerifySyntheticBaseCommitRequiresDeterministicSingleParent(t *testing.T) {
	const (
		tree     = "1111111111111111111111111111111111111111"
		baseline = "2222222222222222222222222222222222222222"
	)
	expected, err := DeterministicSourceSyntheticBaseCommitSHA(tree, baseline, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceSyntheticBaseCommitSHA() error = %v", err)
	}
	validManifest := SourceMaterializationManifest{
		SyntheticBaseTreeSHA:   tree,
		SyntheticBaseCommitSHA: expected,
		ObjectFormat:           gate.GitObjectFormatSHA1,
	}
	validObject := sourceObject{
		oid:  expected,
		kind: "commit",
		data: fmt.Appendf(nil, "tree %s\nparent %s\n\n", tree, baseline),
	}
	for _, test := range []struct {
		name     string
		manifest SourceMaterializationManifest
		object   sourceObject
		wantErr  bool
	}{
		{name: "valid", manifest: validManifest, object: validObject},
		{
			name:     "wrong parent",
			manifest: validManifest,
			object: sourceObject{
				oid:  expected,
				kind: "commit",
				data: fmt.Appendf(nil, "tree %s\nparent %s\n\n", tree, strings.Repeat("3", 40)),
			},
			wantErr: true,
		},
		{
			name: "wrong deterministic identity",
			manifest: SourceMaterializationManifest{
				SyntheticBaseTreeSHA:   tree,
				SyntheticBaseCommitSHA: strings.Repeat("4", 40),
				ObjectFormat:           gate.GitObjectFormatSHA1,
			},
			object:  validObject,
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifySyntheticBaseCommit(test.object, test.manifest, SourceBaseline{
				CommitSHA:    baseline,
				ObjectFormat: gate.GitObjectFormatSHA1,
			})
			if (err != nil) != test.wantErr {
				t.Fatalf("verifySyntheticBaseCommit() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
