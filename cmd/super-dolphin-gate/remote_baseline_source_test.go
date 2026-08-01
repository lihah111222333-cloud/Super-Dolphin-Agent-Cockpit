package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestBuildRemoteBaselineSourceArtifactFullThenDelta(t *testing.T) {
	repository := initRemoteBaselineSourceRepository(t)
	ancestorCommit, _ := commitRemoteBaselineSourceFile(t, repository, "ancestor\n")
	firstCommit, firstTree := commitRemoteBaselineSourceFile(t, repository, "first\n")
	full := requireRemoteBaselineSourceArtifact(t,
		context.Background(),
		repository,
		remoteci.BaselineState{},
		remoteci.BaselineIdentity{MainCommit: firstCommit, MainTree: firstTree},
		filepath.Join(t.TempDir(), "full"),
	)
	assertRemoteBaselineFullArtifact(t, full)
	materialized := filepath.Join(t.TempDir(), "materialized")
	materializeRemoteBaselineFullBundle(t, full, materialized)
	assertRemoteBaselineSourceIdentity(t, materialized, firstCommit, firstTree)
	runRemoteBaselineTestGit(t, materialized, "cat-file", "-e", ancestorCommit+"^{commit}")
	runRemoteBaselineTestGit(t, materialized, "merge-base", "--is-ancestor", ancestorCommit, firstCommit)
	if _, err := os.Stat(filepath.Join(materialized, ".git", "shallow")); !os.IsNotExist(err) {
		t.Fatalf("full baseline materialized a shallow repository: %v", err)
	}

	secondCommit, secondTree := commitRemoteBaselineSourceFile(t, repository, "second\n")
	accepted := remoteci.BaselineState{
		SchemaVersion:        remoteci.BaselineStateSchemaVersion,
		SourceHistoryVersion: remoteci.BaselineSourceHistorySchemaVersion,
		MainCommit:           firstCommit,
		MainTree:             firstTree,
	}
	delta := requireRemoteBaselineSourceArtifact(t,
		context.Background(),
		repository,
		accepted,
		remoteci.BaselineIdentity{MainCommit: secondCommit, MainTree: secondTree},
		filepath.Join(t.TempDir(), "delta"),
	)
	assertRemoteBaselineDeltaArtifact(t, delta, firstCommit, firstTree)
	runRemoteBaselineTestGit(t, materialized, "fetch", "--quiet", delta.BundlePath, secondCommit)
	runRemoteBaselineTestGit(t, materialized, "checkout", "--quiet", "--detach", "FETCH_HEAD")
	runRemoteBaselineTestGit(t, materialized, "fsck", "--full")
	assertRemoteBaselineSourceIdentity(t, materialized, secondCommit, secondTree)
}

func TestBuildRemoteBaselineSourceArtifactReusesAcceptedMain(t *testing.T) {
	repository := initRemoteBaselineSourceRepository(t)
	commit, tree := commitRemoteBaselineSourceFile(t, repository, "same\n")
	accepted := remoteci.BaselineState{
		SchemaVersion:        remoteci.BaselineStateSchemaVersion,
		SourceHistoryVersion: remoteci.BaselineSourceHistorySchemaVersion,
		MainCommit:           commit,
		MainTree:             tree,
	}
	artifact := requireRemoteBaselineSourceArtifact(t,
		context.Background(),
		repository,
		accepted,
		remoteci.BaselineIdentity{MainCommit: commit, MainTree: tree},
		t.TempDir(),
	)

	if artifact.Manifest.Mode != remoteBaselineSourceReuse ||
		artifact.BundlePath != "" ||
		artifact.Manifest.BundleSHA256 != "" ||
		artifact.Manifest.BundleSize != 0 {
		t.Fatalf("reuse artifact = %#v", artifact)
	}
}

func TestBuildRemoteBaselineSourceArtifactRebuildsLegacyHistoryAtSameMain(t *testing.T) {
	repository := initRemoteBaselineSourceRepository(t)
	commit, tree := commitRemoteBaselineSourceFile(t, repository, "same\n")
	accepted := remoteci.BaselineState{
		SchemaVersion:        remoteci.BaselineStateSchemaVersion,
		SourceHistoryVersion: 0,
		MainCommit:           commit,
		MainTree:             tree,
	}
	artifact := requireRemoteBaselineSourceArtifact(t,
		context.Background(),
		repository,
		accepted,
		remoteci.BaselineIdentity{MainCommit: commit, MainTree: tree},
		t.TempDir(),
	)

	assertRemoteBaselineFullArtifact(t, artifact)
}

func initRemoteBaselineSourceRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	runRemoteBaselineTestGit(t, t.TempDir(), "init", "--quiet", repository)
	runRemoteBaselineTestGit(t, repository, "config", "user.name", "Remote CI Test")
	runRemoteBaselineTestGit(t, repository, "config", "user.email", "remote-ci@example.invalid")
	return repository
}

func commitRemoteBaselineSourceFile(t *testing.T, repository string, contents string) (string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, "source.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	runRemoteBaselineTestGit(t, repository, "add", "source.txt")
	runRemoteBaselineTestGit(t, repository, "commit", "--quiet", "-m", "测试基线")
	return remoteBaselineTestGitOutput(t, repository, "rev-parse", "HEAD"),
		remoteBaselineTestGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
}

func materializeRemoteBaselineFullBundle(
	t *testing.T,
	artifact remoteBaselineSourceArtifact,
	destination string,
) {
	t.Helper()
	runRemoteBaselineTestGit(t, t.TempDir(), "clone", "--quiet", "--no-checkout", artifact.BundlePath, destination)
	runRemoteBaselineTestGit(t, destination, "checkout", "--quiet", "--detach", artifact.Manifest.TargetCommit)
	runRemoteBaselineTestGit(t, destination, "remote", "remove", "origin")
	runRemoteBaselineTestGit(t, destination, "fsck", "--full")
}

func assertRemoteBaselineSourceIdentity(t *testing.T, repository string, commit string, tree string) {
	t.Helper()
	if actual := remoteBaselineTestGitOutput(t, repository, "rev-parse", "HEAD"); actual != commit {
		t.Fatalf("HEAD = %q, want %q", actual, commit)
	}
	if actual := remoteBaselineTestGitOutput(t, repository, "rev-parse", "HEAD^{tree}"); actual != tree {
		t.Fatalf("tree = %q, want %q", actual, tree)
	}
}

func runRemoteBaselineTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func remoteBaselineTestGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output[:len(output)-1])
}
