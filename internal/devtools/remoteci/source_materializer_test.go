package remoteci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type sourceImporterStub struct {
	bundle string
	object string
	tree   string
	repo   string
	err    error
}

func (stub *sourceImporterStub) ImportAndVerify(_ context.Context, bundlePath string, expectedObject string, expectedTree string) (string, error) {
	stub.bundle = bundlePath
	stub.object = expectedObject
	stub.tree = expectedTree
	return stub.repo, stub.err
}

func TestImportSourceDelegatesGitTruthToSourceExportOwner(t *testing.T) {
	importer := &sourceImporterStub{repo: "/tmp/verified.git"}
	repo, err := importSource(context.Background(), importer, "/tmp/source.bundle", "commit-sha", "tree-sha")
	if err != nil {
		t.Fatalf("importSource() error = %v", err)
	}
	if repo != importer.repo || importer.bundle != "/tmp/source.bundle" || importer.object != "commit-sha" || importer.tree != "tree-sha" {
		t.Fatalf("import call = %#v, repo = %q", importer, repo)
	}
}

func TestImportSourceFailsClosedOnMissingInputsAndEmptyRepository(t *testing.T) {
	for _, test := range []struct {
		name   string
		bundle string
		object string
		tree   string
		repo   string
	}{
		{name: "bundle", object: "object", tree: "tree", repo: "/tmp/repo"},
		{name: "object", bundle: "/tmp/bundle", tree: "tree", repo: "/tmp/repo"},
		{name: "tree", bundle: "/tmp/bundle", object: "object", repo: "/tmp/repo"},
		{name: "result", bundle: "/tmp/bundle", object: "object", tree: "tree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := importSource(context.Background(), &sourceImporterStub{repo: test.repo}, test.bundle, test.object, test.tree); err == nil {
				t.Fatal("importSource() accepted incomplete source verification")
			}
		})
	}
}

func TestMaterializeVerifiedSourceBundleRoundTrip(t *testing.T) {
	repository := canonicalMaterializerTempDir(t)
	runMaterializerGit(t, repository, "init", "--quiet")
	runMaterializerGit(t, repository, "config", "user.email", "source-materializer@example.invalid")
	runMaterializerGit(t, repository, "config", "user.name", "Source Materializer")
	if err := os.WriteFile(filepath.Join(repository, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runMaterializerGit(t, repository, "add", "candidate.txt")
	runMaterializerGit(t, repository, "commit", "--quiet", "-m", "candidate")
	commit := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}"))
	spec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	}
	artifacts := canonicalMaterializerTempDir(t)
	if _, err := MaterializeSource(context.Background(), repository, spec, artifacts); err != nil {
		t.Fatalf("MaterializeSource() error = %v", err)
	}
	sourceRoot := canonicalMaterializerTempDir(t)
	manifest, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot)
	if err != nil {
		t.Fatalf("MaterializeVerifiedSourceBundle() error = %v", err)
	}
	if manifest.SourceTreeSHA != tree || manifest.MaterializedCommitSHA != commit {
		t.Fatalf("materialized manifest = %#v", manifest)
	}
	content, err := os.ReadFile(filepath.Join(sourceRoot, "candidate.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "candidate\n" {
		t.Fatalf("materialized source = %q", content)
	}
	if got := strings.TrimSpace(runMaterializerGit(t, sourceRoot, "rev-parse", "HEAD^{tree}")); got != tree {
		t.Fatalf("materialized tree = %s, want %s", got, tree)
	}
	if status := strings.TrimSpace(runMaterializerGit(t, sourceRoot, "status", "--porcelain=v1", "--untracked-files=all")); status != "" {
		t.Fatalf("materialized source is dirty: %q", status)
	}
}

func TestMaterializeVerifiedSourceBundleRejectsBundleDigestDrift(t *testing.T) {
	repository := canonicalMaterializerTempDir(t)
	runMaterializerGit(t, repository, "init", "--quiet")
	runMaterializerGit(t, repository, "config", "user.email", "source-materializer@example.invalid")
	runMaterializerGit(t, repository, "config", "user.name", "Source Materializer")
	if err := os.WriteFile(filepath.Join(repository, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runMaterializerGit(t, repository, "add", "candidate.txt")
	runMaterializerGit(t, repository, "commit", "--quiet", "-m", "candidate")
	commit := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}"))
	spec := gate.SourceSpec{Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1, Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree}
	artifacts := canonicalMaterializerTempDir(t)
	if _, err := MaterializeSource(context.Background(), repository, spec, artifacts); err != nil {
		t.Fatalf("MaterializeSource() error = %v", err)
	}
	bundlePath := filepath.Join(artifacts, sourceBundleName)
	if err := os.Chmod(bundlePath, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(bundlePath, os.O_APPEND|os.O_WRONLY, 0o400)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("drift"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	sourceRoot := canonicalMaterializerTempDir(t)
	if _, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot); err == nil {
		t.Fatal("MaterializeVerifiedSourceBundle() accepted bundle digest drift")
	}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed source handoff left entries: %v", entries)
	}
}

func canonicalMaterializerTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func runMaterializerGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
