package gateclosure

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDepsLockRejectsInputAndShapeDrift(t *testing.T) {
	root := t.TempDir()
	writeRuntimeDepsInputs(t, root)
	inputs, err := digestRuntimeDepsInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	lock := validRuntimeDepsLock(inputs)
	if err := lock.validateAgainstSource(root, toolchainLock{NetworkPolicy: "none"}); err != nil {
		t.Fatalf("valid lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.validateAgainstSource(root, toolchainLock{NetworkPolicy: "none"}); !errors.Is(err, errRuntimeDepsInputsDrift) {
		t.Fatalf("input drift error = %v", err)
	}
	lock = validRuntimeDepsLock(inputs)
	lock.BuildMode = "registry"
	if err := lock.validateShape(); err == nil {
		t.Fatal("registry build mode unexpectedly passed")
	}
	lock = validRuntimeDepsLock(inputs)
	lock.CacheScope = "shared"
	if err := lock.validateShape(); err == nil {
		t.Fatal("shared cache scope unexpectedly passed")
	}
	lock = validRuntimeDepsLock(inputs)
	lock.Paths.SQLC = "/usr/local/bin/sqlc"
	if err := lock.validateShape(); err == nil {
		t.Fatal("runtime path drift unexpectedly passed")
	}
}

func TestRuntimeDepsLockStrictDecodeRejectsRegistryFields(t *testing.T) {
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
	data = bytes.Replace(data, []byte("\n}"), []byte(",\n  \"registry_pull_policy\": \"anonymous\"\n}"), 1)
	path := filepath.Join(root, "runtime-deps.lock")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRuntimeDepsLock(path); err == nil {
		t.Fatal("legacy registry field unexpectedly passed")
	}
}

func TestRefreshDependencyClosureRejectsInvalidTree(t *testing.T) {
	for _, tree := range []string{"", "HEAD next"} {
		if err := RefreshDependencyClosure(tree); err == nil || !strings.Contains(err.Error(), "tree is required") {
			t.Fatalf("tree %q error = %v", tree, err)
		}
	}
}

func TestRuntimeDepsLockEncodingContainsOnlyNodeLocalContract(t *testing.T) {
	inputs := runtimeDepsInputs{Dockerfile: testDigest, ToolchainLock: testDigest, GoMod: testDigest, GoSum: testDigest, NilnessRunner: testDigest, NilnessGuard: testDigest, FrontendPackageLock: testDigest, LSPPackageLock: testDigest, ProxyGoMod: testDigest, ProxyGoSum: testDigest, ToolsGoMod: testDigest, ToolsGoSum: testDigest, ManifestBuilder: testDigest, ManifestAPI: testDigest}
	data, err := encodeRuntimeDepsLock(validRuntimeDepsLock(inputs))
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"registry_pull_policy", "images", "oci_index_digest", "platform_manifest_digest"} {
		if bytes.Contains(data, []byte(unwanted)) {
			t.Fatalf("lock retains %q", unwanted)
		}
	}
}

func TestRuntimeDepsDockerfileDoesNotVendorJobSource(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(testRepositoryRoot(t), gateRuntimeDepsDocker))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)
	for _, wanted := range []string{
		"COPY go.mod go.sum ./",
		"COPY third_party/kelindar-event/ ./third_party/kelindar-event/",
		"COPY build/gate/runtime-proxy/go.mod build/gate/runtime-proxy/go.sum ./build/gate/runtime-proxy/",
		"go mod download all",
		"GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off",
		"go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-runtime-seed",
	} {
		if !strings.Contains(dockerfile, wanted) {
			t.Fatalf("runtime dependencies Dockerfile is missing %q", wanted)
		}
	}
	for _, unwanted := range []string{
		"go mod vendor",
		"/out/vendor",
		"/opt/super-dolphin-gate/runtime/vendor",
		"COPY cmd/ ./cmd/",
		"COPY internal/ ./internal/",
		"COPY pkg/ ./pkg/",
		"COPY scripts/ ./scripts/",
		"COPY docs/",
		"COPY test/",
	} {
		if strings.Contains(dockerfile, unwanted) {
			t.Fatalf("runtime dependencies Dockerfile still depends on job source %q", unwanted)
		}
	}
}

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validRuntimeDepsLock(inputs runtimeDepsInputs) runtimeDepsLock {
	return runtimeDepsLock{SchemaVersion: runtimeDepsSchemaVersion, BuildMode: runtimeDepsBuildMode, CacheScope: runtimeDepsCacheScope, Inputs: inputs, Paths: canonicalRuntimeDepsPaths()}
}

func writeRuntimeDepsInputs(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{gateRuntimeDepsDocker, gateToolchain, "go.mod", "go.sum", "internal/devtools/nilnessrunner/runner.go", "scripts/nilness_guard.go", "frontend-app/package-lock.json", gateRuntimeLSPLock, gateRuntimeProxyModule, gateRuntimeProxySum, gateRuntimeToolsModule, gateRuntimeToolsSum, "build/gate/cmd/runtime-seed-manifest/main.go", "internal/devtools/gate/executor_seed.go"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
