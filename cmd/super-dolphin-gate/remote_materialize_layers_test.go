package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestVerifyRemoteDeltaManifestBindsRequestedTransition(t *testing.T) {
	delta := remoteci.BaselineDeltaLayer{
		Generation: 12, ObjectPrefix: "baseline-artifacts/deltas/12",
		ManifestDigest: "sha256:" + strings.Repeat("1", 64),
		BaseCommit:     strings.Repeat("2", 40), BaseTree: strings.Repeat("3", 40),
		MainCommit: strings.Repeat("4", 40), MainTree: strings.Repeat("5", 40),
	}
	manifest := remoteDeltaManifestFixture(delta)
	if err := verifyRemoteDeltaManifest(manifest, delta); err != nil {
		t.Fatalf("verifyRemoteDeltaManifest() error = %v", err)
	}
	manifest.Layers[0].TargetTree = strings.Repeat("6", 40)
	if err := verifyRemoteDeltaManifest(manifest, delta); err == nil {
		t.Fatal("verifyRemoteDeltaManifest() accepted a source transition mismatch")
	}
}

func TestVerifyRemoteDeltaCompatibilityAllowsPolicyChangeAndRejectsRuntimeSeedDrift(t *testing.T) {
	delta := remoteDeltaManifestFixture(remoteci.BaselineDeltaLayer{
		Generation: 12, ObjectPrefix: "baseline-artifacts/12/",
		ManifestDigest: "sha256:" + strings.Repeat("1", 64),
		BaseCommit:     strings.Repeat("2", 40), BaseTree: strings.Repeat("3", 40),
		MainCommit: strings.Repeat("4", 40), MainTree: strings.Repeat("5", 40),
	})
	anchor := delta
	anchor.StorageMode = remoteci.BaselineStorageModeAnchor
	delta.PolicyDigest = "sha256:" + strings.Repeat("d", 64)
	if err := verifyRemoteDeltaCompatibility(anchor, delta); err != nil {
		t.Fatalf("verifyRemoteDeltaCompatibility() error = %v", err)
	}
	delta.RuntimeSeedManifestSHA256 = "sha256:" + strings.Repeat("e", 64)
	if err := verifyRemoteDeltaCompatibility(anchor, delta); err == nil {
		t.Fatal("verifyRemoteDeltaCompatibility() accepted runtime-seed drift")
	}
}

func TestExtractRemoteBaselineArchiveIntoRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "escape.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressor := gzip.NewWriter(file)
	writer := tar.NewWriter(compressor)
	if err := writer.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractRemoteBaselineArchiveInto(t.Context(), archivePath, filepath.Join(root, "expanded")); err == nil {
		t.Fatal("extractRemoteBaselineArchiveInto() accepted a path escape")
	}
}

func TestExtractRemoteBaselineArchiveIntoAllowsContainedLinks(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "links.tar.gz")
	writeRemoteArchiveFixture(t, archivePath, []remoteArchiveFixtureEntry{
		{header: tar.Header{Name: "lib/tool", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4}, data: "tool"},
		{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeSymlink, Linkname: "../lib/tool"}},
		{header: tar.Header{Name: "lib/tool-copy", Typeflag: tar.TypeLink, Linkname: "lib/tool"}},
	})
	destination := filepath.Join(root, "expanded")
	if err := extractRemoteBaselineArchiveInto(t.Context(), archivePath, destination); err != nil {
		t.Fatalf("extractRemoteBaselineArchiveInto() error = %v", err)
	}
	for _, name := range []string{"bin/tool", "lib/tool-copy"} {
		data, err := os.ReadFile(filepath.Join(destination, name))
		if err != nil || string(data) != "tool" {
			t.Fatalf("read extracted link %q = %q, %v", name, data, err)
		}
	}
}

func TestExtractRemoteBaselineArchiveIntoRelocatesRootFSAbsoluteSymlink(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "rootfs-links.tar.gz")
	writeRemoteArchiveFixture(t, archivePath, []remoteArchiveFixtureEntry{
		{header: tar.Header{Name: "runtime/rootfs/usr/share/ca-certificates/mozilla/ACCVRAIZ1.crt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4}, data: "cert"},
		{header: tar.Header{Name: "runtime/rootfs/etc/ssl/certs/ACCVRAIZ1.pem", Typeflag: tar.TypeSymlink, Linkname: "/usr/share/ca-certificates/mozilla/ACCVRAIZ1.crt"}},
	})
	destination := filepath.Join(root, "expanded")
	if err := extractRemoteBaselineArchiveInto(t.Context(), archivePath, destination); err != nil {
		t.Fatalf("extractRemoteBaselineArchiveInto() error = %v", err)
	}
	linkPath := filepath.Join(destination, "runtime/rootfs/etc/ssl/certs/ACCVRAIZ1.pem")
	linkTarget, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if filepath.IsAbs(linkTarget) {
		t.Fatalf("rootfs link target = %q, want contained relative target", linkTarget)
	}
	data, err := os.ReadFile(linkPath)
	if err != nil || string(data) != "cert" {
		t.Fatalf("read relocated rootfs link = %q, %v", data, err)
	}
}

func TestExtractRemoteBaselineArchiveIntoPreservesDirectoryModes(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "directory-modes.tar.gz")
	writeRemoteArchiveFixture(t, archivePath, []remoteArchiveFixtureEntry{
		{header: tar.Header{Name: "runtime/go-mod-cache/example", Typeflag: tar.TypeDir, Mode: 0o555}},
		{header: tar.Header{Name: "runtime/go-mod-cache/example/go.mod", Typeflag: tar.TypeReg, Mode: 0o444, Size: 6}, data: "module"},
	})
	destination := filepath.Join(root, "expanded")
	readOnlyDirectory := filepath.Join(destination, "runtime/go-mod-cache/example")
	t.Cleanup(func() {
		if err := os.Chmod(readOnlyDirectory, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("restore extracted directory mode: %v", err)
		}
	})
	if err := extractRemoteBaselineArchiveInto(t.Context(), archivePath, destination); err != nil {
		t.Fatalf("extractRemoteBaselineArchiveInto() error = %v", err)
	}
	info, err := os.Stat(readOnlyDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o555 {
		t.Fatalf("directory mode = %o, want 555", got)
	}
}

func TestExtractRemoteBaselineArchiveIntoRejectsLinkEscapeAndLinkedParent(t *testing.T) {
	for name, entries := range map[string][]remoteArchiveFixtureEntry{
		"escape":           {{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}}},
		"absolute outside": {{header: tar.Header{Name: "bin/tool", Typeflag: tar.TypeSymlink, Linkname: "/usr/bin/tool"}}},
		"parent": {
			{header: tar.Header{Name: "linked", Typeflag: tar.TypeSymlink, Linkname: "real"}},
			{header: tar.Header{Name: "linked/file", Typeflag: tar.TypeReg, Mode: 0o600, Size: 1}, data: "x"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			archivePath := filepath.Join(root, "invalid.tar.gz")
			writeRemoteArchiveFixture(t, archivePath, entries)
			if err := extractRemoteBaselineArchiveInto(t.Context(), archivePath, filepath.Join(root, "expanded")); err == nil {
				t.Fatal("extractRemoteBaselineArchiveInto() accepted an unsafe link")
			}
		})
	}
}

func TestMaterializeRemoteCacheSeedUsesFixedWidthGeneration(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache-seed", "go-build", "aa")
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "entry-a"), []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := materializeRemoteCacheSeed(root, 12); err != nil {
		t.Fatalf("materializeRemoteCacheSeed() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "cache-seeds", "00000000000000000012")); err != nil {
		t.Fatalf("fixed-width cache seed is missing: %v", err)
	}
}

func TestMaterializeRemoteCacheDeltaStagesInsideExpandedVolume(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "cache-delta.tar.gz")
	writeRemoteArchiveFixture(t, archivePath, []remoteArchiveFixtureEntry{
		{header: tar.Header{Name: "cache-seed/go-build/aa/entry", Typeflag: tar.TypeReg, Mode: 0o600, Size: 5}, data: "cache"},
	})
	expandedRoot := filepath.Join(root, "expanded")
	if err := os.Mkdir(expandedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := materializeRemoteCacheDelta(t.Context(), archivePath, expandedRoot, 30); err != nil {
		t.Fatalf("materializeRemoteCacheDelta() error = %v", err)
	}
	cachePath := filepath.Join(expandedRoot, "cache-seeds", "00000000000000000030", "aa", "entry")
	data, err := os.ReadFile(cachePath)
	if err != nil || string(data) != "cache" {
		t.Fatalf("read materialized cache delta = %q, %v", data, err)
	}
	entries, err := filepath.Glob(filepath.Join(expandedRoot, ".super-dolphin-cache-delta-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("cache delta staging entries = %v, %v", entries, err)
	}
}

func TestRunRemoteBaselineLayerStageStartsAllLayersConcurrently(t *testing.T) {
	layers := []remoteci.BaselineLayer{
		{Name: "runtime-deps", Archive: "runtime-deps.tar.gz"},
		{Name: "source", Archive: "source.tar.gz"},
		{Name: "go-build-cache", Archive: "go-build-cache.tar.gz"},
	}
	started := make(chan string, len(layers))
	release := make(chan struct{})
	result := runRemoteBaselineLayerStageAsync(t, layers, "test",
		func(ctx context.Context, _ string, _ string, layer remoteci.BaselineLayer) error {
			started <- layer.Name
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})

	seen := make(map[string]struct{}, len(layers))
	for range layers {
		select {
		case name := <-started:
			seen[name] = struct{}{}
		case <-time.After(time.Second):
			t.Fatal("layer stage did not start every independent layer concurrently")
		}
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("runRemoteBaselineLayerStage() error = %v", err)
	}
	if len(seen) != len(layers) {
		t.Fatalf("started layers = %v", seen)
	}
}

func TestRunRemoteBaselineLayerStagePreservesRootFailureAfterCancellation(t *testing.T) {
	layers := []remoteci.BaselineLayer{
		{Name: "runtime-deps", Archive: "runtime-deps.tar.gz"},
		{Name: "go-build-cache", Archive: "go-build-cache.tar.gz"},
	}
	boom := errors.New("archive is corrupt")
	err := runRemoteBaselineLayerStage(t.Context(), t.TempDir(), t.TempDir(), layers, "validate",
		func(ctx context.Context, _ string, _ string, layer remoteci.BaselineLayer) error {
			if layer.Name == "go-build-cache" {
				return boom
			}
			<-ctx.Done()
			return ctx.Err()
		})
	if !errors.Is(err, boom) {
		t.Fatalf("runRemoteBaselineLayerStage() error = %v, want root failure", err)
	}
}

func TestValidateRemoteArchiveLayerRejectsCrossLayerEntriesAndLinks(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "valid.tar.gz")
	writeRemoteArchiveFixture(t, valid, []remoteArchiveFixtureEntry{
		{header: tar.Header{Name: "source/main.go", Mode: 0o644, Size: 4}, data: "main"},
		{header: tar.Header{Name: "frontend-embed/index.html", Mode: 0o644, Size: 2}, data: "ok"},
	})
	if err := validateRemoteArchiveLayer(valid, "source"); err != nil {
		t.Fatalf("validateRemoteArchiveLayer(valid) error = %v", err)
	}

	for name, entries := range map[string][]remoteArchiveFixtureEntry{
		"entry": {
			{header: tar.Header{Name: "source/main.go", Mode: 0o644, Size: 4}, data: "main"},
		},
		"hard-link": {
			{header: tar.Header{Name: "runtime/link", Typeflag: tar.TypeLink, Linkname: "source/main.go", Mode: 0o644}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			archivePath := filepath.Join(root, name+".tar.gz")
			writeRemoteArchiveFixture(t, archivePath, entries)
			if err := validateRemoteArchiveLayer(archivePath, "runtime-deps"); err == nil {
				t.Fatal("validateRemoteArchiveLayer() accepted a cross-layer archive")
			}
		})
	}
}

func TestExtractRemoteBaselineLayerPublishesOnlyAfterCompleteValidation(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "runtime-deps.tar.gz")
	writeRemoteArchiveFixture(t, archivePath, []remoteArchiveFixtureEntry{
		{header: tar.Header{Name: "runtime/valid", Mode: 0o644, Size: 2}, data: "ok"},
		{header: tar.Header{Name: "source/escape", Mode: 0o644, Size: 3}, data: "bad"},
	})
	destination := filepath.Join(root, "expanded")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractRemoteBaselineLayer(t.Context(), archivePath, destination, "runtime-deps"); err == nil {
		t.Fatal("extractRemoteBaselineLayer() accepted a cross-layer archive")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed layer published entries: %v", entries)
	}
}

func remoteDeltaManifestFixture(delta remoteci.BaselineDeltaLayer) remoteci.BaselineManifest {
	return remoteci.BaselineManifest{
		SchemaVersion: remoteci.BaselineManifestSchemaVersion,
		Generation:    delta.Generation, MainCommit: delta.MainCommit, MainTree: delta.MainTree,
		Platform: "linux/amd64", PolicyDigest: "sha256:" + strings.Repeat("6", 64),
		ToolchainDigest:  "sha256:" + strings.Repeat("7", 64),
		RuntimeImage:     "registry.example/runtime@sha256:" + strings.Repeat("8", 64),
		GateSourceSHA256: "sha256:" + strings.Repeat("e", 64),
		GateBinarySHA256: "sha256:" + strings.Repeat("9", 64), GateBinarySize: 1,
		RuntimeSeedManifestSHA256: "sha256:" + strings.Repeat("a", 64),
		CABundleSHA256:            "sha256:" + strings.Repeat("b", 64), CABundleSize: 1,
		StorageMode: remoteci.BaselineStorageModeDelta,
		Layers: []remoteci.BaselineLayer{
			{Name: "source", Archive: "source.delta.bundle", SHA256: "sha256:" + strings.Repeat("c", 64), Size: 1, Generation: delta.Generation, Kind: remoteci.BaselineLayerKindDelta, BaseCommit: delta.BaseCommit, BaseTree: delta.BaseTree, TargetCommit: delta.MainCommit, TargetTree: delta.MainTree},
			{Name: "go-build-cache", Archive: "go-build-cache.delta.tar.gz", SHA256: "sha256:" + strings.Repeat("d", 64), Size: 1, Generation: delta.Generation, Kind: remoteci.BaselineLayerKindDelta},
		},
	}
}

type remoteArchiveFixtureEntry struct {
	header tar.Header
	data   string
}

func writeRemoteArchiveFixture(t *testing.T, archivePath string, entries []remoteArchiveFixtureEntry) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	compressor := gzip.NewWriter(file)
	writer := tar.NewWriter(compressor)
	for _, entry := range entries {
		header := entry.header
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if entry.data != "" {
			if _, err := writer.Write([]byte(entry.data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(writer.Close(), compressor.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
}
