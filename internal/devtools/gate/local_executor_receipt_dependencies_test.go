package gate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func localReceiptTestTrustedGit(t *testing.T) TrustedGitBinary {
	t.Helper()
	binary, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Fatalf("resolve trusted Git: %v", err)
	}
	return binary
}

func localReceiptTestTrustedGo(t *testing.T) TrustedGoBinary {
	t.Helper()
	binary, err := ResolveTrustedGoBinary(context.Background())
	if err != nil {
		t.Fatalf("resolve trusted Go: %v", err)
	}
	return binary
}

func TestVerifyGoModuleCacheOfflineRejectsCallerPathGo(t *testing.T) {
	trustedGit := localReceiptTestTrustedGit(t)
	fakeDirectory := t.TempDir()
	fakeGo := filepath.Join(fakeDirectory, "go")
	marker := filepath.Join(fakeDirectory, "caller-path-go-used")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\ntouch \"$FAKE_GO_MARKER\"\nprintf fake-go\\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_GO_MARKER", marker)
	t.Setenv("PATH", fakeDirectory)
	if _, _, err := verifyGoModuleCacheOffline(context.Background(), trustedGit, localReceiptTestTrustedGo(t), t.TempDir(), "0123456789012345678901234567890123456789", t.TempDir(), []byte("module example.com/receiptgo\n\ngo 1.26.5\n"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("offline verification executed caller PATH Go instead of a receipt-bound Go binary")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestLocalExecutorToolchainIgnoresCallerPathGo(t *testing.T) {
	trustedGo := localReceiptTestTrustedGo(t)
	boundPath, err := trustedGo.VerifiedPath()
	if err != nil {
		t.Fatal(err)
	}
	boundRoot, err := trustedGo.GoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fakeDirectory := t.TempDir()
	fakeGo := filepath.Join(fakeDirectory, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf caller-path-go\\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDirectory)
	goDirectory := assertReceiptToolchain(t, trustedGo, boundRoot, boundPath)
	assertReceiptSessionGoResolution(t, trustedGo, boundPath, goDirectory, fakeDirectory)
}

func assertReceiptToolchain(t *testing.T, trustedGo TrustedGoBinary, boundRoot, boundPath string) string {
	t.Helper()
	goRoot, goDirectory, err := localExecutorToolchain(trustedGo)
	if err != nil {
		t.Fatal(err)
	}
	if goRoot != boundRoot || goDirectory != filepath.Dir(boundPath) {
		t.Fatalf("toolchain = root=%q bin-dir=%q, want receipt root=%q bin-dir=%q", goRoot, goDirectory, boundRoot, filepath.Dir(boundPath))
	}
	return goDirectory
}

func assertReceiptSessionGoResolution(t *testing.T, trustedGo TrustedGoBinary, boundPath, goDirectory, fakeDirectory string) {
	t.Helper()
	goBinary, err := localExecutorGoBinary(trustedGo)
	if err != nil {
		t.Fatal(err)
	}
	if goBinary != boundPath {
		t.Fatalf("local executor Go binary = %q, want receipt-bound %q", goBinary, boundPath)
	}
	resolved, err := resolveExecutable("go", localExecutorSearchPathWithReceiptGo(goDirectory, fakeDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != boundPath {
		t.Fatalf("session Go resolution = %q, want receipt-bound %q", resolved, boundPath)
	}
}

func TestVerifyLocalReceiptGoDependencyMaterializesCurrentExactTreeLocalReplace(t *testing.T) {
	fixture := newLocalReceiptGoReplaceFixture(t, true, true)
	proof, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, fixture.tree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify local receipt Go dependency: %v", err)
	}
	if !localReceiptTestHasLockedPath(proof.lockFiles, "third_party/replaced/replaced.go") {
		t.Fatalf("local replacement source was not bound into the receipt proof: %#v", proof.lockFiles)
	}
}

func localReceiptTestTree(t *testing.T, root string) string {
	t.Helper()
	command := exec.Command("git", "rev-parse", "HEAD^{tree}")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("resolve test exact tree: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func localReceiptTestHasLockedPath(locks []localExecutorLockedFile, path string) bool {
	for _, lock := range locks {
		if lock.path == path {
			return true
		}
	}
	return false
}

func TestVerifyLocalReceiptGoDependencyIgnoresDirtyLocalReplacementWorktree(t *testing.T) {
	fixture := newLocalReceiptGoReplaceFixture(t, true, true)
	cleanProof, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, fixture.tree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify clean exact tree: %v", err)
	}
	writeLocalReceiptTestFile(t, filepath.Join(fixture.root, "third_party", "replaced", "replaced.go"), "package replaced\nconst Value = \"dirty\"\n")
	dirtyProof, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, fixture.tree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify exact tree with dirty replacement worktree: %v", err)
	}
	if cleanProof.lockDigest != dirtyProof.lockDigest {
		t.Fatalf("dirty worktree changed exact-tree proof: clean=%s dirty=%s", cleanProof.lockDigest, dirtyProof.lockDigest)
	}
}

func TestVerifyLocalReceiptGoDependencyRejectsIncompleteLocalReplacement(t *testing.T) {
	for name, fixtureShape := range map[string]struct {
		includeModule bool
		includeSource bool
	}{
		"missing go.mod": {includeModule: false, includeSource: true},
		"missing source": {includeModule: true, includeSource: false},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newLocalReceiptGoReplaceFixture(t, fixtureShape.includeModule, fixtureShape.includeSource)
			_, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, fixture.tree, fixture.cacheRoot)
			if err == nil || !strings.Contains(err.Error(), "must contain go.mod and source files") {
				t.Fatalf("verify incomplete local replacement error = %v, want go.mod/source rejection", err)
			}
		})
	}
}

func TestLocalReceiptGoReplacePathRejectsEscapeAndMalformedValues(t *testing.T) {
	for _, value := range []string{"", "/tmp/replaced", "../replaced", "./../replaced", "./", ".\\replaced"} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			if _, err := localReceiptGoReplacePath(value); err == nil {
				t.Fatalf("local replacement path %q unexpectedly accepted", value)
			}
		})
	}
}

func TestLocalReceiptGoReplacePathsRejectsDuplicateLocalMaterialization(t *testing.T) {
	mod := []byte("module example.com/root\n\ngo 1.26.5\n\nreplace example.com/one => ./third_party/replaced\nreplace example.com/two => ./third_party/replaced\n")
	if _, err := localReceiptGoReplacePaths(mod); err == nil {
		t.Fatal("duplicate local replacement target unexpectedly accepted")
	}
}

func TestVerifyLocalReceiptGoDependencyBindsExactLocalReplacementSource(t *testing.T) {
	fixture := newLocalReceiptGoReplaceFixture(t, true, true)
	first, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, fixture.tree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify first tree: %v", err)
	}
	writeLocalReceiptTestFile(t, filepath.Join(fixture.root, "third_party", "replaced", "replaced.go"), "package replaced\nconst Value = \"second\"\n")
	localReceiptTestGit(t, fixture.root, "add", "third_party/replaced/replaced.go")
	localReceiptTestGit(t, fixture.root, "commit", "-m", "change local replacement source")
	secondTree := localReceiptTestTree(t, fixture.root)
	second, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, secondTree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify second tree: %v", err)
	}
	if first.lockDigest == second.lockDigest {
		t.Fatalf("local replacement source drift did not change exact-tree lock proof: %s", first.lockDigest)
	}
}

func TestVerifyLocalReceiptGoDependencyIsStableAcrossRepositoryRoots(t *testing.T) {
	fixture := newLocalReceiptGoReplaceFixture(t, true, true)
	first, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), fixture.root, fixture.tree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify first repository root: %v", err)
	}
	cloneRoot := filepath.Join(t.TempDir(), "clone")
	localReceiptTestGit(t, "", "clone", "--no-hardlinks", fixture.root, cloneRoot)
	secondTree := localReceiptTestTree(t, cloneRoot)
	second, err := verifyLocalReceiptGoDependency(context.Background(), localReceiptTestTrustedGit(t), localReceiptTestTrustedGo(t), cloneRoot, secondTree, fixture.cacheRoot)
	if err != nil {
		t.Fatalf("verify cloned repository root: %v", err)
	}
	if first.lockDigest != second.lockDigest || first.verification != second.verification {
		t.Fatalf("same exact tree had root-dependent proof: first=%#v second=%#v", first, second)
	}
}

func TestLocalReceiptGoDependencyManifestRejectsMutableModuleCacheContent(t *testing.T) {
	fixture := newLocalReceiptGoCacheManifestFixture(t)
	for name, path := range fixture.paths {
		t.Run(name, func(t *testing.T) {
			writeLocalReceiptTestFile(t, filepath.Join(fixture.root, path), "drifted")
			if err := reverifyLocalReceiptDependency(fixture.root, fixture.proof); err == nil {
				t.Fatalf("reverify accepted mutated Go module cache %q", path)
			}
			writeLocalReceiptTestFile(t, filepath.Join(fixture.root, path), fixture.content[path])
		})
	}
}

func TestLocalReceiptGoDependencySnapshotIsPrivateAndPathIndependent(t *testing.T) {
	first := newLocalReceiptGoCacheManifestFixture(t)
	second := newLocalReceiptGoCacheManifestFixture(t)
	if first.proof.contentDigest != second.proof.contentDigest {
		t.Fatalf("same cache content produced root-dependent digest: %s != %s", first.proof.contentDigest, second.proof.contentDigest)
	}
	firstSnapshot, firstCleanup, err := materializeLocalReceiptDependencySnapshot(first.proof)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { requireNoLocalReceiptTestError(t, firstCleanup()) })
	secondSnapshot, secondCleanup, err := materializeLocalReceiptDependencySnapshot(second.proof)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { requireNoLocalReceiptTestError(t, secondCleanup()) })
	path := first.paths["module zip"]
	writeLocalReceiptTestFile(t, filepath.Join(first.root, path), "shared-cache-drift")
	for name, snapshot := range map[string]string{"first workload": firstSnapshot, "second workload": secondSnapshot} {
		content, err := os.ReadFile(filepath.Join(snapshot, path))
		if err != nil || string(content) != first.content[path] {
			t.Fatalf("%s snapshot was polluted: content=%q err=%v", name, content, err)
		}
	}
	if err := reverifyLocalReceiptDependency(first.root, first.proof); err == nil {
		t.Fatal("reverify accepted mutable source cache after private snapshots were created")
	}
}

func TestLocalReceiptEmbedDependencySealsManifestAndPrivateSnapshot(t *testing.T) {
	firstRoot, first := newLocalReceiptEmbedProof(t)
	_, second := newLocalReceiptEmbedProof(t)
	requireLocalReceiptEmbedPathIndependent(t, first, second)
	requireLocalReceiptEmbedRejectsExtra(t, firstRoot, first)
	requireLocalReceiptEmbedPrivateSnapshot(t, firstRoot, first)
}

func newLocalReceiptEmbedProof(t *testing.T) (string, localExecutorDependencyProof) {
	t.Helper()
	root := t.TempDir()
	writeLocalReceiptTestFile(t, filepath.Join(root, "index.html"), "embed")
	writeLocalReceiptTestFile(t, filepath.Join(root, "assets", "main.js"), "console.log('embed')")
	proof, err := verifyLocalReceiptEmbedDependency(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, proof
}

func requireLocalReceiptEmbedPathIndependent(t *testing.T, first, second localExecutorDependencyProof) {
	t.Helper()
	if len(first.contentFiles) != 2 || first.contentDigest != second.contentDigest {
		t.Fatalf("embed manifest is incomplete or root-dependent: first=%#v second=%#v", first, second)
	}
}

func requireLocalReceiptEmbedRejectsExtra(t *testing.T, root string, proof localExecutorDependencyProof) {
	t.Helper()
	writeLocalReceiptTestFile(t, filepath.Join(root, "extra.js"), "extra")
	if err := reverifyLocalReceiptDependency(root, proof); err == nil {
		t.Fatal("reverify accepted extra frontend embed source file")
	}
	if err := os.Remove(filepath.Join(root, "extra.js")); err != nil {
		t.Fatal(err)
	}
}

func requireLocalReceiptEmbedPrivateSnapshot(t *testing.T, root string, proof localExecutorDependencyProof) {
	t.Helper()
	snapshot, cleanup, err := materializeLocalReceiptDependencySnapshot(proof)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { requireNoLocalReceiptTestError(t, cleanup()) })
	writeLocalReceiptTestFile(t, filepath.Join(root, "assets", "main.js"), "drift")
	if err := reverifyLocalReceiptDependency(root, proof); err == nil {
		t.Fatal("reverify accepted mutated frontend embed source")
	}
	content, err := os.ReadFile(filepath.Join(snapshot, "assets", "main.js"))
	if err != nil || string(content) != "console.log('embed')" {
		t.Fatalf("private embed snapshot drifted: content=%q err=%v", content, err)
	}
	info, err := os.Stat(filepath.Join(snapshot, "assets", "main.js"))
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("private embed snapshot is not read-only: mode=%#o err=%v", info.Mode().Perm(), err)
	}
}

func TestLocalReceiptDependencySnapshotCanonicalizesTemporaryRootAlias(t *testing.T) {
	realRoot := t.TempDir()
	aliasRoot := filepath.Join(t.TempDir(), "tmp-alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", aliasRoot)
	fixture := newLocalReceiptGoCacheManifestFixture(t)
	snapshot, cleanup, err := materializeLocalReceiptDependencySnapshot(fixture.proof)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { requireNoLocalReceiptTestError(t, cleanup()) })
	canonical, err := filepath.EvalSymlinks(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot != canonical {
		t.Fatalf("dependency snapshot = %q, want canonical %q", snapshot, canonical)
	}
}

type localReceiptGoCacheManifestFixture struct {
	root    string
	paths   map[string]string
	content map[string]string
	proof   localExecutorDependencyProof
}

func newLocalReceiptGoCacheManifestFixture(t *testing.T) localReceiptGoCacheManifestFixture {
	t.Helper()
	root := t.TempDir()
	paths := map[string]string{
		"module zip":       "cache/download/example.com/mod/@v/v1.0.0.zip",
		"module mod":       "cache/download/example.com/mod/@v/v1.0.0.mod",
		"module info":      "cache/download/example.com/mod/@v/v1.0.0.info",
		"extracted source": "pkg/mod/example.com/mod@v1.0.0/mod.go",
	}
	content := map[string]string{}
	for name, path := range paths {
		content[path] = name + " content"
		writeLocalReceiptTestFile(t, filepath.Join(root, path), content[path])
	}
	files, digest, err := localReceiptDependencyContentManifest(root, localReceiptAbsolutePaths(root, paths))
	if err != nil {
		t.Fatal(err)
	}
	return localReceiptGoCacheManifestFixture{root: root, paths: paths, content: content, proof: localExecutorDependencyProof{name: "go", root: root, contentDigest: digest, contentFiles: files}}
}

func localReceiptAbsolutePaths(root string, values map[string]string) []string {
	paths := make([]string, 0, len(values))
	for _, path := range values {
		paths = append(paths, filepath.Join(root, path))
	}
	return paths
}

func requireNoLocalReceiptTestError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type localReceiptGoReplaceFixture struct {
	root      string
	tree      string
	cacheRoot string
}

func newLocalReceiptGoReplaceFixture(t *testing.T, includeModule, includeSource bool) localReceiptGoReplaceFixture {
	t.Helper()
	root := t.TempDir()
	writeLocalReceiptTestFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.26.5\n\nrequire example.com/replaced v0.0.0\n\nreplace example.com/replaced => ./third_party/replaced\n")
	writeLocalReceiptTestFile(t, filepath.Join(root, "go.sum"), "")
	if includeModule {
		writeLocalReceiptTestFile(t, filepath.Join(root, "third_party", "replaced", "go.mod"), "module example.com/replaced\n\ngo 1.26.5\n")
	}
	if includeSource {
		writeLocalReceiptTestFile(t, filepath.Join(root, "third_party", "replaced", "replaced.go"), "package replaced\nconst Value = \"first\"\n")
	}
	localReceiptTestGit(t, root, "init")
	localReceiptTestGit(t, root, "add", ".")
	localReceiptTestGit(t, root, "-c", "user.name=Local Receipt Test", "-c", "user.email=local-receipt@example.test", "commit", "-m", "create local replacement fixture")
	return localReceiptGoReplaceFixture{root: root, tree: localReceiptTestTree(t, root), cacheRoot: t.TempDir()}
}

func writeLocalReceiptTestFile(t *testing.T, filePath, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}

func localReceiptTestGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
}
