package gateclosure

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	testDigest  = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testDigestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testDigestD = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	testDigestE = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func TestRuntimeDepsDockerCommandTimeoutDoesNotWriteLock(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_RUNTIME_DEPS_TIMEOUT_HELPER") == "1" {
		time.Sleep(time.Minute)
		return
	}
	lockPath := filepath.Join(t.TempDir(), "runtime-deps.lock")
	document := validRuntimeDepsLock(runtimeDepsInputs{
		Dockerfile: testDigest, ToolchainLock: testDigest, GoMod: testDigest, GoSum: testDigest,
		NilnessRunner: testDigest, NilnessGuard: testDigest, FrontendPackageLock: testDigest,
		LSPPackageLock: testDigest, ProxyGoMod: testDigest, ProxyGoSum: testDigest,
		ToolsGoMod: testDigest, ToolsGoSum: testDigest, ManifestBuilder: testDigest, ManifestAPI: testDigest,
	})
	prerequisite := func() error {
		_, err := commandOutputWithTimeout(20*time.Millisecond, "", []string{
			"SUPER_DOLPHIN_RUNTIME_DEPS_TIMEOUT_HELPER=1",
		}, os.Args[0], "-test.run=^TestRuntimeDepsDockerCommandTimeoutDoesNotWriteLock$")
		return err
	}
	err := persistRuntimeDepsLock(lockPath, document, prerequisite)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("timeout error = %v", err)
	}
	if _, statErr := os.Stat(lockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("lock exists after timed-out prerequisite: %v", statErr)
	}
}

func TestPublishRuntimeDepsIndexUsesDockerCredentialsAndVerifiesAnonymousReadback(t *testing.T) {
	amd64Digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	arm64Digest := "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	source := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + amd64Digest + `","size":1,"platform":{"os":"linux","architecture":"amd64"}},{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + arm64Digest + `","size":1,"platform":{"os":"linux","architecture":"arm64"}}]}`)
	sourceDigest := digestBytes(source)
	for _, test := range []struct {
		name       string
		sourceType string
		sourceHead string
		wantError  bool
	}{
		{name: "canonical", sourceType: ociIndexMediaType, sourceHead: sourceDigest},
		{name: "wrong media type", sourceType: ociManifestMediaType, sourceHead: sourceDigest, wantError: true},
		{name: "wrong digest header", sourceType: ociManifestMediaType, sourceHead: testDigest, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var locked []byte
			var commands [][]string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch {
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/refresh-test"):
					writer.Header().Set("Content-Type", test.sourceType)
					writer.Header().Set("Docker-Content-Digest", test.sourceHead)
					_, _ = writer.Write(source)
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/locked-test"):
					writer.Header().Set("Content-Type", ociIndexMediaType)
					writer.Header().Set("Docker-Content-Digest", digestBytes(locked))
					_, _ = writer.Write(locked)
				default:
					t.Errorf("unexpected registry request %s %s", request.Method, request.URL.Path)
					writer.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			repository := strings.TrimPrefix(server.URL, "http://") + "/super-dolphin/runtime-deps"
			testRegistry := testRuntimeDepsRegistry(t, server)
			originalFactory := runtimeDepsRegistryFactory
			runtimeDepsRegistryFactory = func(string) (runtimeDepsRegistry, error) { return testRegistry, nil }
			t.Cleanup(func() { runtimeDepsRegistryFactory = originalFactory })
			original := runtimeDepsRunCommand
			runtimeDepsRunCommand = func(_ time.Duration, name string, arguments ...string) error {
				commands = append(commands, append([]string{name}, arguments...))
				locked = append([]byte(nil), source...)
				return nil
			}
			t.Cleanup(func() { runtimeDepsRunCommand = original })
			err := publishRuntimeDepsIndex(repository, "locked-test", "refresh-test", runtimeDepsPlatforms)
			if test.wantError {
				if err == nil || len(commands) != 0 {
					t.Fatalf("unsafe source publication error = %v, commands = %d", err, len(commands))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"docker", "buildx", "imagetools", "create", "--tag", repository + ":locked-test", repository + ":refresh-test"}
			if len(commands) != 1 || !slices.Equal(commands[0], want) {
				t.Fatalf("Docker publication command = %#v, want %#v", commands, want)
			}
		})
	}
}

func TestInspectRuntimeDepsImagesReadsMutableIndexReferenceOnce(t *testing.T) {
	configurations := map[string][]byte{
		"linux/amd64": []byte(`{"architecture":"amd64","os":"linux","rootfs":{"type":"layers","diff_ids":["` + testDigest + `"]}}`),
		"linux/arm64": []byte(`{"architecture":"arm64","os":"linux","rootfs":{"type":"layers","diff_ids":["` + testDigestB + `"]}}`),
	}
	manifests := make(map[string][]byte, len(configurations))
	manifestDigests := make(map[string]string, len(configurations))
	for platform, configuration := range configurations {
		configDigest := digestBytes(configuration)
		manifest := []byte(`{"schemaVersion":2,"config":{"digest":"` + configDigest + `","size":` + strconv.FormatInt(int64(len(configuration)), 10) + `},"layers":[{"digest":"` + testDigestC + `","size":1}]}`)
		manifestDigest := digestBytes(manifest)
		manifests[manifestDigest] = manifest
		manifestDigests[platform] = manifestDigest
	}
	index := []byte(`{"schemaVersion":2,"mediaType":"` + ociIndexMediaType + `","manifests":[` +
		`{"mediaType":"` + ociManifestMediaType + `","digest":"` + manifestDigests["linux/amd64"] + `","size":1,"platform":{"os":"linux","architecture":"amd64"}},` +
		`{"mediaType":"` + ociManifestMediaType + `","digest":"` + manifestDigests["linux/arm64"] + `","size":1,"platform":{"os":"linux","architecture":"arm64"}}]}`)
	configByDigest := make(map[string][]byte, len(configurations))
	for _, configuration := range configurations {
		configByDigest[digestBytes(configuration)] = configuration
	}

	indexReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/manifests/locked"):
			indexReads++
			if indexReads > 1 {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writeRuntimeDepsRegistryDocument(t, writer, ociIndexMediaType, index)
		case strings.Contains(request.URL.Path, "/manifests/"):
			digest := path.Base(request.URL.Path)
			manifest, exists := manifests[digest]
			if !exists {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			writeRuntimeDepsRegistryDocument(t, writer, ociManifestMediaType, manifest)
		case strings.Contains(request.URL.Path, "/blobs/"):
			digest := path.Base(request.URL.Path)
			configuration, exists := configByDigest[digest]
			if !exists {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = writer.Write(configuration)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	originalFactory := runtimeDepsRegistryFactory
	runtimeDepsRegistryFactory = func(string) (runtimeDepsRegistry, error) { return testRuntimeDepsRegistry(t, server), nil }
	t.Cleanup(func() { runtimeDepsRegistryFactory = originalFactory })
	repository := strings.TrimPrefix(server.URL, "http://") + "/super-dolphin/runtime-deps"
	images, err := inspectRuntimeDepsImages(repository, "locked", runtimeDepsPlatforms)
	if err != nil {
		t.Fatal(err)
	}
	if indexReads != 1 || len(images) != len(runtimeDepsPlatforms) {
		t.Fatalf("index reads = %d, images = %d", indexReads, len(images))
	}
}

func writeRuntimeDepsRegistryDocument(t *testing.T, writer http.ResponseWriter, mediaType string, data []byte) {
	t.Helper()
	writer.Header().Set("Content-Type", mediaType)
	writer.Header().Set("Docker-Content-Digest", digestBytes(data))
	if _, err := writer.Write(data); err != nil {
		t.Error(err)
	}
}

func TestRuntimeDepsRegistryRejectsPathAndReferenceInjection(t *testing.T) {
	for _, repository := range []string{
		"registry.example", "registry.example/", "registry.example/repo/../escape",
		"registry.example/repo?tag=value", "registry.example/repo#fragment",
	} {
		if _, err := newRuntimeDepsRegistry(repository); err == nil {
			t.Fatalf("unsafe repository %q unexpectedly passed", repository)
		}
	}
	registry, err := newRuntimeDepsRegistry("registry.example/super-dolphin/runtime-deps")
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"", "../escape", "tag/value", "tag?query", "tag#fragment", "tag@digest"} {
		if _, err := registry.manifestURL(reference); err == nil {
			t.Fatalf("unsafe reference %q unexpectedly passed", reference)
		}
	}
}

func TestRuntimeDepsRemoteRegistryAllowsOnlyCanonicalGHCR(t *testing.T) {
	for _, repository := range []string{"ghcr.io/super-dolphin/runtime-deps"} {
		if err := validateRuntimeDepsRemoteRegistry(repository); err != nil {
			t.Fatalf("canonical GHCR repository %q: %v", repository, err)
		}
	}
	for _, repository := range []string{
		"runtime-deps.corp.internal/super-dolphin/runtime-deps",
		"registry.example.com/super-dolphin/runtime-deps",
		"ghcr.io:5000/super-dolphin/runtime-deps",
		"ghcr.io:443/super-dolphin/runtime-deps",
		"GHCR.io/super-dolphin/runtime-deps",
		"ghcr.io./super-dolphin/runtime-deps",
	} {
		if err := validateRuntimeDepsRemoteRegistry(repository); err == nil {
			t.Fatalf("non-canonical registry repository %q unexpectedly passed", repository)
		}
	}
}

func TestRuntimeDepsRegistryPrerequisiteIsFailFastAndActionable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v2/" {
			t.Errorf("registry probe path = %q", request.URL.Path)
		}
		writer.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registry := testRuntimeDepsRegistry(t, server)
	if err := validateRuntimeDepsRegistryClient(registry); err != nil {
		t.Fatalf("valid registry prerequisite: %v", err)
	}
	server.Close()
	err := validateRuntimeDepsRegistryClient(registry)
	if err == nil || !strings.Contains(err.Error(), "refresh-dependencies") || !strings.Contains(err.Error(), "curl -fsS") {
		t.Fatalf("unavailable registry error = %v", err)
	}
}

func TestRuntimeDepsRegistryPrerequisiteRejectsNonRegistryEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := validateRuntimeDepsRegistryClient(testRuntimeDepsRegistry(t, server)); err == nil || !strings.Contains(err.Error(), "Registry v2 identity") {
		t.Fatalf("non-registry endpoint error = %v", err)
	}
}

func TestRuntimeDepsRegistryPrerequisiteRejectsInvalidBearerChallenge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		writer.Header().Set("WWW-Authenticate", `Bearer realm="https://auth.example/token"`)
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	if err := validateRuntimeDepsRegistryClient(testRuntimeDepsRegistry(t, server)); err == nil || !strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("authenticated registry error = %v", err)
	}
}

func TestRuntimeDepsLockRejectsInputAndShapeDrift(t *testing.T) {
	root := t.TempDir()
	writeRuntimeDepsInputs(t, root)
	inputs, err := digestRuntimeDepsInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	lock := validRuntimeDepsLock(inputs)
	toolchain := toolchainLock{NetworkPolicy: "none", TargetPlatforms: runtimeDepsPlatforms}
	if err := lock.validateAgainstSource(root, toolchain); err != nil {
		t.Fatalf("valid lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.validateAgainstSource(root, toolchain); !errors.Is(err, errRuntimeDepsInputsDrift) {
		t.Fatalf("input drift error = %v, want errRuntimeDepsInputsDrift", err)
	}
	writeRuntimeDepsInputs(t, root)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.validateAgainstSource(root, toolchain); !errors.Is(err, errRuntimeDepsInputsDrift) {
		t.Fatalf("go.mod dependency graph drift error = %v, want errRuntimeDepsInputsDrift", err)
	}
	writeRuntimeDepsInputs(t, root)
	if err := os.WriteFile(filepath.Join(root, "internal/devtools/nilnessrunner/runner.go"), []byte("drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.validateAgainstSource(root, toolchain); !errors.Is(err, errRuntimeDepsInputsDrift) {
		t.Fatalf("nilness vendor closure drift error = %v, want errRuntimeDepsInputsDrift", err)
	}
	lock = validRuntimeDepsLock(inputs)
	lock.Images[0].Image.PlatformManifestDigest = "latest"
	if err := lock.validateShape(); err == nil {
		t.Fatal("mutable platform identity unexpectedly passed")
	}
	lock = validRuntimeDepsLock(inputs)
	lock.RegistryPullPolicy = "authenticated"
	if err := lock.validateShape(); err == nil {
		t.Fatal("authenticated runtime dependency pull policy unexpectedly passed")
	}
	lock = validRuntimeDepsLock(inputs)
	lock.Images[0].Image.Registry = "127.0.0.1:5000/super-dolphin/runtime-deps"
	if err := lock.validateShape(); err == nil {
		t.Fatal("loopback runtime dependency registry unexpectedly passed")
	}
	lock = validRuntimeDepsLock(inputs)
	lock.Paths.SQLC = "/usr/local/bin/sqlc"
	if err := lock.validateShape(); err == nil {
		t.Fatal("runtime path drift unexpectedly passed")
	}
}

func TestRuntimeDepsLockRejectsCrossPlatformIdentityTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*runtimeDepsLock)
	}{
		{name: "repository mismatch", mutate: func(lock *runtimeDepsLock) { lock.Images[1].Image.Registry = "registry.example/other" }},
		{name: "index mismatch", mutate: func(lock *runtimeDepsLock) { lock.Images[1].Image.OCIIndexDigest = testDigestE }},
		{name: "duplicate manifest", mutate: func(lock *runtimeDepsLock) {
			lock.Images[1].Image.PlatformManifestDigest = lock.Images[0].Image.PlatformManifestDigest
		}},
		{name: "duplicate config", mutate: func(lock *runtimeDepsLock) { lock.Images[1].Image.ConfigDigest = lock.Images[0].Image.ConfigDigest }},
	} {
		t.Run(test.name, func(t *testing.T) {
			lock := validRuntimeDepsLock(runtimeDepsInputs{
				Dockerfile: testDigest, ToolchainLock: testDigest, GoMod: testDigest, GoSum: testDigest,
				NilnessRunner: testDigest, NilnessGuard: testDigest, FrontendPackageLock: testDigest,
				LSPPackageLock: testDigest, ProxyGoMod: testDigest, ProxyGoSum: testDigest,
				ToolsGoMod: testDigest, ToolsGoSum: testDigest, ManifestBuilder: testDigest, ManifestAPI: testDigest,
			})
			test.mutate(&lock)
			if err := lock.validateShape(); err == nil {
				t.Fatal("tampered cross-platform runtime dependency identity unexpectedly passed")
			}
		})
	}
}

func TestRuntimeDepsLockStrictDecodeRejectsUnknownField(t *testing.T) {
	root := t.TempDir()
	writeRuntimeDepsInputs(t, root)
	inputs, err := digestRuntimeDepsInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeRuntimeDepsLock(validRuntimeDepsLock(inputs))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("\n}"), []byte(",\n  \"unexpected\": true\n}"), 1)
	path := filepath.Join(root, "runtime-deps.lock")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRuntimeDepsLock(path); err == nil {
		t.Fatal("unknown lock field unexpectedly passed")
	}
}

func TestReuseRuntimeDepsPublicationOnlyMigratesMissingOrLegacyV2Lock(t *testing.T) {
	for _, test := range []struct {
		name      string
		prepare   func(*testing.T, string)
		wantError bool
	}{
		{name: "missing"},
		{name: "legacy v2", prepare: func(t *testing.T, lockPath string) {
			if err := os.WriteFile(lockPath, []byte(`{"schema_version":"2","image":{}}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "malformed", wantError: true, prepare: func(t *testing.T, lockPath string) {
			if err := os.WriteFile(lockPath, []byte(`{"schema_version":"2"`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsupported schema", wantError: true, prepare: func(t *testing.T, lockPath string) {
			if err := os.WriteFile(lockPath, []byte(`{"schema_version":"1"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "io error", wantError: true, prepare: func(t *testing.T, lockPath string) {
			if err := os.Mkdir(lockPath, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			sourceRoot := t.TempDir()
			lockPath := filepath.Join(sourceRoot, filepath.FromSlash(gateRuntimeDepsLock))
			if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if test.prepare != nil {
				test.prepare(t, lockPath)
			}
			reused, err := reuseRuntimeDepsPublication(root, sourceRoot, "registry.example/runtime-deps", "locked-tree", toolchainLock{}, "tree")
			if reused {
				t.Fatal("unverified runtime dependency publication was reused")
			}
			if test.wantError && (err == nil || !strings.Contains(err.Error(), "read staged runtime dependency lock for reuse")) {
				t.Fatalf("read failure = %v", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("explicit migration returned error: %v", err)
			}
		})
	}
}

func TestTruthDockerfileUsesOnlyImmutableOfflineRuntime(t *testing.T) {
	lock := toolchainLock{SourceDateEpoch: "0", NetworkPolicy: "none"}
	runtimeLock := validRuntimeDepsLock(runtimeDepsInputs{
		Dockerfile: testDigest, ToolchainLock: testDigest, GoMod: testDigest, GoSum: testDigest,
		NilnessRunner: testDigest, NilnessGuard: testDigest, FrontendPackageLock: testDigest,
		LSPPackageLock: testDigest, ProxyGoMod: testDigest, ProxyGoSum: testDigest,
		ToolsGoMod: testDigest, ToolsGoSum: testDigest,
		ManifestBuilder: testDigest, ManifestAPI: testDigest,
	})
	data, err := renderDockerfile(lock, runtimeLock, []string{
		"build/gate/closure/closure.go", "build/gate/closure/runtime_deps.go",
		"cmd/super-dolphin-gate/main.go", gateRuntimeProxyModule, gateRuntimeProxySum,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"ARG RUNTIME_DEPS_IMAGE\n",
		"/usr/local/bin/super-dolphin-runtime-seed verify",
		"/usr/local/bin/super-dolphin-gate-executor",
		"cp -a /opt/super-dolphin-gate/runtime/vendor ./vendor",
		"go build -mod=vendor -trimpath -buildvcs=false -o /tmp/nilness-guard ./scripts/nilness_guard.go",
		`COPY ["build/gate/runtime-proxy/go.mod","build/gate/runtime-proxy/go.sum","./build/gate/runtime-proxy/"]`,
		"build/gate/closure/closure.go",
		"build/gate/closure/runtime_deps.go",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("truth Dockerfile missing %q", required)
		}
	}
	if strings.Contains(text, ":latest") || strings.Contains(text, "runtime-node.tar") || strings.Contains(text, "runtime-tools.tar") {
		t.Fatal("truth Dockerfile contains mutable or archived dependency fallback")
	}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "RUN ") && !strings.HasPrefix(line, "RUN --network=none ") {
			t.Fatalf("truth Dockerfile has network-capable RUN: %s", line)
		}
	}
}

func TestManifestTracksClosureVerifierWithoutLegacyGeneratorPaths(t *testing.T) {
	data, err := renderManifest([]string{
		"build/gate/closure/closure.go", "build/gate/closure/runtime_deps.go", "build/gate/closure/runtime_deps_test.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest inputManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"build/gate/closure/closure.go", "build/gate/closure/runtime_deps.go", "build/gate/closure/runtime_deps_test.go",
	} {
		if !slices.Contains(manifest.Inputs, required) {
			t.Fatalf("manifest omitted closure verifier input %q", required)
		}
	}
	for _, legacy := range []string{
		"build/gate/cmd/generate-closure/runtime_deps.go", "build/gate/cmd/generate-closure/runtime_deps_test.go",
	} {
		if slices.Contains(manifest.Inputs, legacy) {
			t.Fatalf("manifest retained legacy closure input %q", legacy)
		}
	}
}

func TestRuntimeDepsVendorIncludesNilnessAnalyzerClosure(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "runtime-deps.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"go mod vendor -o /out/vendor",
		"/out/vendor/golang.org/x/tools/go/analysis/multichecker/multichecker.go",
		"/out/vendor/golang.org/x/tools/go/analysis/passes/nilness/nilness.go",
		"GOPROXY=off GOSUMDB=off go build -mod=vendor -trimpath -buildvcs=false -o /tmp/nilness-guard ./scripts/nilness_guard.go",
	} {
		if !bytes.Contains(dockerfile, []byte(required)) {
			t.Fatalf("runtime dependency vendor closure is missing %q", required)
		}
	}
}

func TestRuntimeDepsProvidesPsForProcessTreeAssertions(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "runtime-deps.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"apt-get install -y --no-install-recommends",
		"Acquire::Retries \"10\"",
		"Acquire::http::Pipeline-Depth \"0\"",
		"https://deb.debian.org",
		"if [ \"$attempts\" -ge 5 ]",
		"retry_command sh -c 'apt-get update && apt-get install -y --no-install-recommends",
		"pkg-config procps xauth xvfb'",
		"test -x /usr/bin/ps",
	} {
		if !bytes.Contains(dockerfile, []byte(required)) {
			t.Fatalf("runtime dependency image is missing process inspection contract %q", required)
		}
	}
}

func TestRuntimeDepsProvidesHeadlessDesktopDisplay(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "runtime-deps.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"procps xauth xvfb'",
		"test -x /usr/bin/Xvfb",
		"test -x /usr/bin/xauth",
		"test -x /usr/bin/xvfb-run",
		"USER 65532:65532\nRUN --network=none xvfb-run -a sh -ec 'test -n \"$DISPLAY\"'",
	} {
		if !bytes.Contains(dockerfile, []byte(required)) {
			t.Fatalf("runtime dependency image is missing headless desktop contract %q", required)
		}
	}
}

func TestRuntimeDepsManifestCopyDoesNotInvalidateSystemDependencyLayer(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "runtime-deps.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	installLayer := bytes.Index(dockerfile, []byte("RUN set -eu;"))
	manifestCopy := bytes.Index(dockerfile, []byte("COPY --from=manifest-builder /runtime/manifest.json"))
	nonRootUser := bytes.Index(dockerfile, []byte("USER 65532:65532"))
	if installLayer < 0 || manifestCopy < 0 || nonRootUser < 0 {
		t.Fatalf("runtime dependency layer markers are missing: install=%d manifest=%d user=%d", installLayer, manifestCopy, nonRootUser)
	}
	if manifestCopy < installLayer || manifestCopy > nonRootUser {
		t.Fatalf("tree-specific manifest COPY must follow the system dependency layer and precede the non-root runtime: install=%d manifest=%d user=%d", installLayer, manifestCopy, nonRootUser)
	}
}

func TestRuntimeDepsRepositoryVendorRunsOnceOnBuildPlatform(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "runtime-deps.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dockerfile, []byte("FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS repository-vendor")) {
		t.Fatal("architecture-independent repository vendor stage must run once on the build platform")
	}
}

func TestSqruffArtifactsRequireCanonicalReleaseAndDigest(t *testing.T) {
	artifacts := []sqruffArtifact{
		{Platform: "linux/amd64", URL: "https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-x86_64-musl.tar.gz", SHA256: "d96a06daca2a214eb0b6c07b2821e9cdb1379086041bcca6f8bab031b6eb8026"},
		{Platform: "linux/arm64", URL: "https://github.com/quarylabs/sqruff/releases/download/v0.38.0/sqruff-linux-aarch64-musl.tar.gz", SHA256: "7e1abca59aeb3a0899a78be36dbfd4002db2ce6754835250beeea2fab95f5abf"},
	}
	if err := validateSqruffArtifacts(artifacts); err != nil {
		t.Fatalf("canonical sqruff artifacts: %v", err)
	}
	artifacts[0].SHA256 = strings.ToUpper(artifacts[0].SHA256)
	if err := validateSqruffArtifacts(artifacts); err == nil {
		t.Fatal("non-canonical Sqruff artifact digest unexpectedly passed")
	}
}

func TestRuntimeSeedHelperUsesCanonicalGateAPIs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "cmd", "runtime-seed-manifest", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{"gate.BuildRuntimeSeedManifest", "gate.EncodeRuntimeSeedManifest", "gate.RuntimeSeedTreeDigest"} {
		if !bytes.Contains(data, []byte(symbol)) {
			t.Fatalf("runtime seed helper does not use %s", symbol)
		}
	}
}

func validRuntimeDepsLock(inputs runtimeDepsInputs) runtimeDepsLock {
	return runtimeDepsLock{
		SchemaVersion: runtimeDepsSchemaVersion, RegistryPullPolicy: runtimeDepsPullPolicy,
		Images: []runtimeDepsImage{
			{Platform: "linux/amd64", Image: gatecontract.ImageIdentity{Registry: "ghcr.io/super-dolphin/runtime-deps", OCIIndexDigest: testDigest, PlatformManifestDigest: testDigestB, ConfigDigest: testDigestC, RootFSDiffIDs: []string{testDigest}, OS: "linux", Architecture: "amd64"}, ImageSize: 1},
			{Platform: "linux/arm64", Image: gatecontract.ImageIdentity{Registry: "ghcr.io/super-dolphin/runtime-deps", OCIIndexDigest: testDigest, PlatformManifestDigest: testDigestD, ConfigDigest: testDigestE, RootFSDiffIDs: []string{testDigest}, OS: "linux", Architecture: "arm64"}, ImageSize: 1},
		},
		Inputs: inputs, Paths: canonicalRuntimeDepsPaths(),
	}
}

func writeRuntimeDepsInputs(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{
		gateRuntimeDepsDocker, "go.mod", "go.sum", "frontend-app/package-lock.json",
		gateToolchain,
		"internal/devtools/nilnessrunner/runner.go", "scripts/nilness_guard.go",
		gateRuntimeLSPLock, gateRuntimeProxyModule, gateRuntimeProxySum, gateRuntimeToolsModule, gateRuntimeToolsSum,
		"build/gate/cmd/runtime-seed-manifest/main.go", "internal/devtools/gate/executor_seed.go",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
