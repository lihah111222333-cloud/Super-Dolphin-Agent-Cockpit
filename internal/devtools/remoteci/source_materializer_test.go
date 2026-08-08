package remoteci

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type sourceMaterializerFixture struct {
	repository           string
	baseline             SourceBaseline
	baselineSourceCommit string
	candidateCommit      string
	candidateTree        string
}

func TestSourceMaterializationRootsOmitsOnlyDeterministicEmptyTree(t *testing.T) {
	emptyTree, err := DeterministicSourceEmptyTreeSHA(gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceEmptyTreeSHA() error = %v", err)
	}
	tests := []struct {
		name     string
		baseTree string
		want     []string
	}{
		{name: "empty parent", baseTree: emptyTree, want: []string{"candidate-tree"}},
		{name: "non-empty parent", baseTree: "parent-tree", want: []string{"candidate-tree", "parent-tree"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sourceMaterializationRoots("candidate-tree", test.baseTree, gate.GitObjectFormatSHA1)
			if err != nil {
				t.Fatalf("sourceMaterializationRoots() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("sourceMaterializationRoots() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMaterializeVerifiedSourceBundleRoundTrip(t *testing.T) {
	fixture := newSourceMaterializerFixture(t, "candidate.txt", "candidate\n", "seed", "candidate.txt", "candidate changed\n", "candidate", true)
	artifacts, _ := materializeSourceFixture(t, fixture, commitSourceSpec(fixture))
	sourceRoot := canonicalMaterializerTempDir(t)
	manifest, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot, fixture.baseline)
	if err != nil {
		t.Fatalf("MaterializeVerifiedSourceBundle() error = %v", err)
	}
	assertRoundTripMaterialization(t, sourceRoot, manifest, fixture)
}

func TestMaterializeVerifiedSourceBundleRejectsBundleDigestDrift(t *testing.T) {
	fixture := newSourceMaterializerFixture(t, "candidate.txt", "candidate\n", "seed", "candidate.txt", "candidate changed\n", "candidate", true)
	artifacts, materialization := materializeSourceFixture(t, fixture, commitSourceSpec(fixture))
	appendMaterializerBundleDrift(t, materialization.BundlePath)
	sourceRoot := canonicalMaterializerTempDir(t)
	if _, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot, fixture.baseline); err == nil {
		t.Fatal("MaterializeVerifiedSourceBundle() accepted bundle digest drift")
	}
	assertEmptyMaterializerDirectory(t, sourceRoot)
}

func TestMaterializeVerifiedSourceBundleRejectsSyntheticBaseManifestTamper(t *testing.T) {
	fixture := newSourceMaterializerFixture(t, "candidate.txt", "candidate\n", "seed", "candidate.txt", "candidate changed\n", "candidate", true)
	artifacts, materialization := materializeSourceFixture(t, fixture, commitSourceSpec(fixture))
	manifestData, err := os.ReadFile(materialization.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestData = bytes.Replace(manifestData, []byte(materialization.Manifest.SyntheticBaseCommitSHA), []byte(strings.Repeat("0", 40)), 1)
	if err := os.Chmod(materialization.ManifestPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(materialization.ManifestPath, manifestData, 0o400); err != nil {
		t.Fatal(err)
	}
	sourceRoot := canonicalMaterializerTempDir(t)
	if _, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot, fixture.baseline); err == nil {
		t.Fatal("MaterializeVerifiedSourceBundle() accepted synthetic base manifest tamper")
	}
	assertEmptyMaterializerDirectory(t, sourceRoot)
}

func TestMaterializeVerifiedSourceBundleUsesSourceParentTreeForCRLFWhitespaceRange(t *testing.T) {
	fixture := newCRLFParentMaterializerFixture(t)
	spec := gate.SourceSpec{Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1, Commit: &gate.CommitSource{SHA: fixture.candidateCommit}, SourceTreeSHA: fixture.candidateTree}
	artifacts := canonicalMaterializerTempDir(t)
	materialization, err := MaterializeSource(context.Background(), fixture.repository, spec, artifacts, fixture.baseline)
	if err != nil {
		t.Fatalf("MaterializeSource() error = %v", err)
	}
	sourceRoot := canonicalMaterializerTempDir(t)
	manifest, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot, fixture.baseline)
	if err != nil {
		t.Fatalf("MaterializeVerifiedSourceBundle() error = %v", err)
	}
	assertCRLFParentMaterialization(t, sourceRoot, fixture, materialization, manifest)
}

type crlfParentMaterializerFixture struct {
	repository      string
	baseline        SourceBaseline
	parentCommit    string
	parentTree      string
	candidateCommit string
	candidateTree   string
}

func newCRLFParentMaterializerFixture(t *testing.T) crlfParentMaterializerFixture {
	t.Helper()
	repository := canonicalMaterializerTempDir(t)
	setupMaterializerRepository(t, repository)
	writeMaterializerFile(t, repository, "legacy.txt", "line\n")
	runMaterializerGit(t, repository, "add", "legacy.txt")
	runMaterializerGit(t, repository, "commit", "--quiet", "-m", "accepted source")
	baselineTree := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}"))
	baselineRoot := canonicalMaterializerTempDir(t)
	baseline, err := BuildSourceBaseline(context.Background(), repository, baselineTree, baselineRoot, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("BuildSourceBaseline() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "legacy.txt"), []byte("line\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runMaterializerGit(t, repository, "add", "legacy.txt")
	runMaterializerGit(t, repository, "commit", "--quiet", "-m", "legacy CRLF parent")
	parentCommit := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD"))
	parentTree := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}"))
	writeMaterializerFile(t, repository, "added.go", "package added\n")
	runMaterializerGit(t, repository, "add", "added.go")
	runMaterializerGit(t, repository, "commit", "--quiet", "-m", "candidate")
	candidateCommit := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD"))
	candidateTree := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}"))
	return crlfParentMaterializerFixture{
		repository:      repository,
		baseline:        baseline,
		parentCommit:    parentCommit,
		parentTree:      parentTree,
		candidateCommit: candidateCommit,
		candidateTree:   candidateTree,
	}
}

func assertCRLFParentMaterialization(t *testing.T, sourceRoot string, fixture crlfParentMaterializerFixture, materialization SourceMaterialization, manifest SourceMaterializationManifest) {
	t.Helper()
	wantSynthetic, err := DeterministicSourceSyntheticBaseCommitSHA(fixture.parentTree, fixture.baseline.CommitSHA, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceSyntheticBaseCommitSHA() error = %v", err)
	}
	if manifest.TrustedBaseCommitSHA != fixture.parentCommit || manifest.SyntheticBaseTreeSHA != fixture.parentTree || manifest.SyntheticBaseCommitSHA != wantSynthetic {
		t.Fatalf("manifest source parent identity = %#v", manifest)
	}
	assertMaterializerBaseRef(t, sourceRoot, wantSynthetic)
	command := exec.Command("git", "-C", sourceRoot, "diff", "--check", "refs/source/base", "HEAD", "--")
	output, err := command.CombinedOutput()
	if err != nil || len(output) != 0 {
		t.Fatalf("synthetic base diff --check = err=%v output=%q", err, output)
	}
	if materialization.Manifest.SyntheticBaseCommitSHA != manifest.SyntheticBaseCommitSHA {
		t.Fatalf("materialization/worker synthetic base mismatch: %s vs %s", materialization.Manifest.SyntheticBaseCommitSHA, manifest.SyntheticBaseCommitSHA)
	}
}

func TestSourceTransportCommitCoversCommitRangeTreeAndUnrelatedHistory(t *testing.T) {
	fixture := newSourceMaterializerFixture(t, "baseline.txt", "accepted\n", "accepted source", "candidate.txt", "candidate\n", "candidate", false)
	wantSynthetic, err := DeterministicSourceSyntheticBaseCommitSHA(fixture.baseline.TreeSHA, fixture.baseline.CommitSHA, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceSyntheticBaseCommitSHA() error = %v", err)
	}
	wantTransport, err := DeterministicSourceTransportCommitSHA(fixture.candidateTree, wantSynthetic, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceTransportCommitSHA() error = %v", err)
	}
	for _, spec := range transportSourceSpecs(fixture) {
		t.Run(string(spec.Kind), func(t *testing.T) {
			assertTransportMaterialization(t, fixture, spec, wantTransport)
		})
	}
}

func newSourceMaterializerFixture(t *testing.T, baselineFile string, baselineContent string, baselineMessage string, candidateFile string, candidateContent string, candidateMessage string, useBaselineCommit bool) sourceMaterializerFixture {
	t.Helper()
	repository := canonicalMaterializerTempDir(t)
	setupMaterializerRepository(t, repository)
	writeMaterializerFile(t, repository, baselineFile, baselineContent)
	runMaterializerGit(t, repository, "add", baselineFile)
	runMaterializerGit(t, repository, "commit", "--quiet", "-m", baselineMessage)
	baselineSourceCommit := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD"))
	baselineTree := strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}"))
	baselineRoot := canonicalMaterializerTempDir(t)
	baseline, err := BuildSourceBaseline(context.Background(), repository, baselineTree, baselineRoot, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("BuildSourceBaseline() error = %v", err)
	}
	writeMaterializerFile(t, repository, candidateFile, candidateContent)
	runMaterializerGit(t, repository, "add", candidateFile)
	if useBaselineCommit {
		createMaterializerCommit(t, repository, candidateMessage, baseline.CommitSHA, baseline.RepositoryRoot)
	} else {
		runMaterializerGit(t, repository, "commit", "--quiet", "-m", candidateMessage)
	}
	return sourceMaterializerFixture{
		repository: repository, baseline: baseline, baselineSourceCommit: baselineSourceCommit,
		candidateCommit: strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD")),
		candidateTree:   strings.TrimSpace(runMaterializerGit(t, repository, "rev-parse", "HEAD^{tree}")),
	}
}

func setupMaterializerRepository(t *testing.T, repository string) {
	t.Helper()
	runMaterializerGit(t, repository, "init", "--quiet", "--initial-branch=main")
	runMaterializerGit(t, repository, "config", "user.email", "source-materializer@example.invalid")
	runMaterializerGit(t, repository, "config", "user.name", "Source Materializer")
}

func writeMaterializerFile(t *testing.T, repository string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitSourceSpec(fixture sourceMaterializerFixture) gate.SourceSpec {
	return gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: fixture.candidateCommit}, SourceTreeSHA: fixture.candidateTree,
	}
}

func materializeSourceFixture(t *testing.T, fixture sourceMaterializerFixture, spec gate.SourceSpec) (string, SourceMaterialization) {
	t.Helper()
	artifacts := canonicalMaterializerTempDir(t)
	materialization, err := MaterializeSource(context.Background(), fixture.repository, spec, artifacts, fixture.baseline)
	if err != nil {
		t.Fatalf("MaterializeSource() error = %v", err)
	}
	return artifacts, materialization
}

func assertRoundTripMaterialization(t *testing.T, sourceRoot string, manifest SourceMaterializationManifest, fixture sourceMaterializerFixture) {
	t.Helper()
	wantSynthetic, err := DeterministicSourceSyntheticBaseCommitSHA(fixture.baseline.TreeSHA, fixture.baseline.CommitSHA, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceSyntheticBaseCommitSHA() error = %v", err)
	}
	wantTransport, err := DeterministicSourceTransportCommitSHA(fixture.candidateTree, wantSynthetic, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceTransportCommitSHA() error = %v", err)
	}
	if manifest.SourceTreeSHA != fixture.candidateTree || manifest.Source.Commit.SHA != fixture.candidateCommit || manifest.SyntheticBaseTreeSHA != fixture.baseline.TreeSHA || manifest.SyntheticBaseCommitSHA != wantSynthetic || manifest.TransportCommitSHA != wantTransport {
		t.Fatalf("materialized manifest = %#v", manifest)
	}
	content, err := os.ReadFile(filepath.Join(sourceRoot, "candidate.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "candidate changed\n" {
		t.Fatalf("materialized source = %q", content)
	}
	assertMaterializerRepository(t, sourceRoot, fixture.candidateTree, wantTransport)
	assertMaterializerBaseRef(t, sourceRoot, wantSynthetic)
}

func assertMaterializerBaseRef(t *testing.T, sourceRoot string, want string) {
	t.Helper()
	if got := strings.TrimSpace(runMaterializerGit(t, sourceRoot, "rev-parse", "--verify", "refs/source/base^{commit}")); got != want {
		t.Fatalf("materialized source base ref = %s, want %s", got, want)
	}
}

func assertMaterializerRepository(t *testing.T, sourceRoot string, wantTree string, wantHead string) {
	t.Helper()
	if got := strings.TrimSpace(runMaterializerGit(t, sourceRoot, "rev-parse", "HEAD^{tree}")); got != wantTree {
		t.Fatalf("materialized tree = %s, want %s", got, wantTree)
	}
	if status := strings.TrimSpace(runMaterializerGit(t, sourceRoot, "status", "--porcelain=v1", "--untracked-files=all")); status != "" {
		t.Fatalf("materialized source is dirty: %q", status)
	}
	if wantHead != "" {
		if got := strings.TrimSpace(runMaterializerGit(t, sourceRoot, "rev-parse", "HEAD")); got != wantHead {
			t.Fatalf("materialized HEAD = %s, want %s", got, wantHead)
		}
	}
}

func appendMaterializerBundleDrift(t *testing.T, bundlePath string) {
	t.Helper()
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
}

func assertEmptyMaterializerDirectory(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed source handoff left entries: %v", entries)
	}
}

func transportSourceSpecs(fixture sourceMaterializerFixture) []gate.SourceSpec {
	return []gate.SourceSpec{
		commitSourceSpec(fixture),
		{Kind: gate.SourceKindRange, ObjectFormat: gate.GitObjectFormatSHA1, Range: &gate.RangeSource{
			BaseKind: gate.BaseKindCommit, BaseSHA: fixture.baselineSourceCommit, HeadSHA: fixture.candidateCommit,
			LocalRef: "refs/heads/main", RemoteRef: "refs/heads/main", ObservedRemoteSHA: fixture.baselineSourceCommit,
			UpdateKind: gate.UpdateKindFastForward,
		}, SourceTreeSHA: fixture.candidateTree},
		{Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1, Tree: &gate.TreeSource{
			SHA: fixture.candidateTree, ParentCommitSHA: fixture.baselineSourceCommit,
		}, SourceTreeSHA: fixture.candidateTree},
	}
}

func assertTransportMaterialization(t *testing.T, fixture sourceMaterializerFixture, spec gate.SourceSpec, wantTransport string) {
	t.Helper()
	artifacts, materialization := materializeSourceFixture(t, fixture, spec)
	manifest := materialization.Manifest
	wantSynthetic, err := DeterministicSourceSyntheticBaseCommitSHA(fixture.baseline.TreeSHA, fixture.baseline.CommitSHA, gate.GitObjectFormatSHA1)
	if err != nil {
		t.Fatalf("DeterministicSourceSyntheticBaseCommitSHA() error = %v", err)
	}
	if manifest.TransportCommitSHA != wantTransport || manifest.SourceTreeSHA != fixture.candidateTree || manifest.Source.SourceTreeSHA != fixture.candidateTree || manifest.SyntheticBaseTreeSHA != fixture.baseline.TreeSHA || manifest.SyntheticBaseCommitSHA != wantSynthetic {
		t.Fatalf("transport manifest = %#v", manifest)
	}
	assertSingleTransportPrerequisite(t, materialization.BundlePath, manifest, fixture.baseline)
	sourceRoot := canonicalMaterializerTempDir(t)
	if _, err := MaterializeVerifiedSourceBundle(context.Background(), artifacts, sourceRoot, fixture.baseline); err != nil {
		t.Fatalf("MaterializeVerifiedSourceBundle() error = %v", err)
	}
	assertMaterializerRepository(t, sourceRoot, fixture.candidateTree, wantTransport)
	assertMaterializerBaseRef(t, sourceRoot, wantSynthetic)
	if runMaterializerGitExpectFailure(t, sourceRoot, "cat-file", "-e", fixture.baselineSourceCommit+"^{commit}") == nil {
		t.Fatalf("unrelated original source history %s was included in transport", fixture.baselineSourceCommit)
	}
}

type materializerTestBundleHeader struct {
	prerequisites []string
	refs          []string
}

func assertSingleTransportPrerequisite(t *testing.T, bundlePath string, manifest SourceMaterializationManifest, baseline SourceBaseline) {
	t.Helper()
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	header, err := parseMaterializerBundleHeader(data)
	if err != nil {
		t.Fatal(err)
	}
	wantRef := strings.TrimSuffix(expectedBundleRefs(manifest), "\n")
	if len(header.prerequisites) != 1 || header.prerequisites[0] != baseline.CommitSHA {
		t.Fatalf("bundle prerequisites = %v, want [%s]", header.prerequisites, baseline.CommitSHA)
	}
	if len(header.refs) != 1 || header.refs[0] != wantRef {
		t.Fatalf("bundle refs = %v, want [%s]", header.refs, wantRef)
	}
	if err := verifyBundlePrerequisites(bundlePath, manifest, baseline); err != nil {
		t.Fatalf("verifyBundlePrerequisites() error = %v", err)
	}
}

func parseMaterializerBundleHeader(data []byte) (materializerTestBundleHeader, error) {
	headerBytes, _, found := bytes.Cut(data, []byte("\n\n"))
	if !found {
		return materializerTestBundleHeader{}, fmt.Errorf("source bundle is missing header terminator")
	}
	lines := strings.Split(string(headerBytes), "\n")
	if len(lines) < 2 || lines[0] != "# v2 git bundle" {
		return materializerTestBundleHeader{}, fmt.Errorf("source bundle header = %q", string(headerBytes))
	}
	var header materializerTestBundleHeader
	for _, line := range lines[1:] {
		if prerequisite, ok := strings.CutPrefix(line, "-"); ok {
			fields := strings.Fields(prerequisite)
			if len(fields) > 0 {
				header.prerequisites = append(header.prerequisites, fields[0])
			}
			continue
		}
		header.refs = append(header.refs, line)
	}
	return header, nil
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
	// Production baseline/artifact roots are intentionally read-only. Restore
	// test-only permissions before testing.T removes its TempDir tree.
	t.Cleanup(func() {
		_ = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return os.Chmod(current, 0o700)
			}
			return os.Chmod(current, 0o600)
		})
	})
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

func runMaterializerGitExpectFailure(t *testing.T, repository string, args ...string) error {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	_, err := command.CombinedOutput()
	return err
}

func createMaterializerCommit(t *testing.T, repository string, message string, parent string, alternateRoot string) {
	t.Helper()
	tree := strings.TrimSpace(runMaterializerGit(t, repository, "write-tree"))
	command := exec.Command("git", "-C", repository, "commit-tree", tree, "-p", parent, "-m", message)
	command.Env = append(os.Environ(), "GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(alternateRoot, "objects"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit-tree: %v\n%s", err, output)
	}
	commit := strings.TrimSpace(string(output))
	runMaterializerGit(t, repository, "update-ref", "refs/heads/main", commit)
}
