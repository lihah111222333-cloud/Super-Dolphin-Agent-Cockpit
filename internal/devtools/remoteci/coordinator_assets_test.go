package remoteci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type remoteAssetsTestFixture struct {
	input    RunInput
	tempRoot string
}

func TestBuildRemoteAssetsBindsAcceptedBaselineAndPublishesBothArtifacts(t *testing.T) {
	fixture := newRemoteAssetsTestFixture(t)
	assets := mustBuildRemoteAssets(t, fixture)
	assertRemoteAssetsIdentity(t, fixture.input, assets)
	assertRemoteAssetsArtifacts(t, fixture.tempRoot, assets)
}

func newRemoteAssetsTestFixture(t *testing.T) remoteAssetsTestFixture {
	t.Helper()
	_, input := remoteRunFixture(t)
	return remoteAssetsTestFixture{input: input, tempRoot: canonicalCoordinatorTempDir(t)}
}

func mustBuildRemoteAssets(t *testing.T, fixture remoteAssetsTestFixture) remoteAssets {
	t.Helper()
	assets, err := buildRemoteAssets(context.Background(), fixture.input, "job-assets", fixture.tempRoot, "source/")
	if err != nil {
		t.Fatalf("buildRemoteAssets() error = %v", err)
	}
	return assets
}

func assertRemoteAssetsIdentity(t *testing.T, input RunInput, assets remoteAssets) {
	t.Helper()
	manifest := assets.materialization.Manifest
	expectedBaseline := mustDeterministicBaselineCommitSHA(t, input)
	expectedTransport := mustDeterministicTransportCommitSHA(t, input, expectedBaseline)
	assertRemoteAssetsBaselineIdentity(t, input, manifest, expectedBaseline)
	assertRemoteAssetsCandidateIdentity(t, input, manifest, expectedTransport)
	assertRemoteAssetsProtocol(t, input, manifest)
	assertRemoteAssetsManifestValid(t, manifest)
	assertRemoteAssetsDigests(t, manifest, assets)
}

func mustDeterministicBaselineCommitSHA(t *testing.T, input RunInput) string {
	t.Helper()
	commitSHA, err := DeterministicSourceBaselineCommitSHA(input.RunnerBaseTree, input.Source.ObjectFormat)
	if err != nil {
		t.Fatalf("DeterministicSourceBaselineCommitSHA() error = %v", err)
	}
	return commitSHA
}

func mustDeterministicTransportCommitSHA(t *testing.T, input RunInput, baselineCommitSHA string) string {
	t.Helper()
	commitSHA, err := DeterministicSourceTransportCommitSHA(input.Source.SourceTreeSHA, baselineCommitSHA, input.Source.ObjectFormat)
	if err != nil {
		t.Fatalf("DeterministicSourceTransportCommitSHA() error = %v", err)
	}
	return commitSHA
}

func assertRemoteAssetsBaselineIdentity(t *testing.T, input RunInput, manifest SourceMaterializationManifest, expectedBaseline string) {
	t.Helper()
	if manifest.BaselineTreeSHA != input.RunnerBaseTree || manifest.BaselineCommitSHA != expectedBaseline {
		t.Fatalf("manifest baseline = tree %q commit %q, want tree %q commit %q", manifest.BaselineTreeSHA, manifest.BaselineCommitSHA, input.RunnerBaseTree, expectedBaseline)
	}
}

func assertRemoteAssetsCandidateIdentity(t *testing.T, input RunInput, manifest SourceMaterializationManifest, expectedTransport string) {
	t.Helper()
	if manifest.SourceTreeSHA != input.Source.SourceTreeSHA || manifest.TransportCommitSHA != expectedTransport {
		t.Fatalf("manifest candidate identity = tree %q transport %q, want tree %q transport %q", manifest.SourceTreeSHA, manifest.TransportCommitSHA, input.Source.SourceTreeSHA, expectedTransport)
	}
}

func assertRemoteAssetsProtocol(t *testing.T, input RunInput, manifest SourceMaterializationManifest) {
	t.Helper()
	if manifest.ObjectFormat != input.Source.ObjectFormat || manifest.TransportKind != sourceTransportKind || manifest.SchemaVersion != sourceManifestVersion {
		t.Fatalf("manifest protocol = schema=%d transport=%q format=%q", manifest.SchemaVersion, manifest.TransportKind, manifest.ObjectFormat)
	}
}

func assertRemoteAssetsManifestValid(t *testing.T, manifest SourceMaterializationManifest) {
	t.Helper()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("materialized manifest validation = %v", err)
	}
}

func assertRemoteAssetsDigests(t *testing.T, manifest SourceMaterializationManifest, assets remoteAssets) {
	t.Helper()
	if assets.bundleDigest != strings.TrimPrefix(manifest.BundleDigest, "sha256:") || assets.bundleSize <= 0 || assets.manifestDigest == "" {
		t.Fatalf("remote assets digests/size = bundle=%q size=%d manifest=%q", assets.bundleDigest, assets.bundleSize, assets.manifestDigest)
	}
}

func assertRemoteAssetsArtifacts(t *testing.T, tempRoot string, assets remoteAssets) {
	t.Helper()
	assertRemoteAssetFile(t, assets.materialization.BundlePath)
	assertRemoteAssetFile(t, assets.materialization.ManifestPath)
	assertRemoteAssetsDirectory(t, assets.materialization.BundlePath)
	assertRemoteAssetsBaselineCleanup(t, tempRoot)
}

func assertRemoteAssetFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat published source artifact %q: %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateSourceFileMode || info.Size() <= 0 {
		t.Fatalf("published source artifact %q = mode=%s size=%d", path, info.Mode(), info.Size())
	}
}

func assertRemoteAssetsDirectory(t *testing.T, bundlePath string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(bundlePath))
	if err != nil {
		t.Fatalf("read source artifact directory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("source artifact directory entries = %d, want exactly bundle and manifest", len(entries))
	}
}

func assertRemoteAssetsBaselineCleanup(t *testing.T, tempRoot string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(tempRoot, "source-baseline.git")); !os.IsNotExist(err) {
		t.Fatalf("local accepted baseline ODB was not cleaned after materialization: err=%v", err)
	}
}

func canonicalCoordinatorTempDir(t *testing.T) string {
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
