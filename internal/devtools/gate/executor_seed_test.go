package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const runtimeProxyFixtureSum = "github.com/kelindar/event v1.5.2 h1:qtgssZqMh/QQMCIxlbx4wU3DoMHOrJXKdiZhphJ4YbY=\n"

func TestDiscoverExecutorGoBuildCacheSeedRootsOrdersGenerationsAndRejectsUnavailableRoot(t *testing.T) {
	missingRoot := filepath.Join(realTempDir(t), "cache-seeds")
	if _, err := discoverExecutorGoBuildCacheSeedRoots(missingRoot); err == nil {
		t.Fatal("discoverExecutorGoBuildCacheSeedRoots accepted missing generation root")
	}

	emptyRoot := filepath.Join(realTempDir(t), "cache-seeds")
	if err := os.Mkdir(emptyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverExecutorGoBuildCacheSeedRoots(emptyRoot); err == nil {
		t.Fatal("discoverExecutorGoBuildCacheSeedRoots accepted empty generation root")
	}

	generationsRoot := filepath.Join(realTempDir(t), "cache-seeds")
	for _, name := range []string{"00000000000020260728", "00000000000020260729"} {
		if err := os.MkdirAll(filepath.Join(generationsRoot, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seedRoots, err := discoverExecutorGoBuildCacheSeedRoots(generationsRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(generationsRoot, "00000000000020260729"), filepath.Join(generationsRoot, "00000000000020260728")}
	if !slices.Equal(seedRoots, want) {
		t.Fatalf("generation seed roots = %v, want %v", seedRoots, want)
	}
}

func TestDiscoverExecutorGoBuildCacheSeedRootsRejectsInvalidGenerations(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, root string){
		"symlink": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Symlink(root, filepath.Join(root, "00000000000020260729")); err != nil {
				t.Fatal(err)
			}
		},
		"file": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "00000000000020260729"), "not a directory", 0o600)
		},
		"too many": func(t *testing.T, root string) {
			t.Helper()
			for _, generation := range []string{"00000000000000000001", "00000000000000000002", "00000000000000000003", "00000000000000000004", "00000000000000000005", "00000000000000000006"} {
				if err := os.Mkdir(filepath.Join(root, generation), 0o700); err != nil {
					t.Fatal(err)
				}
			}
		},
		"non canonical generation": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(root, "20260729"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(realTempDir(t), "cache-seeds")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			prepare(t, root)
			if _, err := discoverExecutorGoBuildCacheSeedRoots(root); err == nil {
				t.Fatal("discoverExecutorGoBuildCacheSeedRoots unexpectedly accepted invalid generations")
			}
		})
	}
}

func TestSeedExecutorGoBuildCacheSeedsValidatesLayersWithoutCopying(t *testing.T) {
	newest := realTempDir(t)
	oldest := realTempDir(t)
	privateRoot := realTempDir(t)
	writeTestFile(t, filepath.Join(newest, "newest"), "new", 0o600)
	writeTestFile(t, filepath.Join(oldest, "oldest"), "old", 0o600)
	if err := seedExecutorGoBuildCacheSeeds([]string{newest, oldest}, privateRoot); err != nil {
		t.Fatalf("validate multi-layer Go build cache seed: %v", err)
	}
	assertDirectoryEmpty(t, privateRoot)
	if err := seedExecutorGoBuildCacheSeeds([]string{newest, filepath.Join(newest, "nested")}, privateRoot); err == nil {
		t.Fatal("seed validation accepted overlapping layers")
	}
}

func TestExecuteExecutorOwnsRuntimeSeedWriteAndVerify(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"write", "verify"} {
		err := ExecuteExecutor(
			context.Background(),
			[]string{"runtime-seed", action, source, runtimeRoot},
			io.Discard,
			io.Discard,
		)
		if err != nil {
			t.Fatalf("ExecuteExecutor(runtime-seed %s) error = %v", action, err)
		}
	}
}

func TestExecuteExecutorOwnsGoModuleOverlay(t *testing.T) {
	sharedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	metadata := filepath.Join(sharedRoot, "cache", "download", "example.com", "module", "@v", "v1.0.0.mod")
	moduleSource := filepath.Join(sharedRoot, "example.com", "module@v1.0.0", "module.go")
	writeTestFile(t, metadata, "module example.com/module\n", 0o444)
	writeTestFile(t, moduleSource, "package module\n", 0o444)

	err := ExecuteExecutor(
		context.Background(),
		[]string{"go-module-overlay", sharedRoot, privateRoot},
		io.Discard,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("ExecuteExecutor(go-module-overlay) error = %v", err)
	}
	assertRuntimeSeedSymlink(t, filepath.Join(privateRoot, "example.com"), filepath.Join(sharedRoot, "example.com"))
	assertRuntimeSeedSymlink(t, filepath.Join(privateRoot, "cache", "download", "example.com", "module", "@v", "v1.0.0.mod"), metadata)
	if err := ExecuteExecutor(context.Background(), []string{"go-module-overlay", sharedRoot}, io.Discard, io.Discard); err == nil {
		t.Fatal("ExecuteExecutor(go-module-overlay) accepted missing private root")
	}
}

func TestInstallRuntimeSeedsCreatesFrontendOverlayWithoutCopyingDependencies(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	config := executorConfig{runtimeSeedRoot: runtimeRoot, runtimeSeedManifest: manifestPath}
	npmCache := filepath.Join(realTempDir(t), "npm-cache")
	requireRuntimeSeedTestNoError(t, "create npm cache", os.Mkdir(npmCache, 0o700))
	layout := executorLayout{sourceCopy: source, npmCache: npmCache}
	program := ExecutorProgram{NeedsGoSeed: true, NeedsFrontendSeed: true}
	requireRuntimeSeedTestNoError(t, "install runtime seeds", installRuntimeSeeds(config, layout, program))
	assertRuntimeSeedPathMissing(t, filepath.Join(source, "vendor"))
	nodeModules := filepath.Join(source, "frontend-app", "node_modules")
	assertRuntimeSeedPhysicalDirectory(t, nodeModules)
	assertRuntimeSeedSymlink(
		t,
		filepath.Join(nodeModules, "tool"),
		filepath.Join(runtimeRoot, "frontend", "node_modules", "tool"),
	)
	assertRuntimeSeedSymlink(t, filepath.Join(nodeModules, ".vite"), filepath.Join(runtimeRoot, "frontend", "vite-cache"))
	assertRuntimeSeedPhysicalDirectory(t, filepath.Join(nodeModules, ".vite-temp"))
	requireRuntimeSeedTestNoError(t, "read shared Vite cache", runtimeSeedPathExists(filepath.Join(nodeModules, ".vite", "deps", "_metadata.json")))
	requireRuntimeSeedTestNoError(t, "read shared frontend seed", runtimeSeedPathExists(filepath.Join(nodeModules, "tool", "index.js")))
	assertDirectoryEmpty(t, npmCache)
	if err := installRuntimeSeeds(config, layout, program); err == nil {
		t.Fatal("runtime seed unexpectedly overwrote an existing target")
	}
}

func TestBindSharedGoModuleCacheRejectsMissingDownloadMetadata(t *testing.T) {
	sharedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	err := bindSharedGoModuleCache(sharedRoot, privateRoot)
	if err == nil || !strings.Contains(err.Error(), "shared download metadata") {
		t.Fatalf("bindSharedGoModuleCache error = %v, want missing download metadata", err)
	}
	if info, statErr := os.Lstat(privateRoot); statErr != nil || !info.IsDir() {
		t.Fatalf("private cache mountpoint changed after rejection: info=%v err=%v", info, statErr)
	}
}

func TestBindSharedGoModuleCacheSharesFilesWithWritableMetadataDirectories(t *testing.T) {
	sharedRoot := realTempDir(t)
	privateRoot := realTempDir(t)
	requireRuntimeSeedTestNoError(t, "make private cache writable", os.Chmod(privateRoot, 0o700))
	metadataRoot := filepath.Join(sharedRoot, "cache", "download", "example.com", "module", "@v")
	metadata := filepath.Join(metadataRoot, "v1.0.0.mod")
	metadataList := filepath.Join(metadataRoot, "list")
	metadataLock := filepath.Join(metadataRoot, "v1.0.0.lock")
	moduleSource := filepath.Join(sharedRoot, "example.com", "module@v1.0.0", "module.go")
	writeTestFile(t, metadata, "module example.com/module\n", 0o444)
	writeTestFile(t, metadataList, "v1.0.0\n", 0o444)
	writeTestFile(t, metadataLock, "", 0o444)
	writeTestFile(t, moduleSource, "package module\n", 0o444)
	sharedDigest := mustRuntimeSeedTreeDigest(t, sharedRoot)

	requireRuntimeSeedTestNoError(t, "bind shared Go module cache", bindSharedGoModuleCache(sharedRoot, privateRoot))
	assertRuntimeSeedPhysicalDirectory(t, privateRoot)
	privateModule := filepath.Join(privateRoot, "example.com")
	assertRuntimeSeedSymlink(t, privateModule, filepath.Join(sharedRoot, "example.com"))
	privateMetadataRoot := filepath.Join(privateRoot, "cache", "download", "example.com", "module", "@v")
	privateMetadata := filepath.Join(privateMetadataRoot, "v1.0.0.mod")
	assertRuntimeSeedSymlink(t, privateMetadata, metadata)
	privateList := filepath.Join(privateMetadataRoot, "list")
	privateLock := filepath.Join(privateMetadataRoot, "v1.0.0.lock")
	assertRuntimeSeedPhysicalFile(t, privateList)
	assertRuntimeSeedPhysicalFile(t, privateLock)
	requireRuntimeSeedTestNoError(t, "update private module list", os.WriteFile(privateList, []byte("v1.0.0\nv1.1.0\n"), 0o600))
	requireRuntimeSeedTestNoError(t, "update private module lock", os.WriteFile(privateLock, []byte("private\n"), 0o600))
	newMetadataRoot := filepath.Join(privateRoot, "cache", "download", "new.example", "module", "@v")
	requireRuntimeSeedTestNoError(t, "create private metadata directory", os.MkdirAll(newMetadataRoot, 0o700))
	requireRuntimeSeedTestNoError(
		t,
		"write private metadata",
		os.WriteFile(filepath.Join(newMetadataRoot, "v1.0.0.info"), []byte("{}\n"), 0o600),
	)
	if after := mustRuntimeSeedTreeDigest(t, sharedRoot); after != sharedDigest {
		t.Fatalf("shared Go module cache digest changed through private overlay: got %s, want %s", after, sharedDigest)
	}
}

func requireRuntimeSeedTestNoError(t *testing.T, operation string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", operation, err)
	}
}

func assertRuntimeSeedPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runtime seed unexpectedly materialized %q: %v", path, err)
	}
}

func assertRuntimeSeedPhysicalDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("runtime seed path %q is not a private physical directory: info=%v err=%v", path, info, err)
	}
}

func assertRuntimeSeedPhysicalFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("runtime seed path %q is not a private physical file: info=%v err=%v", path, info, err)
	}
}

func assertRuntimeSeedSymlink(t *testing.T, path string, expected string) {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil || target != expected {
		t.Fatalf("runtime seed link %q = %q, want %q, err=%v", path, target, expected, err)
	}
}

func runtimeSeedPathExists(path string) error {
	_, err := os.Stat(path)
	return err
}

func TestInstallRuntimeSeedsRejectsSnapshotLockDrift(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	writeTestFile(t, filepath.Join(source, "go.sum"), "drifted\n", 0o600)
	config := executorConfig{runtimeSeedRoot: runtimeRoot, runtimeSeedManifest: manifestPath}
	program := ExecutorProgram{NeedsGoSeed: true}
	if err := installRuntimeSeeds(config, executorLayout{sourceCopy: source}, program); err == nil {
		t.Fatal("runtime seed unexpectedly accepted go.sum drift")
	}
}

func TestInstallRuntimeSeedsRejectsPackageLockDrift(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":2}\n", 0o600)
	config := executorConfig{runtimeSeedRoot: runtimeRoot, runtimeSeedManifest: manifestPath}
	program := ExecutorProgram{NeedsFrontendSeed: true}
	if err := installRuntimeSeeds(config, executorLayout{sourceCopy: source}, program); err == nil {
		t.Fatal("runtime seed unexpectedly accepted package-lock.json drift")
	}
}

func TestInstallRuntimeSeedsCopiesFrontendEmbedWithoutRuntimeManifest(t *testing.T) {
	source := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(source, "cmd", "agent-terminal"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedRoot := realTempDir(t)
	writeTestFile(t, filepath.Join(seedRoot, "index.html"), "<main>embed</main>\n", 0o600)
	writeTestFile(t, filepath.Join(seedRoot, "assets", "app.js"), "console.log('embed')\n", 0o600)

	config := executorConfig{frontendEmbedSeedRoot: seedRoot}
	program := ExecutorProgram{NeedsFrontendEmbedSeed: true}
	if err := installExecutorSeeds(config, executorLayout{sourceCopy: source}, program); err != nil {
		t.Fatalf("installExecutorSeeds: %v", err)
	}
	installed := filepath.Join(source, "cmd", "agent-terminal", "web-dist", "index.html")
	content, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "<main>embed</main>\n" {
		t.Fatalf("installed index.html = %q", content)
	}
	asset, err := os.ReadFile(filepath.Join(source, "cmd", "agent-terminal", "web-dist", "assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(asset) != "console.log('embed')\n" {
		t.Fatalf("installed asset = %q", asset)
	}
}

func TestInstallRuntimeSeedsRejectsFrontendEmbedWithoutIndex(t *testing.T) {
	source := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(source, "cmd", "agent-terminal"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := executorConfig{frontendEmbedSeedRoot: realTempDir(t)}
	err := installExecutorSeeds(config, executorLayout{sourceCopy: source}, ExecutorProgram{NeedsFrontendEmbedSeed: true})
	if err == nil || !strings.Contains(err.Error(), "frontend embed seed index.html") {
		t.Fatalf("installExecutorSeeds error = %v, want missing frontend embed index.html", err)
	}
}

func TestInstallRuntimeSeedsRejectsExistingFrontendEmbedTarget(t *testing.T) {
	source := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(source, "cmd", "agent-terminal", "web-dist"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedRoot := realTempDir(t)
	writeTestFile(t, filepath.Join(seedRoot, "index.html"), "<main>embed</main>\n", 0o600)

	config := executorConfig{frontendEmbedSeedRoot: seedRoot}
	err := installExecutorSeeds(config, executorLayout{sourceCopy: source}, ExecutorProgram{NeedsFrontendEmbedSeed: true})
	if err == nil || !strings.Contains(err.Error(), "frontend embed seed target already exists") {
		t.Fatalf("installExecutorSeeds error = %v, want existing frontend embed target rejection", err)
	}
}

func TestInstallRuntimeSeedsRejectsEscapingFrontendEmbedSymlink(t *testing.T) {
	source := realTempDir(t)
	if err := os.MkdirAll(filepath.Join(source, "cmd", "agent-terminal"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedRoot := realTempDir(t)
	writeTestFile(t, filepath.Join(seedRoot, "index.html"), "<main>embed</main>\n", 0o600)
	outside := filepath.Join(realTempDir(t), "outside.js")
	writeTestFile(t, outside, "outside\n", 0o600)
	relativeOutside, err := filepath.Rel(seedRoot, outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relativeOutside, filepath.Join(seedRoot, "escape.js")); err != nil {
		t.Fatal(err)
	}

	config := executorConfig{frontendEmbedSeedRoot: seedRoot}
	err = installExecutorSeeds(config, executorLayout{sourceCopy: source}, ExecutorProgram{NeedsFrontendEmbedSeed: true})
	if err == nil || !strings.Contains(err.Error(), "runtime seed symlink escapes seed root") {
		t.Fatalf("installExecutorSeeds error = %v, want escaping symlink rejection", err)
	}
}

func TestRuntimeSeedDigestRejectsEscapingSymlink(t *testing.T) {
	root := realTempDir(t)
	writeTestFile(t, filepath.Join(root, "safe"), "safe", 0o600)
	if err := os.Symlink("../../outside", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := RuntimeSeedTreeDigest(root); err == nil {
		t.Fatal("RuntimeSeedTreeDigest unexpectedly accepted an escaping symlink")
	}
}

func TestRuntimeSeedTreeDigestFramesFileRecordsUnambiguously(t *testing.T) {
	singleFileTree := realTempDir(t)
	doubleFileTree := realTempDir(t)
	payload := []byte("payload")
	legacySecondRecord := append([]byte("F\x00b\x000600\x00"), payload...)
	writeTestFile(t, filepath.Join(singleFileTree, "a"), string(legacySecondRecord), 0o600)
	writeTestFile(t, filepath.Join(doubleFileTree, "a"), "", 0o600)
	writeTestFile(t, filepath.Join(doubleFileTree, "b"), string(payload), 0o600)

	legacySingle := append([]byte("F\x00a\x000600\x00"), legacySecondRecord...)
	legacyDouble := append([]byte("F\x00a\x000600\x00F\x00b\x000600\x00"), payload...)
	if !bytes.Equal(legacySingle, legacyDouble) {
		t.Fatal("test fixture does not reproduce the legacy ambiguous byte stream")
	}
	singleDigest := runtimeSeedDigest(t, singleFileTree)
	doubleDigest := runtimeSeedDigest(t, doubleFileTree)
	if singleDigest == doubleDigest {
		t.Fatal("framed runtime seed digest collided for structurally different trees")
	}
}

func TestRuntimeSeedTreeDigestBindsFileContentAndPermissions(t *testing.T) {
	root := realTempDir(t)
	path := filepath.Join(root, "file")
	writeTestFile(t, path, "first", 0o600)
	baseline := runtimeSeedDigest(t, root)
	writeTestFile(t, path, "other", 0o600)
	if contentDigest := runtimeSeedDigest(t, root); contentDigest == baseline {
		t.Fatal("runtime seed digest did not bind regular file content")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if modeDigest := runtimeSeedDigest(t, root); modeDigest == runtimeSeedDigestWithMode(t, root, path, 0o600) {
		t.Fatal("runtime seed digest did not bind regular file permissions")
	}
}

func TestRuntimeSeedTreeDigestBindsSymlinkTarget(t *testing.T) {
	root := realTempDir(t)
	writeTestFile(t, filepath.Join(root, "first"), "same", 0o600)
	writeTestFile(t, filepath.Join(root, "second"), "same", 0o600)
	link := filepath.Join(root, "link")
	if err := os.Symlink("first", link); err != nil {
		t.Fatal(err)
	}
	baseline := runtimeSeedDigest(t, root)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("second", link); err != nil {
		t.Fatal(err)
	}
	if targetDigest := runtimeSeedDigest(t, root); targetDigest == baseline {
		t.Fatal("runtime seed digest did not bind symlink target")
	}
}

func runtimeSeedDigest(t *testing.T, root string) string {
	t.Helper()
	digest, err := RuntimeSeedTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func runtimeSeedDigestWithMode(t *testing.T, root string, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return runtimeSeedDigest(t, root)
}

func TestRuntimeSeedManifestPublicAPIRoundTrip(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	manifest, err := BuildRuntimeSeedManifest(source, runtimeRoot)
	if err != nil {
		t.Fatalf("BuildRuntimeSeedManifest: %v", err)
	}
	if err := manifest.Validate(source, runtimeRoot); err != nil {
		t.Fatalf("RuntimeSeedManifest.Validate: %v", err)
	}
	var encoded bytes.Buffer
	if err := EncodeRuntimeSeedManifest(&encoded, manifest); err != nil {
		t.Fatalf("EncodeRuntimeSeedManifest: %v", err)
	}
	decoded, err := DecodeRuntimeSeedManifest(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("DecodeRuntimeSeedManifest: %v", err)
	}
	if !reflect.DeepEqual(decoded, manifest) {
		t.Fatalf("decoded manifest = %+v, want %+v", decoded, manifest)
	}
	assertRuntimeSeedManifestFields(t, manifest)
	missingProxy := manifest
	missingProxy.ModuleProxyTreeSHA256 = ""
	if err := EncodeRuntimeSeedManifest(io.Discard, missingProxy); err == nil {
		t.Fatal("EncodeRuntimeSeedManifest unexpectedly accepted a missing module proxy digest")
	}
	missingModuleCache := manifest
	missingModuleCache.GoModCacheTreeSHA256 = ""
	if err := EncodeRuntimeSeedManifest(io.Discard, missingModuleCache); err == nil {
		t.Fatal("EncodeRuntimeSeedManifest unexpectedly accepted a missing Go module cache digest")
	}
	missingNPMCache := manifest
	missingNPMCache.NPMCacheTreeSHA256 = ""
	if err := EncodeRuntimeSeedManifest(io.Discard, missingNPMCache); err == nil {
		t.Fatal("EncodeRuntimeSeedManifest unexpectedly accepted a missing npm cache digest")
	}
}

func TestPrepareExecutorRuntimeSeedsBindsInstalledNPMCacheForPreviousSchema(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	current, err := LoadRuntimeSeedManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	writePreviousRuntimeSeedManifest(t, manifestPath, current)

	prepared, err := prepareExecutorRuntimeSeeds(runtimeRoot, manifestPath, false, true)
	if err != nil {
		t.Fatalf("prepare previous runtime seed schema: %v", err)
	}
	if prepared.manifest.SchemaVersion != RuntimeSeedSchemaVersion ||
		prepared.manifest.NPMCacheTreeSHA256 != current.NPMCacheTreeSHA256 ||
		!prepared.frontendTreeVerified {
		t.Fatalf("prepared previous runtime seed = %+v", prepared)
	}
}

func TestPreviousRuntimeSeedSchemaFailsClosedWithoutInstalledNPMCache(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	current, err := LoadRuntimeSeedManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	writePreviousRuntimeSeedManifest(t, manifestPath, current)
	if err := os.RemoveAll(filepath.Join(runtimeRoot, "frontend", "npm-cache")); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareExecutorRuntimeSeeds(runtimeRoot, manifestPath, false, true); err == nil || !strings.Contains(err.Error(), "legacy frontend npm cache") {
		t.Fatalf("prepare previous runtime seed without npm cache error = %v", err)
	}
}

func TestRuntimeSeedManifestRejectsOlderOrSpoofedPreviousSchema(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := RuntimeSeedManifest{
		SchemaVersion:         runtimeSeedLegacySchemaVersion,
		GoSumSHA256:           digest,
		ModuleProxyLockSHA256: digest,
		ModuleProxyTreeSHA256: digest,
		GoModCacheTreeSHA256:  digest,
		PackageLockSHA256:     digest,
		NodeModulesTreeSHA256: digest,
		RipgrepSHA256:         digest,
		SqruffSHA256:          digest,
	}
	older := manifest
	older.SchemaVersion--
	spoofed := manifest
	spoofed.NPMCacheTreeSHA256 = digest
	for name, candidate := range map[string]RuntimeSeedManifest{"older": older, "spoofed": spoofed} {
		encoded, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeRuntimeSeedManifest(bytes.NewReader(encoded)); err == nil {
			t.Fatalf("DecodeRuntimeSeedManifest unexpectedly accepted %s schema", name)
		}
	}
}

func writePreviousRuntimeSeedManifest(t *testing.T, path string, current RuntimeSeedManifest) {
	t.Helper()
	current.SchemaVersion = runtimeSeedLegacySchemaVersion
	current.NPMCacheTreeSHA256 = ""
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertRuntimeSeedManifestFields(t *testing.T, manifest RuntimeSeedManifest) {
	t.Helper()
	value := reflect.ValueOf(manifest)
	for index := 0; index < value.NumField(); index++ {
		field := value.Type().Field(index)
		if field.Tag.Get("json") == "" {
			t.Errorf("manifest field %s has no JSON consumer tag", field.Name)
		}
		assertRuntimeSeedManifestField(t, field, value.Field(index))
	}
}

func assertRuntimeSeedManifestField(t *testing.T, field reflect.StructField, value reflect.Value) {
	t.Helper()
	switch value.Kind() {
	case reflect.Uint32:
		if value.Uint() == 0 {
			t.Errorf("manifest field %s was not produced", field.Name)
		}
	case reflect.String:
		if !validSHA256Digest(value.String()) {
			t.Errorf("manifest digest field %s was not produced", field.Name)
		}
	default:
		t.Errorf("manifest field %s has unsupported kind %s", field.Name, value.Kind())
	}
}

func TestRuntimeSeedManifestRejectsUnknownFieldsAndSeedTamper(t *testing.T) {
	unknown := strings.NewReader(`{"schema_version":1,"go_sum_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","module_proxy_lock_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","module_proxy_tree_sha256":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","package_lock_sha256":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","node_modules_tree_sha256":"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","ripgrep_sha256":"sha256:1111111111111111111111111111111111111111111111111111111111111111","sqruff_sha256":"sha256:2222222222222222222222222222222222222222222222222222222222222222","unknown":true}`)
	if _, err := DecodeRuntimeSeedManifest(unknown); err == nil {
		t.Fatal("DecodeRuntimeSeedManifest unexpectedly accepted an unknown field")
	}

	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	manifest, err := BuildRuntimeSeedManifest(source, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runtimeRoot, "go-proxy", "tampered"), "tamper\n", 0o600)
	if err := manifest.Validate(source, runtimeRoot); err == nil {
		t.Fatal("RuntimeSeedManifest.Validate unexpectedly accepted seed tamper")
	}
	if err := os.Remove(filepath.Join(runtimeRoot, "bin", "sqruff")); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRuntimeSeedManifest(source, runtimeRoot); err == nil {
		t.Fatal("BuildRuntimeSeedManifest unexpectedly accepted missing sqruff")
	}
}

func TestRuntimeSeedManifestRejectsMissingLockedModuleZip(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	zipPath := filepath.Join(runtimeRoot, "go-proxy", "github.com", "kelindar", "event", "@v", "v1.5.2.zip")
	if err := os.Remove(zipPath); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRuntimeSeedManifest(source, runtimeRoot); err == nil || !strings.Contains(err.Error(), ".zip") {
		t.Fatalf("BuildRuntimeSeedManifest missing zip error = %v", err)
	}
}

func TestRuntimeSeedManifestRejectsGoModuleCacheTamper(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	manifest, err := BuildRuntimeSeedManifest(source, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runtimeRoot, "go-mod-cache", "tampered"), "tamper\n", 0o600)
	if err := manifest.Validate(source, runtimeRoot); err == nil {
		t.Fatal("RuntimeSeedManifest.Validate unexpectedly accepted Go module cache tamper")
	}
}

func TestRuntimeSeedManifestRejectsNPMCacheTamper(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	manifest, err := BuildRuntimeSeedManifest(source, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runtimeRoot, "frontend", "npm-cache", "_cacache", "index-v5", "tampered"), "tamper\n", 0o600)
	if err := manifest.Validate(source, runtimeRoot); err == nil {
		t.Fatal("RuntimeSeedManifest.Validate unexpectedly accepted npm cache tamper")
	}
}

func TestRuntimeSeedDigestRejectsEscapingSymlinkChain(t *testing.T) {
	root := realTempDir(t)
	outside := realTempDir(t)
	writeTestFile(t, filepath.Join(outside, "outside"), "outside", 0o600)
	if err := os.Symlink(filepath.Join("..", filepath.Base(outside), "outside"), filepath.Join(root, "second")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("second", filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	if _, err := RuntimeSeedTreeDigest(root); err == nil {
		t.Fatal("RuntimeSeedTreeDigest unexpectedly accepted an escaping symlink chain")
	}
}

func TestRuntimeSeedInspectOutputsRecomputedManifest(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "fixture sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	want, err := BuildRuntimeSeedManifest(source, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := executeRuntimeSeedInspectCommand([]string{source, runtimeRoot}, &output); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRuntimeSeedManifest(&output)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("runtime seed inspect manifest = %#v, want %#v", got, want)
	}
}

func TestRuntimeSeedManifestDriftFieldsReportsOnlyChangedIdentities(t *testing.T) {
	tracked := RuntimeSeedManifest{SchemaVersion: RuntimeSeedSchemaVersion, GoSumSHA256: "sha256:tracked", NPMCacheTreeSHA256: "sha256:npm"}
	actual := tracked
	actual.GoSumSHA256 = "sha256:actual"
	actual.NPMCacheTreeSHA256 = "sha256:changed"
	if got := runtimeSeedManifestDriftFields(tracked, actual); !slices.Equal(got, []string{"go_sum_sha256", "npm_cache_tree_sha256"}) {
		t.Fatalf("runtime seed drift fields = %v", got)
	}
}

func writeRuntimeSeedFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	proxyLockPath := filepath.Join(source, "build", "gate", "runtime-proxy", "go.sum")
	writeTestFile(t, proxyLockPath, runtimeProxyFixtureSum, 0o600)
	runtimeRoot := realTempDir(t)
	moduleProxyRoot := filepath.Join(runtimeRoot, "go-proxy")
	goModCacheRoot := filepath.Join(runtimeRoot, "go-mod-cache")
	nodeModulesRoot := filepath.Join(runtimeRoot, "frontend", "node_modules")
	npmCacheRoot := filepath.Join(runtimeRoot, "frontend", "npm-cache")
	viteCacheRoot := filepath.Join(runtimeRoot, "frontend", "vite-cache")
	proxyVersionRoot := filepath.Join(moduleProxyRoot, "github.com", "kelindar", "event", "@v")
	writeTestFile(t, filepath.Join(proxyVersionRoot, "list"), "v1.5.2\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.info"), "{}\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.mod"), "module github.com/kelindar/event\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.zip"), "fixture zip\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.ziphash"), strings.Fields(runtimeProxyFixtureSum)[2]+"\n", 0o600)
	moduleCacheVersionRoot := filepath.Join(goModCacheRoot, "cache", "download", "github.com", "kelindar", "event", "@v")
	writeTestFile(t, filepath.Join(moduleCacheVersionRoot, "list"), "v1.5.2\n", 0o444)
	writeTestFile(t, filepath.Join(moduleCacheVersionRoot, "v1.5.2.info"), "{}\n", 0o444)
	writeTestFile(t, filepath.Join(moduleCacheVersionRoot, "v1.5.2.lock"), "", 0o444)
	writeTestFile(t, filepath.Join(moduleCacheVersionRoot, "v1.5.2.mod"), "module github.com/kelindar/event\n", 0o444)
	writeTestFile(t, filepath.Join(moduleCacheVersionRoot, "v1.5.2.zip"), "fixture zip\n", 0o444)
	writeTestFile(t, filepath.Join(moduleCacheVersionRoot, "v1.5.2.ziphash"), strings.Fields(runtimeProxyFixtureSum)[2]+"\n", 0o444)
	writeTestFile(t, filepath.Join(goModCacheRoot, "github.com", "kelindar", "event@v1.5.2", "go.mod"), "module github.com/kelindar/event\n", 0o444)
	writeTestFile(t, filepath.Join(goModCacheRoot, "github.com", "kelindar", "event@v1.5.2", "event.go"), "package event\n", 0o444)
	writeTestFile(t, filepath.Join(nodeModulesRoot, "tool", "index.js"), "export {}\n", 0o600)
	writeTestFile(t, filepath.Join(npmCacheRoot, "_cacache", "content-v2", "sha512", "aa", "fixture"), "fixture package\n", 0o444)
	writeTestFile(t, filepath.Join(npmCacheRoot, "_cacache", "index-v5", "aa", "fixture"), "fixture index\n", 0o444)
	writeTestFile(t, filepath.Join(viteCacheRoot, "deps", "_metadata.json"), "{\"hash\":\"fixture\"}\n", 0o444)
	ripgrepPath := filepath.Join(runtimeRoot, "bin", "rg")
	writeTestFile(t, ripgrepPath, "fixture ripgrep\n", 0o700)
	sqruffPath := filepath.Join(runtimeRoot, "bin", "sqruff")
	writeTestFile(t, sqruffPath, "fixture sqruff\n", 0o700)
	goSumDigest := mustRuntimeSeedFileDigest(t, filepath.Join(source, "go.sum"))
	packageLockDigest := mustRuntimeSeedFileDigest(t, filepath.Join(source, "frontend-app", "package-lock.json"))
	moduleProxyDigest := mustRuntimeSeedTreeDigest(t, moduleProxyRoot)
	goModCacheDigest := mustRuntimeSeedTreeDigest(t, goModCacheRoot)
	nodeModulesDigest := mustRuntimeSeedTreeDigest(t, nodeModulesRoot)
	npmCacheDigest := mustRuntimeSeedTreeDigest(t, npmCacheRoot)
	viteCacheDigest := mustRuntimeSeedTreeDigest(t, viteCacheRoot)
	ripgrepDigest := mustRuntimeSeedFileDigest(t, ripgrepPath)
	sqruffDigest := mustRuntimeSeedFileDigest(t, sqruffPath)
	proxyLockDigest := mustRuntimeSeedFileDigest(t, proxyLockPath)
	manifest := RuntimeSeedManifest{
		SchemaVersion: RuntimeSeedSchemaVersion, GoSumSHA256: goSumDigest,
		ModuleProxyLockSHA256: proxyLockDigest, ModuleProxyTreeSHA256: moduleProxyDigest,
		GoModCacheTreeSHA256: goModCacheDigest,
		PackageLockSHA256:    packageLockDigest, NodeModulesTreeSHA256: nodeModulesDigest,
		NPMCacheTreeSHA256: npmCacheDigest, ViteCacheTreeSHA256: viteCacheDigest,
		RipgrepSHA256: ripgrepDigest, SqruffSHA256: sqruffDigest,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(runtimeRoot, "manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return runtimeRoot, manifestPath
}

func mustRuntimeSeedFileDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustRuntimeSeedTreeDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := RuntimeSeedTreeDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
