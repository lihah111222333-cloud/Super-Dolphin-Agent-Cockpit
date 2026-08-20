package remoteci

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestBuildPortableBuilderInputBundleProducesExactSelfContainedBundle 验证 bundle 完整性及源仓库不变。
func TestBuildPortableBuilderInputBundleProducesExactSelfContainedBundle(t *testing.T) {
	fixture := newPortableBuilderInputBundleFixture(t)
	beforeRefs := fixtureGitOutput(t, fixture.repository, "for-each-ref", "--format=%(objectname) %(refname)")
	beforeHead := fixtureGitOutput(t, fixture.repository, "symbolic-ref", "-q", "HEAD")
	beforeIndex := readFile(t, filepath.Join(fixture.repository, ".git", "index"))
	beforeTracked := readFile(t, filepath.Join(fixture.repository, "tracked.txt"))

	outputPath := filepath.Join(t.TempDir(), "input.bundle")
	metadata, err := BuildPortableBuilderInputBundle(context.Background(), fixture.request, outputPath)
	if err != nil {
		t.Fatalf("BuildPortableBuilderInputBundle() error = %v", err)
	}
	assertPortableBuilderInputBundleOutput(t, outputPath, metadata, fixture.request)
	assertPortableBuilderInputBundleSourceUnchanged(t, fixture.repository, beforeRefs, beforeHead, beforeIndex, beforeTracked)
}

func assertPortableBuilderInputBundleOutput(t *testing.T, outputPath string, metadata PortableBuilderInputBundleMetadata, request PortableBuilderInputBundleRequest) {
	t.Helper()
	bundle := readFile(t, outputPath)
	if metadata.SHA256 != bytesDigest(bundle) || metadata.SizeBytes != int64(len(bundle)) {
		t.Fatalf("metadata = %#v, want digest/size of output", metadata)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat output bundle: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %04o, want 0600", info.Mode().Perm())
	}
	if err := readSourceBaselineBuilderHeader(outputPath, request.ObjectFormat, PortableBuilderInputBundleRef, request.CommitSHA); err != nil {
		t.Fatalf("read exact bundle header: %v", err)
	}
	assertPortableBuilderInputBundleImports(t, outputPath, request)
}

func assertPortableBuilderInputBundleSourceUnchanged(t *testing.T, repository, beforeRefs, beforeHead string, beforeIndex, beforeTracked []byte) {
	t.Helper()
	if got := fixtureGitOutput(t, repository, "for-each-ref", "--format=%(objectname) %(refname)"); got != beforeRefs {
		t.Fatalf("source refs changed from %q to %q", beforeRefs, got)
	}
	if got := fixtureGitOutput(t, repository, "symbolic-ref", "-q", "HEAD"); got != beforeHead {
		t.Fatalf("source HEAD changed from %q to %q", beforeHead, got)
	}
	if got := readFile(t, filepath.Join(repository, ".git", "index")); !bytes.Equal(got, beforeIndex) {
		t.Fatal("source index changed")
	}
	if got := readFile(t, filepath.Join(repository, "tracked.txt")); !bytes.Equal(got, beforeTracked) {
		t.Fatal("source worktree changed")
	}
}

func TestBuildPortableBuilderInputBundleHasStableBytesAndWriterParity(t *testing.T) {
	fixture := newPortableBuilderInputBundleFixture(t)
	firstPath := filepath.Join(t.TempDir(), "first.bundle")
	first, err := BuildPortableBuilderInputBundle(context.Background(), fixture.request, firstPath)
	if err != nil {
		t.Fatalf("first bundle = %v", err)
	}
	var written bytes.Buffer
	second, err := WritePortableBuilderInputBundle(context.Background(), fixture.request, &written)
	if err != nil {
		t.Fatalf("writer bundle = %v", err)
	}
	if first != second {
		t.Fatalf("writer metadata = %#v, path metadata = %#v", second, first)
	}
	if !bytes.Equal(readFile(t, firstPath), written.Bytes()) {
		t.Fatal("writer bytes differ from create-only output bytes")
	}

	secondPath := filepath.Join(t.TempDir(), "second.bundle")
	third, err := BuildPortableBuilderInputBundle(context.Background(), fixture.request, secondPath)
	if err != nil {
		t.Fatalf("second path bundle = %v", err)
	}
	if first != third || !bytes.Equal(readFile(t, firstPath), readFile(t, secondPath)) {
		t.Fatalf("repeated bundle identity drifted: first=%#v second=%#v", first, third)
	}
}

func TestPortableBuilderInputBundleRejectsImplicitInputsAndOutputOverwrite(t *testing.T) {
	fixture := newPortableBuilderInputBundleFixture(t)
	cases := map[string]func(*PortableBuilderInputBundleRequest){
		"noncanonical root": func(request *PortableBuilderInputBundleRequest) {
			request.RepositoryRoot += string(filepath.Separator) + "."
		},
		"HEAD alias": func(request *PortableBuilderInputBundleRequest) { request.CommitSHA = "HEAD" },
		"wrong tree": func(request *PortableBuilderInputBundleRequest) { request.TreeSHA = strings.Repeat("f", 40) },
		"wrong object format": func(request *PortableBuilderInputBundleRequest) {
			request.ObjectFormat = gate.GitObjectFormatSHA256
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := fixture.request
			mutate(&request)
			outputPath := filepath.Join(t.TempDir(), "rejected.bundle")
			if _, err := BuildPortableBuilderInputBundle(context.Background(), request, outputPath); err == nil {
				t.Fatal("BuildPortableBuilderInputBundle() accepted an implicit or mismatched input")
			}
			if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
				t.Fatalf("rejected build created output: %v", err)
			}
		})
	}

	outputPath := filepath.Join(t.TempDir(), "existing.bundle")
	original := []byte("keep-existing-bytes")
	if err := os.WriteFile(outputPath, original, 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	if _, err := BuildPortableBuilderInputBundle(context.Background(), fixture.request, outputPath); err == nil {
		t.Fatal("BuildPortableBuilderInputBundle() overwrote an existing output")
	}
	if got := readFile(t, outputPath); !bytes.Equal(got, original) {
		t.Fatal("existing output was modified")
	}
}

func TestPortableBuilderInputBundleRejectsCancellationAndTypedNilWriter(t *testing.T) {
	fixture := newPortableBuilderInputBundleFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildPortableBuilderInputBundle(ctx, fixture.request, filepath.Join(t.TempDir(), "cancelled.bundle")); err == nil {
		t.Fatal("cancelled bundle build unexpectedly succeeded")
	}
	var destination *bytes.Buffer
	if _, err := WritePortableBuilderInputBundle(context.Background(), fixture.request, destination); err == nil {
		t.Fatal("typed nil writer unexpectedly succeeded")
	}
}

func TestPortableBuilderInputBundleFieldGuard(t *testing.T) {
	typeFieldNames := reflect.TypeFor[PortableBuilderInputBundleRequest]()
	covered := map[string]struct{}{
		"repository_root": {},
		"commit_sha":      {},
		"tree_sha":        {},
		"object_format":   {},
	}
	seen := make(map[string]struct{}, typeFieldNames.NumField())
	for field := range typeFieldNames.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("request field %s has no guard-visible JSON name", field.Name)
		}
		seen[name] = struct{}{}
		if _, ok := covered[name]; !ok {
			t.Fatalf("request field %s is missing consumer coverage", name)
		}
	}
	for name := range covered {
		if _, ok := seen[name]; !ok {
			t.Fatalf("field guard has stale consumer coverage %q", name)
		}
	}
}

type portableBuilderInputBundleFixture struct {
	repository string
	request    PortableBuilderInputBundleRequest
}

func newPortableBuilderInputBundleFixture(t *testing.T) portableBuilderInputBundleFixture {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "source")
	runFixtureGit(t, root, "init", "-q", "--object-format=sha1", "--", repository)
	runFixtureGit(t, repository, "config", "user.name", "portable-builder-fixture")
	runFixtureGit(t, repository, "config", "user.email", "portable-builder-fixture@example.invalid")
	writeFile(t, filepath.Join(repository, "tracked.txt"), []byte("first\n"), 0o644)
	runFixtureGit(t, repository, "add", "--", "tracked.txt")
	runFixtureGit(t, repository, "commit", "-qm", "first")
	writeFile(t, filepath.Join(repository, "tracked.txt"), []byte("second\n"), 0o644)
	writeFile(t, filepath.Join(repository, "second.txt"), []byte("second\n"), 0o644)
	runFixtureGit(t, repository, "add", "--", "tracked.txt", "second.txt")
	runFixtureGit(t, repository, "commit", "-qm", "second")
	commit := strings.TrimSpace(fixtureGitOutput(t, repository, "rev-parse", "HEAD"))
	tree := strings.TrimSpace(fixtureGitOutput(t, repository, "rev-parse", "HEAD^{tree}"))
	return portableBuilderInputBundleFixture{
		repository: repository,
		request: PortableBuilderInputBundleRequest{
			RepositoryRoot: repository,
			CommitSHA:      commit,
			TreeSHA:        tree,
			ObjectFormat:   gate.GitObjectFormatSHA1,
		},
	}
}

func assertPortableBuilderInputBundleImports(t *testing.T, bundlePath string, request PortableBuilderInputBundleRequest) {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "verify.git")
	runFixtureGit(t, root, "init", "-q", "--bare", "--object-format="+string(request.ObjectFormat), "--", repository)
	verifyOutput := fixtureGitOutput(t, repository, "bundle", "verify", "--", bundlePath)
	if !strings.Contains(verifyOutput, "complete history") {
		t.Fatalf("bundle verify output does not prove zero prerequisites: %q", verifyOutput)
	}
	runFixtureGit(t, repository, "bundle", "unbundle", "--", bundlePath)
	runFixtureGitWithInput(t, repository, fmt.Sprintf("create %s %s\n", PortableBuilderInputBundleRef, request.CommitSHA), "update-ref", "--stdin")
	if got, want := fixtureGitOutput(t, repository, "for-each-ref", "--format=%(objectname) %(refname)"), request.CommitSHA+" "+PortableBuilderInputBundleRef+"\n"; got != want {
		t.Fatalf("imported refs = %q, want %q", got, want)
	}
	if got := strings.TrimSpace(fixtureGitOutput(t, repository, "rev-parse", "--verify", "--end-of-options", request.CommitSHA+"^{tree}")); got != request.TreeSHA {
		t.Fatalf("imported tree = %q, want %q", got, request.TreeSHA)
	}
	if output := fixtureGitOutput(t, repository, "fsck", "--full", "--strict", "--no-reflogs", "--unreachable", "--no-progress", PortableBuilderInputBundleRef); strings.TrimSpace(output) != "" {
		t.Fatalf("imported bundle has unreachable objects: %q", output)
	}
}

func runFixtureGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	if err := runGitDiscardForTest(repository, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func runFixtureGitWithInput(t *testing.T, repository, input string, args ...string) {
	t.Helper()
	if err := runGitForTest(repository, strings.NewReader(input), args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func fixtureGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	output, err := runGitOutput(context.Background(), repository, nil, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

func runGitDiscardForTest(repository string, args ...string) error {
	return runGitForTest(repository, nil, args...)
}

func runGitForTest(repository string, input io.Reader, args ...string) error {
	return runGit(context.Background(), repository, input, io.Discard, args...)
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
