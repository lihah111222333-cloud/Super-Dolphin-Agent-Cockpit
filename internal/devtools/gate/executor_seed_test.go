package gate

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const runtimeProxyFixtureSum = "github.com/kelindar/event v1.5.2 h1:qtgssZqMh/QQMCIxlbx4wU3DoMHOrJXKdiZhphJ4YbY=\n"

func TestInstallRuntimeSeedsBindsLocksAndPreventsOverwrite(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, manifestPath := writeRuntimeSeedFixture(t, source)
	config := executorConfig{runtimeSeedRoot: runtimeRoot, runtimeSeedManifest: manifestPath}
	npmCache := filepath.Join(realTempDir(t), "npm-cache")
	if err := os.Mkdir(npmCache, 0o700); err != nil {
		t.Fatal(err)
	}
	layout := executorLayout{sourceCopy: source, npmCache: npmCache}
	program := ExecutorProgram{NeedsGoSeed: true, NeedsFrontendSeed: true}
	if err := installRuntimeSeeds(config, layout, program); err != nil {
		t.Fatalf("installRuntimeSeeds: %v", err)
	}
	for _, path := range []string{
		filepath.Join(source, "vendor", "modules.txt"),
		filepath.Join(source, "frontend-app", "node_modules", "tool", "index.js"),
		filepath.Join(npmCache, "_cacache", "content-v2", "sha512", "fixture"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("installed seed path %s: %v", path, err)
		}
	}
	if err := installRuntimeSeeds(config, layout, program); err == nil {
		t.Fatal("runtime seed unexpectedly overwrote an existing target")
	}
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
	missingCache := manifest
	missingCache.NPMCacheTreeSHA256 = ""
	if err := EncodeRuntimeSeedManifest(io.Discard, missingCache); err == nil {
		t.Fatal("EncodeRuntimeSeedManifest unexpectedly accepted a missing npm cache digest")
	}
}

func TestInstallPreparedRuntimeSeedCopiesCacheAndRejectsInvalidState(t *testing.T) {
	seedRoot := realTempDir(t)
	seedFile := filepath.Join(seedRoot, "_cacache", "content-v2", "sha512", "fixture")
	writeTestFile(t, seedFile, "cached package\n", 0o600)
	digest := mustRuntimeSeedTreeDigest(t, seedRoot)
	targetParent := realTempDir(t)
	targetRoot := filepath.Join(targetParent, "npm-cache")
	if err := os.Mkdir(targetRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := installPreparedRuntimeSeed(seedRoot, digest, targetRoot); err != nil {
		t.Fatalf("installPreparedRuntimeSeed: %v", err)
	}
	installed := filepath.Join(targetRoot, "_cacache", "content-v2", "sha512", "fixture")
	content, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "cached package\n" {
		t.Fatalf("installed cache content = %q", content)
	}
	if err := installPreparedRuntimeSeed(seedRoot, digest, targetRoot); err == nil {
		t.Fatal("installPreparedRuntimeSeed unexpectedly accepted a non-empty target")
	}
	tamperedTarget := filepath.Join(targetParent, "tampered-cache")
	if err := os.Mkdir(tamperedTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, seedFile, "tampered package\n", 0o600)
	if err := installPreparedRuntimeSeed(seedRoot, digest, tamperedTarget); err == nil {
		t.Fatal("installPreparedRuntimeSeed unexpectedly accepted a tampered seed")
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
	unknown := strings.NewReader(`{"schema_version":1,"go_sum_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","vendor_tree_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","package_lock_sha256":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","node_modules_tree_sha256":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","unknown":true}`)
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
	writeTestFile(t, filepath.Join(runtimeRoot, "vendor", "tampered"), "tamper\n", 0o600)
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

func TestRuntimeSeedManifestRejectsNPMCacheTamper(t *testing.T) {
	source := realTempDir(t)
	writeTestFile(t, filepath.Join(source, "go.sum"), "module sum\n", 0o600)
	writeTestFile(t, filepath.Join(source, "frontend-app", "package-lock.json"), "{\"lockfileVersion\":3}\n", 0o600)
	runtimeRoot, _ := writeRuntimeSeedFixture(t, source)
	manifest, err := BuildRuntimeSeedManifest(source, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(runtimeRoot, "frontend", "npm-cache", "tampered"), "tamper\n", 0o600)
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

func writeRuntimeSeedFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	proxyLockPath := filepath.Join(source, "build", "gate", "runtime-proxy", "go.sum")
	writeTestFile(t, proxyLockPath, runtimeProxyFixtureSum, 0o600)
	runtimeRoot := realTempDir(t)
	vendorRoot := filepath.Join(runtimeRoot, "vendor")
	moduleProxyRoot := filepath.Join(runtimeRoot, "go-proxy")
	nodeModulesRoot := filepath.Join(runtimeRoot, "frontend", "node_modules")
	npmCacheRoot := filepath.Join(runtimeRoot, "frontend", "npm-cache")
	writeTestFile(t, filepath.Join(vendorRoot, "modules.txt"), "# modules\n", 0o600)
	proxyVersionRoot := filepath.Join(moduleProxyRoot, "github.com", "kelindar", "event", "@v")
	writeTestFile(t, filepath.Join(proxyVersionRoot, "list"), "v1.5.2\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.info"), "{}\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.mod"), "module github.com/kelindar/event\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.zip"), "fixture zip\n", 0o600)
	writeTestFile(t, filepath.Join(proxyVersionRoot, "v1.5.2.ziphash"), strings.Fields(runtimeProxyFixtureSum)[2]+"\n", 0o600)
	writeTestFile(t, filepath.Join(nodeModulesRoot, "tool", "index.js"), "export {}\n", 0o600)
	writeTestFile(t, filepath.Join(npmCacheRoot, "_cacache", "content-v2", "sha512", "fixture"), "cached package\n", 0o600)
	ripgrepPath := filepath.Join(runtimeRoot, "bin", "rg")
	writeTestFile(t, ripgrepPath, "fixture ripgrep\n", 0o700)
	sqruffPath := filepath.Join(runtimeRoot, "bin", "sqruff")
	writeTestFile(t, sqruffPath, "fixture sqruff\n", 0o700)
	goSumDigest := mustRuntimeSeedFileDigest(t, filepath.Join(source, "go.sum"))
	packageLockDigest := mustRuntimeSeedFileDigest(t, filepath.Join(source, "frontend-app", "package-lock.json"))
	vendorDigest := mustRuntimeSeedTreeDigest(t, vendorRoot)
	moduleProxyDigest := mustRuntimeSeedTreeDigest(t, moduleProxyRoot)
	nodeModulesDigest := mustRuntimeSeedTreeDigest(t, nodeModulesRoot)
	npmCacheDigest := mustRuntimeSeedTreeDigest(t, npmCacheRoot)
	ripgrepDigest := mustRuntimeSeedFileDigest(t, ripgrepPath)
	sqruffDigest := mustRuntimeSeedFileDigest(t, sqruffPath)
	proxyLockDigest := mustRuntimeSeedFileDigest(t, proxyLockPath)
	manifest := RuntimeSeedManifest{
		SchemaVersion: RuntimeSeedSchemaVersion, GoSumSHA256: goSumDigest, VendorTreeSHA256: vendorDigest,
		ModuleProxyLockSHA256: proxyLockDigest, ModuleProxyTreeSHA256: moduleProxyDigest,
		PackageLockSHA256: packageLockDigest, NodeModulesTreeSHA256: nodeModulesDigest, NPMCacheTreeSHA256: npmCacheDigest,
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
