package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestLoadRemoteMaterializeConfigAcceptsNestedRequestKey(t *testing.T) {
	values := map[string]string{
		remoteWorkerRoleEnv:       "worker-role",
		remoteOSSEndpointEnv:      "oss-cn-shenzhen-internal.aliyuncs.com",
		remoteOSSBucketEnv:        "ci-bucket",
		remoteRequestKeyEnv:       "baseline-artifacts/source-deltas/job-1234/shard-00.request.json",
		remoteRequestSHA256Env:    strings.Repeat("a", sha256.Size*2),
		remoteBaselineManifestEnv: "sha256:" + strings.Repeat("b", sha256.Size*2),
		remoteSSLCAFileEnv:        remoteDataCacheRootPath + "/ca-certificates.crt",
	}
	config, err := loadRemoteMaterializeConfig(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadRemoteMaterializeConfig() error = %v", err)
	}
	if config.RequestKey != values[remoteRequestKeyEnv] || config.BaselineManifest != values[remoteBaselineManifestEnv] {
		t.Fatalf("remote request key = %q", config.RequestKey)
	}
}

func TestHandoffRemoteWorkRoot(t *testing.T) {
	root := t.TempDir()
	var mode os.FileMode
	var uid, gid int
	err := handoffRemoteWorkRoot(
		root,
		func(path string, value os.FileMode) error {
			if path != root {
				t.Fatalf("chmod path = %q, want %q", path, root)
			}
			mode = value
			return nil
		},
		func(path string, gotUID int, gotGID int) error {
			if path != root {
				t.Fatalf("chown path = %q, want %q", path, root)
			}
			uid, gid = gotUID, gotGID
			return nil
		},
	)
	if err != nil {
		t.Fatalf("handoffRemoteWorkRoot() error = %v", err)
	}
	if mode != 0o700 || uid != remoteExecutorUID || gid != remoteExecutorGID {
		t.Fatalf("handoff = mode %o uid %d gid %d", mode, uid, gid)
	}
}

func TestHandoffRemoteWorkRootRejectsNonEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := handoffRemoteWorkRoot(root, os.Chmod, os.Chown); err == nil {
		t.Fatal("handoffRemoteWorkRoot() unexpectedly passed")
	}
}

func TestDownloadVerifiedFileCleansFailedStagingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.patch")
	expected := digestRemoteFixture([]byte("expected"))
	err := downloadVerifiedFile(context.Background(), func(context.Context, string, int64, io.Writer) (int64, error) {
		return 0, errors.New("temporary OSS failure")
	}, "source.patch", expected, 1024, path)
	if err == nil || !strings.Contains(err.Error(), "temporary OSS failure") {
		t.Fatalf("downloadVerifiedFile() error = %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed download left staging files: %v", entries)
	}
}

func TestExtractRemoteDataCacheBaseVerifiesAndExpandsArchive(t *testing.T) {
	cacheRoot, expandedRoot, manifestDigest := writeRemoteBaselineArchiveFixture(t)
	manifest, err := extractRemoteDataCacheBase(
		context.Background(),
		cacheRoot,
		expandedRoot,
		manifestDigest,
	)
	if err != nil {
		t.Fatalf("extractRemoteDataCacheBase() error = %v", err)
	}
	if len(manifest.Layers) != 3 || manifest.ArchiveSize != 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
	data, err := os.ReadFile(filepath.Join(expandedRoot, "source", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "baseline\n" {
		t.Fatalf("expanded source = %q", data)
	}
}

func TestExtractRemoteDataCacheBaseRejectsManifestOrArchiveDrift(t *testing.T) {
	cacheRoot, expandedRoot, manifestDigest := writeRemoteBaselineArchiveFixture(t)
	if _, err := extractRemoteDataCacheBase(
		context.Background(),
		cacheRoot,
		expandedRoot,
		"sha256:"+strings.Repeat("0", 64),
	); err == nil {
		t.Fatal("extractRemoteDataCacheBase() accepted manifest drift")
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "go-build-cache.tar.gz"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractRemoteDataCacheBase(
		context.Background(),
		cacheRoot,
		expandedRoot,
		manifestDigest,
	); err == nil {
		t.Fatal("extractRemoteDataCacheBase() accepted archive drift")
	}
	entries, err := os.ReadDir(expandedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("layer extraction started before all layers were verified: %v", entries)
	}
}

func TestExtractRemoteDataCacheBaseAcceptsLegacySingleArchive(t *testing.T) {
	cacheRoot, expandedRoot, manifestDigest := writeRemoteBaselineLegacyArchiveFixture(t)
	manifest, err := extractRemoteDataCacheBase(
		context.Background(),
		cacheRoot,
		expandedRoot,
		manifestDigest,
	)
	if err != nil {
		t.Fatalf("extractRemoteDataCacheBase() legacy error = %v", err)
	}
	if manifest.SchemaVersion != 6 || manifest.ArchiveSize <= 0 || len(manifest.Layers) != 0 {
		t.Fatalf("legacy manifest = %#v", manifest)
	}
}

func TestExtractRemoteDataCacheBaseAcceptsV7LayeredArchive(t *testing.T) {
	cacheRoot, expandedRoot, manifestDigest := writeRemoteBaselineV7ArchiveFixture(t)
	manifest, err := extractRemoteDataCacheBase(
		context.Background(),
		cacheRoot,
		expandedRoot,
		manifestDigest,
	)
	if err != nil {
		t.Fatalf("extractRemoteDataCacheBase() v7 error = %v", err)
	}
	if manifest.SchemaVersion != 7 || len(manifest.Layers) != 3 {
		t.Fatalf("v7 manifest = %#v", manifest)
	}
}

func TestVerifyRemoteBootstrapTLSRejectsCABundleDrift(t *testing.T) {
	cacheRoot, _, manifestDigest := writeRemoteBaselineArchiveFixture(t)
	caFile := filepath.Join(cacheRoot, "ca-certificates.crt")
	if err := verifyRemoteBootstrapTLS(cacheRoot, caFile, manifestDigest); err != nil {
		t.Fatalf("verifyRemoteBootstrapTLS() error = %v", err)
	}
	if err := os.WriteFile(caFile, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRemoteBootstrapTLS(cacheRoot, caFile, manifestDigest); err == nil {
		t.Fatal("verifyRemoteBootstrapTLS() accepted CA bundle drift")
	}
}

func writeRemoteBaselineArchiveFixture(t *testing.T) (string, string, string) {
	t.Helper()
	return writeRemoteBaselineLayerArchiveFixture(t, remoteci.BaselineManifestSchemaVersion)
}

func writeRemoteBaselineV7ArchiveFixture(t *testing.T) (string, string, string) {
	t.Helper()
	return writeRemoteBaselineLayerArchiveFixture(t, 7)
}

func writeRemoteBaselineLayerArchiveFixture(t *testing.T, schemaVersion uint32) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	payloadRoot := filepath.Join(root, "payload")
	expandedRoot := filepath.Join(root, "expanded")
	files := writeRemoteBaselinePayloadFixture(t, cacheRoot, payloadRoot, expandedRoot)
	layerSpecs := []struct {
		name    string
		archive string
		entries []string
	}{
		{name: "runtime-deps", archive: "runtime-deps.tar.gz", entries: []string{"runtime"}},
		{name: "source", archive: "source.tar.gz", entries: []string{"source", "frontend-embed"}},
		{name: "go-build-cache", archive: "go-build-cache.tar.gz", entries: []string{"cache-seed"}},
	}
	layers := make([]remoteci.BaselineLayer, 0, len(layerSpecs))
	for _, spec := range layerSpecs {
		archivePath := filepath.Join(cacheRoot, spec.archive)
		writeTarFixtureEntries(t, payloadRoot, archivePath, spec.entries...)
		info, err := os.Stat(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		layers = append(layers, remoteci.BaselineLayer{
			Name: spec.name, Archive: spec.archive,
			SHA256: digestRemoteFixtureFile(t, archivePath), Size: info.Size(),
		})
		if schemaVersion == remoteci.BaselineManifestSchemaVersion {
			layers[len(layers)-1].Generation = 1
			layers[len(layers)-1].Kind = remoteci.BaselineLayerKindAnchor
		}
	}
	caData := []byte(files["runtime/rootfs/etc/ssl/certs/ca-certificates.crt"])
	if err := os.WriteFile(filepath.Join(cacheRoot, "ca-certificates.crt"), caData, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := remoteci.BaselineManifest{
		SchemaVersion:             schemaVersion,
		Generation:                1,
		MainCommit:                strings.Repeat("1", 40),
		MainTree:                  strings.Repeat("2", 40),
		Platform:                  "linux/amd64",
		PolicyDigest:              "sha256:" + strings.Repeat("3", 64),
		ToolchainDigest:           "sha256:" + strings.Repeat("4", 64),
		RuntimeImage:              "registry.example/runtime@sha256:" + strings.Repeat("5", 64),
		GateBinarySHA256:          digestRemoteFixture([]byte(files["bin/super-dolphin-gate"])),
		GateBinarySize:            int64(len(files["bin/super-dolphin-gate"])),
		RuntimeSeedManifestSHA256: digestRemoteFixture([]byte(files["runtime/manifest.json"])),
		CABundleSHA256:            digestRemoteFixture(caData),
		CABundleSize:              int64(len(caData)),
		Layers:                    layers,
	}
	if schemaVersion == remoteci.BaselineManifestSchemaVersion {
		manifest.GateSourceSHA256 = "sha256:" + strings.Repeat("6", 64)
		manifest.StorageMode = remoteci.BaselineStorageModeAnchor
	}
	return writeRemoteBaselineManifestFixture(t, cacheRoot, expandedRoot, manifest)
}

func writeRemoteBaselineLegacyArchiveFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "cache")
	payloadRoot := filepath.Join(root, "payload")
	expandedRoot := filepath.Join(root, "expanded")
	files := writeRemoteBaselinePayloadFixture(t, cacheRoot, payloadRoot, expandedRoot)
	archivePath := filepath.Join(cacheRoot, "baseline.tar.gz")
	writeTarFixtureEntries(t, payloadRoot, archivePath, ".")
	archiveInfo, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	caData := []byte(files["runtime/rootfs/etc/ssl/certs/ca-certificates.crt"])
	if err := os.WriteFile(filepath.Join(cacheRoot, "ca-certificates.crt"), caData, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := remoteci.BaselineManifest{
		SchemaVersion:             6,
		Generation:                1,
		MainCommit:                strings.Repeat("1", 40),
		MainTree:                  strings.Repeat("2", 40),
		Platform:                  "linux/amd64",
		PolicyDigest:              "sha256:" + strings.Repeat("3", 64),
		ToolchainDigest:           "sha256:" + strings.Repeat("4", 64),
		RuntimeImage:              "registry.example/runtime@sha256:" + strings.Repeat("5", 64),
		GateBinarySHA256:          digestRemoteFixture([]byte(files["bin/super-dolphin-gate"])),
		GateBinarySize:            int64(len(files["bin/super-dolphin-gate"])),
		RuntimeSeedManifestSHA256: digestRemoteFixture([]byte(files["runtime/manifest.json"])),
		CABundleSHA256:            digestRemoteFixture(caData),
		CABundleSize:              int64(len(caData)),
		ArchiveSHA256:             digestRemoteFixtureFile(t, archivePath),
		ArchiveSize:               archiveInfo.Size(),
	}
	return writeRemoteBaselineManifestFixture(t, cacheRoot, expandedRoot, manifest)
}

func writeRemoteBaselineManifestFixture(
	t *testing.T,
	cacheRoot string,
	expandedRoot string,
	manifest remoteci.BaselineManifest,
) (string, string, string) {
	t.Helper()
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "baseline-manifest.json"), manifestData, 0o644); err != nil {
		t.Fatal(err)
	}
	return cacheRoot, expandedRoot, remoteci.BaselineManifestDigest(manifestData)
}

func writeRemoteBaselinePayloadFixture(t *testing.T, cacheRoot string, payloadRoot string, expandedRoot string) map[string]string {
	t.Helper()
	for _, path := range []string{
		filepath.Join(cacheRoot, "bin"),
		filepath.Join(payloadRoot, "bin"),
		filepath.Join(payloadRoot, "cache-seed"),
		filepath.Join(payloadRoot, "frontend-embed"),
		filepath.Join(payloadRoot, "runtime"),
		filepath.Join(payloadRoot, "source"),
		expandedRoot,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"bin/super-dolphin-gate":                           "gate\n",
		"runtime/manifest.json":                            "{}\n",
		"runtime/rootfs/etc/ssl/certs/ca-certificates.crt": "fixture-ca\n",
		"source/README.md":                                 "baseline\n",
		"cache-seed/.keep":                                 "cache\n",
		"frontend-embed/index.html":                        "embed\n",
	}
	for name, content := range files {
		path := filepath.Join(payloadRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"super-dolphin-gate"} {
		data, err := os.ReadFile(filepath.Join(payloadRoot, "bin", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheRoot, "bin", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func writeTarFixtureEntries(t *testing.T, root string, destination string, entries ...string) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	compressor := newFastGzipFixtureWriter(t, file)
	writer := tar.NewWriter(compressor)
	for _, entry := range entries {
		err = writeTarFixtureEntry(root, entry, writer)
		if err != nil {
			break
		}
	}
	closeErr := writer.Close()
	compressorCloseErr := compressor.Close()
	fileCloseErr := file.Close()
	if combined := errorsJoinFixture(err, closeErr, compressorCloseErr, fileCloseErr); combined != nil {
		t.Fatal(combined)
	}
}

func writeTarFixtureEntry(root string, entry string, writer *tar.Writer) error {
	return filepath.Walk(filepath.Join(root, filepath.FromSlash(entry)), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		return errorsJoinFixture(copyErr, closeErr)
	})
}

func newFastGzipFixtureWriter(t *testing.T, writer io.Writer) *gzip.Writer {
	t.Helper()
	compressor, err := gzip.NewWriterLevel(writer, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	return compressor
}

func digestRemoteFixture(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", sum)
}

func digestRemoteFixtureFile(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func errorsJoinFixture(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
