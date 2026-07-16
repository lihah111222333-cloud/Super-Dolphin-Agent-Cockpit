package localci

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestMaterializeSourceCommitRoundTrip(t *testing.T) {
	repo := newSourceTestRepository(t)
	firstTree := repo.writeTree(t, "first")
	firstCommit := repo.commitTree(t, firstTree, "")
	secondTree := repo.writeTree(t, "second")
	secondCommit := repo.commitTree(t, secondTree, firstCommit)
	repo.run(t, nil, "update-ref", "refs/heads/other", secondCommit)

	spec := gate.SourceSpec{
		Kind:          gate.SourceKindCommit,
		ObjectFormat:  gate.GitObjectFormatSHA1,
		Commit:        &gate.CommitSource{SHA: firstCommit},
		SourceTreeSHA: firstTree,
	}
	result := materializeAndVerify(t, repo.root, spec)
	if result.Manifest.MaterializedCommitSHA != firstCommit {
		t.Fatalf("materialized commit = %s, want %s", result.Manifest.MaterializedCommitSHA, firstCommit)
	}
	if result.Manifest.SourceTreeSHA != firstTree {
		t.Fatalf("source tree = %s, want %s", result.Manifest.SourceTreeSHA, firstTree)
	}
}

func TestMaterializeSourceRangeRoundTrip(t *testing.T) {
	repo := newSourceTestRepository(t)
	baseTree := repo.writeTree(t, "base")
	base := repo.commitTree(t, baseTree, "")
	headTree := repo.writeTree(t, "head")
	head := repo.commitTree(t, headTree, base)

	spec := gate.SourceSpec{
		Kind:         gate.SourceKindRange,
		ObjectFormat: gate.GitObjectFormatSHA1,
		Range: &gate.RangeSource{
			BaseKind:          gate.BaseKindCommit,
			BaseSHA:           base,
			HeadSHA:           head,
			LocalRef:          "refs/heads/main",
			RemoteRef:         "refs/heads/main",
			ObservedRemoteSHA: base,
			UpdateKind:        gate.UpdateKindFastForward,
		},
		SourceTreeSHA: headTree,
	}
	result := materializeAndVerify(t, repo.root, spec)
	if result.Manifest.MaterializedCommitSHA != head {
		t.Fatalf("materialized range head = %s, want %s", result.Manifest.MaterializedCommitSHA, head)
	}
	if result.Manifest.SyntheticCommitSHA != "" {
		t.Fatalf("range unexpectedly created synthetic commit %s", result.Manifest.SyntheticCommitSHA)
	}
}

func TestMaterializeSourceNewBranchEmptyTreeDoesNotForgeBase(t *testing.T) {
	repo := newSourceTestRepository(t)
	tree := repo.writeTree(t, "new branch")
	head := repo.commitTree(t, tree, "")
	zeroOID := strings.Repeat("0", 40)

	spec := gate.SourceSpec{
		Kind:         gate.SourceKindRange,
		ObjectFormat: gate.GitObjectFormatSHA1,
		Range: &gate.RangeSource{
			BaseKind:          gate.BaseKindEmptyTree,
			HeadSHA:           head,
			LocalRef:          "refs/heads/new",
			RemoteRef:         "refs/heads/new",
			ObservedRemoteSHA: zeroOID,
			UpdateKind:        gate.UpdateKindCreate,
		},
		SourceTreeSHA: tree,
	}
	result := materializeAndVerify(t, repo.root, spec)
	if result.Manifest.MaterializedCommitSHA != head || result.Manifest.SyntheticCommitSHA != "" {
		t.Fatalf("empty-tree range forged a base or commit: %+v", result.Manifest)
	}
}

func TestMaterializeSourceDanglingStagedTreeCreatesSyntheticCommitWithoutRepositoryRef(t *testing.T) {
	repo := newSourceTestRepository(t)
	parentTree := repo.writeTree(t, "parent")
	parent := repo.commitTree(t, parentTree, "")
	repo.run(t, nil, "update-ref", "refs/heads/main", parent)
	repo.run(t, nil, "read-tree", parent)
	if err := os.WriteFile(filepath.Join(repo.root, "staged.txt"), []byte("staged"), 0o600); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	repo.run(t, nil, "add", "--", "staged.txt")
	danglingTree := repo.outputLine(t, nil, "write-tree")
	refsBefore := repo.output(t, nil, "for-each-ref", "--format=%(refname) %(objectname)")

	spec := gate.SourceSpec{
		Kind:          gate.SourceKindTree,
		ObjectFormat:  gate.GitObjectFormatSHA1,
		Tree:          &gate.TreeSource{SHA: danglingTree, ParentCommitSHA: parent},
		SourceTreeSHA: danglingTree,
	}
	result := materializeAndVerify(t, repo.root, spec)
	if result.Manifest.SyntheticCommitSHA == "" || result.Manifest.SyntheticCommitSHA == parent {
		t.Fatalf("synthetic commit = %q, parent = %q", result.Manifest.SyntheticCommitSHA, parent)
	}
	refsAfter := repo.output(t, nil, "for-each-ref", "--format=%(refname) %(objectname)")
	if !bytes.Equal(refsBefore, refsAfter) {
		t.Fatalf("real repository refs changed:\nbefore: %s\nafter: %s", refsBefore, refsAfter)
	}
}

func TestMaterializeSourceRejectsTreeMismatchAndWrongObjectType(t *testing.T) {
	repo := newSourceTestRepository(t)
	tree := repo.writeTree(t, "tree")
	commit := repo.commitTree(t, tree, "")
	blob := repo.outputLine(t, strings.NewReader("blob"), "hash-object", "-w", "--stdin")

	tests := []struct {
		name string
		spec gate.SourceSpec
	}{
		{
			name: "tree mismatch",
			spec: gate.SourceSpec{
				Kind:          gate.SourceKindCommit,
				ObjectFormat:  gate.GitObjectFormatSHA1,
				Commit:        &gate.CommitSource{SHA: commit},
				SourceTreeSHA: strings.Repeat("a", 40),
			},
		},
		{
			name: "blob used as commit",
			spec: gate.SourceSpec{
				Kind:          gate.SourceKindCommit,
				ObjectFormat:  gate.GitObjectFormatSHA1,
				Commit:        &gate.CommitSource{SHA: blob},
				SourceTreeSHA: tree,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outputRoot := newPrivateSourceOutput(t)
			if _, err := MaterializeSource(context.Background(), repo.root, test.spec, outputRoot); err == nil {
				t.Fatal("MaterializeSource unexpectedly succeeded")
			}
		})
	}
}

func TestMaterializeSourceFailFastBoundaries(t *testing.T) {
	repo := newSourceTestRepository(t)
	tree := repo.writeTree(t, "source")
	commit := repo.commitTree(t, tree, "")
	spec := gate.SourceSpec{
		Kind:          gate.SourceKindCommit,
		ObjectFormat:  gate.GitObjectFormatSHA1,
		Commit:        &gate.CommitSource{SHA: commit},
		SourceTreeSHA: tree,
	}

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := MaterializeSource(ctx, repo.root, spec, newPrivateSourceOutput(t))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("trailing output", func(t *testing.T) {
		outputRoot := newPrivateSourceOutput(t)
		if err := os.WriteFile(filepath.Join(outputRoot, "unexpected"), []byte("x"), 0o600); err != nil {
			t.Fatalf("write trailing output: %v", err)
		}
		if _, err := MaterializeSource(context.Background(), repo.root, spec, outputRoot); err == nil {
			t.Fatal("MaterializeSource accepted trailing output")
		}
	})

	t.Run("symlink output", func(t *testing.T) {
		parent := t.TempDir()
		target := newPrivateSourceOutput(t)
		link := filepath.Join(parent, "output-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("create output symlink: %v", err)
		}
		if _, err := MaterializeSource(context.Background(), repo.root, spec, link); err == nil {
			t.Fatal("MaterializeSource accepted symlink output")
		}
	})

	t.Run("typed nil Git input", func(t *testing.T) {
		var typedNil *bytes.Reader
		var output bytes.Buffer
		err := runGit(context.Background(), repo.root, typedNil, &output, "rev-parse", "--show-object-format")
		if err == nil || !strings.Contains(err.Error(), "typed nil") {
			t.Fatalf("runGit error = %v, want typed nil rejection", err)
		}
	})
}

func materializeAndVerify(t *testing.T, repoRoot string, spec gate.SourceSpec) SourceMaterialization {
	t.Helper()
	outputRoot := newPrivateSourceOutput(t)
	result, err := MaterializeSource(context.Background(), repoRoot, spec, outputRoot)
	if err != nil {
		t.Fatalf("MaterializeSource: %v", err)
	}
	manifest, err := ImportAndVerifySourceBundle(context.Background(), outputRoot)
	if err != nil {
		t.Fatalf("ImportAndVerifySourceBundle: %v", err)
	}
	if manifest.MaterializedCommitSHA != result.Manifest.MaterializedCommitSHA {
		t.Fatalf("roundtrip commit = %s, want %s", manifest.MaterializedCommitSHA, result.Manifest.MaterializedCommitSHA)
	}
	for _, path := range []string{result.BundlePath, result.ManifestPath} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatalf("lstat artifact %s: %v", path, err)
		}
		if info.Mode().Perm() != privateSourceFileMode || !info.Mode().IsRegular() {
			t.Fatalf("artifact %s mode = %v, want regular 0400", path, info.Mode())
		}
	}
	return result
}

func newPrivateSourceOutput(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize output parent: %v", err)
	}
	path := filepath.Join(parent, "output")
	if err := os.Mkdir(path, privateSourceDirMode); err != nil {
		t.Fatalf("create private source output: %v", err)
	}
	return path
}

type sourceTestRepository struct {
	root string
}

func newSourceTestRepository(t *testing.T) sourceTestRepository {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize repository parent: %v", err)
	}
	root := filepath.Join(parent, "repo--config-injection")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("create source repository: %v", err)
	}
	repo := sourceTestRepository{root: root}
	repo.run(t, nil, "init", "-q")
	return repo
}

func (repo sourceTestRepository) writeTree(t *testing.T, content string) string {
	t.Helper()
	blob := repo.outputLine(t, strings.NewReader(content), "hash-object", "-w", "--stdin")
	entry := "100644 blob " + blob + "\tfile.txt\n"
	return repo.outputLine(t, strings.NewReader(entry), "mktree")
}

func (repo sourceTestRepository) commitTree(t *testing.T, tree string, parent string) string {
	t.Helper()
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return repo.outputLine(t, strings.NewReader("test commit\n"), args...)
}

func (repo sourceTestRepository) outputLine(t *testing.T, stdin io.Reader, args ...string) string {
	t.Helper()
	output := repo.output(t, stdin, args...)
	line := strings.TrimSuffix(string(output), "\n")
	if line == string(output) || strings.Contains(line, "\n") {
		t.Fatalf("git %s returned non-line output %q", args[0], output)
	}
	return line
}

func (repo sourceTestRepository) run(t *testing.T, stdin io.Reader, args ...string) {
	t.Helper()
	_ = repo.output(t, stdin, args...)
}

func (repo sourceTestRepository) output(t *testing.T, stdin io.Reader, args ...string) []byte {
	t.Helper()
	command := exec.Command("git", append([]string{"--no-replace-objects", "-C", repo.root}, args...)...)
	command.Stdin = stdin
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Source Test",
		"GIT_AUTHOR_EMAIL=source-test.invalid",
		"GIT_AUTHOR_DATE=2001-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Source Test",
		"GIT_COMMITTER_EMAIL=source-test.invalid",
		"GIT_COMMITTER_DATE=2001-01-01T00:00:00Z",
		"LC_ALL=C",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", args[0], err, output)
	}
	return output
}
