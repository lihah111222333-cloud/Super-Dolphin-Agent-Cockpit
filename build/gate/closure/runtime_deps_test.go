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
	recipeInputs, err := digestRuntimeDepsRecipeInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	lock := validRuntimeDepsLock(inputs)
	lock.RecipeInputs = recipeInputs
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

func TestRuntimeDepsLockRejectsRuntimeSeedWorkerRecipeClosureDrift(t *testing.T) {
	root := t.TempDir()
	writeRuntimeDepsInputs(t, root)
	inputs, err := digestRuntimeDepsInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	recipeInputs, err := digestRuntimeDepsRecipeInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{runtimeSeedWorkerRecipePath, runtimeSeedFrontendRecipePath} {
		t.Run(path, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte("drift\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			lock := validRuntimeDepsLock(inputs)
			lock.RecipeInputs = recipeInputs
			if err := lock.validateAgainstSource(root, toolchainLock{NetworkPolicy: "none"}); !errors.Is(err, errRuntimeDepsInputsDrift) {
				t.Fatalf("runtime seed worker recipe drift error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte(path+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeDepsLockRejectsMissingRuntimeSeedWorkerRecipeClosureFile(t *testing.T) {
	root := t.TempDir()
	writeRuntimeDepsInputs(t, root)
	inputs, err := digestRuntimeDepsInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	recipeInputs, err := digestRuntimeDepsRecipeInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	lock := validRuntimeDepsLock(inputs)
	lock.RecipeInputs = recipeInputs
	for _, path := range []string{runtimeSeedWorkerRecipePath, runtimeSeedFrontendRecipePath} {
		t.Run(path, func(t *testing.T) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(path))); err != nil {
				t.Fatal(err)
			}
			if err := lock.validateAgainstSource(root, toolchainLock{NetworkPolicy: "none"}); err == nil {
				t.Fatal("missing runtime seed worker recipe closure file unexpectedly passed")
			}
			writeTestRuntimeDepsInput(t, root, path)
		})
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
	inputs := runtimeDepsInputs{ToolchainLock: testDigest, GoMod: testDigest, GoSum: testDigest, GoDistributionLock: testDigest, NilnessRunner: testDigest, NilnessGuard: testDigest, FrontendPackageLock: testDigest, LSPPackageLock: testDigest, ProxyGoMod: testDigest, ProxyGoSum: testDigest, ToolsGoMod: testDigest, ToolsGoSum: testDigest}
	recipeInputs := runtimeDepsRecipeInputs{Dockerfile: testDigest, RuntimeSeedWorker: testDigest}
	lock := validRuntimeDepsLock(inputs)
	lock.RecipeInputs = recipeInputs
	data, err := encodeRuntimeDepsLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"registry_pull_policy", "images", "oci_index_digest", "platform_manifest_digest", "runtime_seed_recipe_sha256", "runtime_seed_script_sha256", "runtime_seed_script_browser_sha256", "runtime_seed_script_runtime_sha256", "runtime_seed_script_tail_sha256", "runtime_seed_script_control_sha256"} {
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
		"go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate",
		"/tmp/super-dolphin-gate worker runtime-seed write",
	} {
		if !strings.Contains(dockerfile, wanted) {
			t.Fatalf("runtime dependencies Dockerfile is missing %q", wanted)
		}
	}
	for _, unwanted := range []string{
		"go mod vendor",
		"super-dolphin-runtime-seed",
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
	write := strings.Index(dockerfile, "/tmp/super-dolphin-gate worker runtime-seed write")
	remove := strings.Index(dockerfile, "rm /opt/super-dolphin-gate/runtime/manifest.json")
	absent := strings.Index(dockerfile, "test ! -e /opt/super-dolphin-gate/runtime/manifest.json")
	if write < 0 || remove <= write || absent <= remove {
		t.Fatal("runtime dependency image must validate then remove its temporary manifest before generation-one takes ownership")
	}
}

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validRuntimeDepsLock(inputs runtimeDepsInputs) runtimeDepsLock {
	return runtimeDepsLock{SchemaVersion: runtimeDepsSchemaVersion, BuildMode: runtimeDepsBuildMode, CacheScope: runtimeDepsCacheScope, Inputs: inputs, RecipeInputs: runtimeDepsRecipeInputs{Dockerfile: testDigest, RuntimeSeedWorker: testDigest}, Paths: canonicalRuntimeDepsPaths()}
}

func writeRuntimeDepsInputs(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{gateRuntimeDepsDocker, gateToolchain, "go.mod", "go.sum", "internal/devtools/godistribution/go-distribution.lock", "internal/devtools/nilnessrunner/runner.go", "scripts/nilness_guard.go", "frontend-app/package-lock.json", gateRuntimeLSPLock, gateRuntimeProxyModule, gateRuntimeProxySum, gateRuntimeToolsModule, gateRuntimeToolsSum, runtimeSeedWorkerRecipePath, runtimeSeedFrontendRecipePath} {
		writeTestRuntimeDepsInput(t, root, name)
	}
}

func writeTestRuntimeDepsInput(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
