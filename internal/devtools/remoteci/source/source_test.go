package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAndVerifyTreeDelta(t *testing.T) {
	repo, spec := newSourceFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("must not be exported\n"), 0o600); err != nil {
		t.Fatalf("write dirty worktree file: %v", err)
	}
	artifact, err := Build(context.Background(), repo, spec, t.TempDir())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	baseRoot := cloneSourceBase(t, repo, spec.BaseCommit)
	manifest, err := Verify(context.Background(), artifact.ManifestPath, artifact.PatchPath, baseRoot)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	assertVerifiedTrees(t, manifest, spec, baseRoot)
	assertMaterializedFixture(t, baseRoot)
}

// assertVerifiedTrees 断言清单树身份和物化仓库树保持一致。
func assertVerifiedTrees(t *testing.T, manifest Manifest, spec SourceSpec, root string) {
	t.Helper()
	if manifest.BaseTree != spec.BaseTree || manifest.TargetTree != spec.TargetTree {
		t.Fatalf("verified trees = %q -> %q, want %q -> %q",
			manifest.BaseTree, manifest.TargetTree, spec.BaseTree, spec.TargetTree)
	}
	if got := gitFixtureOutput(t, root, "rev-parse", "HEAD^{tree}"); got != spec.TargetTree {
		t.Fatalf("materialized tree = %q, want %q", got, spec.TargetTree)
	}
}

// assertMaterializedFixture 断言物化后的文件内容和元数据均来自目标树。
func assertMaterializedFixture(t *testing.T, root string) {
	t.Helper()
	assertMaterializedContent(t, root)
	assertMaterializedMetadata(t, root)
}

// assertMaterializedContent 断言文本和二进制文件的物化内容。
func assertMaterializedContent(t *testing.T, root string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "tracked.txt"))
	if err != nil {
		t.Fatalf("read materialized tracked file: %v", err)
	}
	if string(content) != "second committed version\n" {
		t.Fatalf("materialized tracked file = %q", content)
	}
	binary, err := os.ReadFile(filepath.Join(root, "binary.dat"))
	if err != nil {
		t.Fatalf("read materialized binary file: %v", err)
	}
	if string(binary) != "\x00\x01\xfftarget\x00" {
		t.Fatalf("materialized binary = %v", binary)
	}
}

// assertMaterializedMetadata 断言可执行位、符号链接和删除文件的状态。
func assertMaterializedMetadata(t *testing.T, root string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(root, "executable.sh"))
	if err != nil {
		t.Fatalf("stat executable: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("materialized executable bit is missing")
	}
	link, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil || link != "tracked.txt" {
		t.Fatalf("materialized symlink = %q, %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(root, "deleted.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file remains, stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dirty.txt")); !os.IsNotExist(err) {
		t.Fatalf("dirty worktree file was exported, stat error = %v", err)
	}
}

func TestVerifyRejectsTamperedPatch(t *testing.T) {
	repo, spec := newSourceFixture(t)
	artifact, err := Build(context.Background(), repo, spec, t.TempDir())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	file, err := os.OpenFile(artifact.PatchPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open patch for tampering: %v", err)
	}
	if _, err := file.WriteString("tampered"); err != nil {
		t.Fatalf("tamper patch: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close tampered patch: %v", err)
	}
	_, err = Verify(context.Background(), artifact.ManifestPath, artifact.PatchPath, cloneSourceBase(t, repo, spec.BaseCommit))
	if err == nil || !strings.Contains(err.Error(), "bytes do not match") {
		t.Fatalf("Verify() error = %v, want digest failure", err)
	}
}

func TestVerifyRejectsWrongBaseTree(t *testing.T) {
	repo, spec := newSourceFixture(t)
	artifact, err := Build(context.Background(), repo, spec, t.TempDir())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	root := cloneSourceBase(t, repo, spec.TargetCommit)
	_, err = Verify(context.Background(), artifact.ManifestPath, artifact.PatchPath, root)
	if err == nil || !strings.Contains(err.Error(), "image base tree") {
		t.Fatalf("Verify() error = %v, want base mismatch", err)
	}
}

func TestBuildRejectsWrongTargetTree(t *testing.T) {
	repo, spec := newSourceFixture(t)
	spec.TargetTree = strings.Repeat("0", len(spec.TargetTree))
	_, err := Build(context.Background(), repo, spec, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "tree does not match") {
		t.Fatalf("Build() error = %v, want tree mismatch", err)
	}
}

func TestVerifyRejectsManifestWrongTargetTree(t *testing.T) {
	repo, spec := newSourceFixture(t)
	artifact, err := Build(context.Background(), repo, spec, t.TempDir())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	artifact.Manifest.TargetTree = strings.Repeat("0", len(artifact.Manifest.TargetTree))
	encoded, err := json.Marshal(artifact.Manifest)
	if err != nil {
		t.Fatalf("encode changed manifest: %v", err)
	}
	if err := os.WriteFile(artifact.ManifestPath, encoded, 0o600); err != nil {
		t.Fatalf("write changed manifest: %v", err)
	}
	_, err = Verify(context.Background(), artifact.ManifestPath, artifact.PatchPath, cloneSourceBase(t, repo, spec.BaseCommit))
	if err == nil || !strings.Contains(err.Error(), "does not match target tree") {
		t.Fatalf("Verify() error = %v, want target mismatch", err)
	}
}

func newSourceFixture(t *testing.T) (string, SourceSpec) {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	runGit(t, repo, "config", "user.email", "source-test@example.invalid")
	runGit(t, repo, "config", "user.name", "Source Test")
	writeFixtureFile(t, repo, "tracked.txt", []byte("first committed version\n"), 0o600)
	writeFixtureFile(t, repo, "binary.dat", []byte("\x00base\xff"), 0o600)
	writeFixtureFile(t, repo, "deleted.txt", []byte("delete me\n"), 0o600)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "--quiet", "-m", "base")
	baseCommit := gitFixtureOutput(t, repo, "rev-parse", "HEAD")
	baseTree := gitFixtureOutput(t, repo, "rev-parse", "HEAD^{tree}")

	writeFixtureFile(t, repo, "tracked.txt", []byte("second committed version\n"), 0o600)
	writeFixtureFile(t, repo, "binary.dat", []byte("\x00\x01\xfftarget\x00"), 0o600)
	writeFixtureFile(t, repo, "executable.sh", []byte("#!/bin/sh\nexit 0\n"), 0o700)
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatalf("delete fixture file: %v", err)
	}
	if err := os.Symlink("tracked.txt", filepath.Join(repo, "current")); err != nil {
		t.Fatalf("create fixture symlink: %v", err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "--quiet", "-m", "target")
	targetCommit := gitFixtureOutput(t, repo, "rev-parse", "HEAD")
	targetTree := gitFixtureOutput(t, repo, "rev-parse", "HEAD^{tree}")
	return repo, SourceSpec{
		BaseCommit: baseCommit, BaseTree: baseTree,
		TargetCommit: targetCommit, TargetTree: targetTree,
	}
}

func cloneSourceBase(t *testing.T, repo string, commit string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "base")
	runGit(t, repo, "clone", "--quiet", "--no-hardlinks", repo, root)
	runGit(t, root, "checkout", "--quiet", "--detach", commit)
	return root
}

func writeFixtureFile(t *testing.T, root string, name string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), content, mode); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	if err := gitRun(context.Background(), repo, args...); err != nil {
		t.Fatalf("git %s: %v", args[0], err)
	}
}

func gitFixtureOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	output, err := gitOutput(context.Background(), repo, args...)
	if err != nil {
		t.Fatalf("git %s: %v", args[0], err)
	}
	return output
}
