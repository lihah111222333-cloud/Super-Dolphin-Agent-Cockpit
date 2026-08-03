package gateclosure_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

const (
	dockerfilePath       = "build/gate/Dockerfile"
	manifestPath         = "build/gate/inputs.json"
	toolchainPath        = "build/gate/toolchain.lock"
	runtimeDepsLockPath  = "build/gate/runtime-deps.lock"
	runtimeDepsBuildPath = "build/gate/runtime-deps.Dockerfile"
)

type manifest struct {
	SchemaVersion             string   `json:"schema_version"`
	Dockerfile                string   `json:"dockerfile"`
	Inputs                    []string `json:"inputs"`
	GateCompileInputs         []string `json:"gate_compile_inputs"`
	SourceSnapshotInputCount  int      `json:"source_snapshot_input_count"`
	SourceSnapshotPathsSHA256 string   `json:"source_snapshot_paths_sha256"`
}

func TestTruthImageClosureUsesImmutableStandaloneRuntime(t *testing.T) {
	root := repositoryRoot(t)
	tracked := readManifest(t, root)
	if tracked.SchemaVersion != "3" || tracked.Dockerfile != dockerfilePath ||
		!sort.StringsAreSorted(tracked.Inputs) || !sort.StringsAreSorted(tracked.GateCompileInputs) ||
		tracked.SourceSnapshotInputCount <= 0 || !strings.HasPrefix(tracked.SourceSnapshotPathsSHA256, "sha256:") {
		t.Fatalf("invalid truth image manifest identity: %+v", tracked)
	}
	for _, required := range []string{
		"cmd/super-dolphin-gate/main.go",
		"internal/devtools/gate/executor.go",
		"internal/devtools/gate/executor_mapping.go",
		toolchainPath, runtimeDepsLockPath, runtimeDepsBuildPath,
	} {
		if !slices.Contains(tracked.Inputs, required) {
			t.Fatalf("truth image closure is missing %s", required)
		}
	}
	for _, required := range []string{"cmd/super-dolphin-gate/main.go", "go.mod", "go.sum"} {
		if !slices.Contains(tracked.GateCompileInputs, required) {
			t.Fatalf("gate compile closure is missing %s", required)
		}
	}
	for _, name := range tracked.Inputs {
		if strings.Contains(name, "node_modules") || strings.HasSuffix(name, ".tar.gz") || strings.Contains(name, "runtime-tools-vendor") {
			t.Fatalf("large dependency payload entered Git closure: %s", name)
		}
		if strings.Contains(name, "super-dolphin-gate-executor") || strings.Contains(name, "runtime-seed-manifest") {
			t.Fatalf("retired secondary command entered standalone gate closure: %s", name)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("closure input %s is not a regular file: %v", name, err)
		}
	}
}

func TestTruthDockerfileIsOfflineAndDigestOnly(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, dockerfilePath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"ARG RUNTIME_DEPS_IMAGE\n",
		"ARG BASELINE_CACHE_IMAGE\n",
		"ARG COMPILED_SEED_IMAGE\n",
		"ARG MAIN_COMMIT\n",
		"ARG GATE_SOURCE_DIGEST\n",
		"ARG RUNTIME_DEPENDENCY_DIGEST\n",
		"FROM ${BASELINE_CACHE_IMAGE} AS baseline-cache",
		"--mount=type=bind,from=baseline-cache,source=/,target=/baseline-cache,ro",
		"worker go-cache-proxy --seed /baseline-cache/opt/super-dolphin/cache/go-build --private /out/go-build-cache",
		"env GOCACHEPROG=\"$go_cache_proxy --metrics",
		"FROM ${RUNTIME_DEPS_IMAGE} AS compiled-seed\nUSER root",
		"FROM ${COMPILED_SEED_IMAGE} AS compiled-seed-source",
		"FROM ${RUNTIME_DEPS_IMAGE} AS postprocess",
		"remote-ci-cache-material/v1",
		`"authority":"non_authoritative_material"`,
		`"seed_steps"`,
		"compiled seed material manifest fields mismatch",
		"compiled seed material identity mismatch",
		"org.super-dolphin.gate-source-digest=\"${GATE_SOURCE_DIGEST}\"",
		"org.super-dolphin.main-commit-sha=\"${MAIN_COMMIT}\"",
		"go build -mod=mod -trimpath -buildvcs=false -o /out/super-dolphin-gate ./cmd/super-dolphin-gate",
		"seed_compile_phase normal_compile env CGO_ENABLED=1 go test -mod=mod -run \"^$\" ./...",
		"seed_compile_phase e2e_compile env CGO_ENABLED=1 go test -mod=mod -tags=e2e -run \"^$\" ./...",
		"seed_compile_phase race_compile env CGO_ENABLED=1 go test -mod=mod -race -run \"^$\" \"$@\"",
		"seed compile complete phase=%s authority=non_authoritative_material",
		"compiled-seed complete go_cache_entries=%s",
		"COPY --from=postprocess /out/super-dolphin-gate /super-dolphin-gate",
		"ENTRYPOINT [\"/super-dolphin-gate\"]",
		"ENV GOCACHE=/out/go-build-cache",
		"COPY --from=postprocess /out/go-build-cache /opt/super-dolphin/cache/go-build",
		"chmod -R a-w /opt/super-dolphin/cache/go-build",
		"org.super-dolphin.source-tree-sha=\"${BUILD_SOURCE_TREE}\"",
		"org.super-dolphin.image-input-digest=\"${IMAGE_INPUT_DIGEST}\"",
		"org.super-dolphin.policy-sha=\"${POLICY_DIGEST}\"",
		"org.super-dolphin.toolchain-digest=\"${TOOLCHAIN_DIGEST}\"",
		"org.super-dolphin.platform=\"${TARGET_PLATFORM}\"",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("truth Dockerfile is missing %q", required)
		}
	}
	for _, forbidden := range []string{"runtime-deps:latest", "runtime-node.tar", "runtime-tools.tar", "vendor.tar.gz", "go mod download", "super-dolphin-gate-executor", "/opt/super-dolphin-gate/cache-seed/go-build", "COPY --from=baseline-cache /opt/super-dolphin/cache/go-build /root/.cache/go-build", "cp -a /baseline-cache", "record_provision_check", "generation-one-build-receipt", "generation-one-receipts", "provision_checks", "accepted_snapshot_id"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("truth Dockerfile contains forbidden fallback %q", forbidden)
		}
	}
	if strings.Contains(text, "127.0.0.1:5000") || strings.Contains(text, "localhost:") {
		t.Fatal("truth Dockerfile must receive its runtime dependency image from the platform-bound build request")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "RUN ") && !strings.HasPrefix(line, "RUN --network=none ") {
			t.Fatalf("truth Dockerfile has network-capable RUN: %s", line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDependencyRefreshInputsAreSmallLocks(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, toolchainPath))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		RuntimeDepsLock   string   `json:"runtime_deps_lock"`
		DependencySources []string `json:"dependency_sources"`
		NetworkPolicy     string   `json:"network_policy"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err == nil {
		t.Fatal("partial toolchain schema unexpectedly decoded with unknown-field rejection")
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["runtime_deps_lock"] != runtimeDepsLockPath || document["network_policy"] != "none" {
		t.Fatalf("toolchain runtime boundary drifted: %+v", document)
	}
	for _, name := range []string{"go.sum", "frontend-app/package-lock.json", "build/gate/runtime-lsp/package-lock.json", "build/gate/runtime-proxy/go.mod", "build/gate/runtime-proxy/go.sum", "build/gate/runtime-tools/go.mod", "build/gate/runtime-tools/go.sum"} {
		if !slices.Contains(asStrings(t, document["dependency_sources"]), name) {
			t.Fatalf("toolchain dependency sources are missing %s", name)
		}
	}
}

func TestRuntimeDependencyRefreshInstallsLockedChromiumOnlyInRefreshImage(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, runtimeDepsBuildPath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "ARG BUILD_SOURCE_TREE") || strings.Contains(text, "org.super-dolphin.source-tree-sha") {
		t.Fatal("runtime dependency image identity must not depend on the production source tree")
	}
	identityLabelIndex := strings.Index(text, "LABEL org.super-dolphin.runtime-deps-input-digest")
	runtimeProbeIndex := strings.Index(text, `RUN --network=none test "$(actionlint -version | head -n 1)" = "v1.7.12"`)
	if identityLabelIndex < 0 || runtimeProbeIndex < 0 || identityLabelIndex <= runtimeProbeIndex {
		t.Fatal("runtime dependency identity labels must follow all expensive dependency layers")
	}
	for _, required := range []string{
		`/tmp/super-dolphin-gate worker validate-go-distribution`,
		`test "$(/usr/local/go/bin/go version)" = "go version go1.26.5 linux/amd64"`,
		`test "$(node --version)" = "v24.18.0"`,
		`test "$(npm --version)" = "11.16.0"`,
		`test "$(python3 --version)" = "Python 3.11.2"`,
		`test "$(gopls version | tail -n 1)" = "golang.org/x/tools/gopls v0.22.0"`,
		`test "$(/opt/super-dolphin-gate/runtime/bin/sqlc version)" = "v1.30.0"`,
		"go mod download all",
		"cd /src/build/gate/runtime-proxy",
		"github.com/kelindar/event/@v/v1.5.2.zip",
		"grep -Fxq v1.5.2",
		"cp -a \"$(go env GOMODCACHE)/cache/download/.\" /out/go-proxy/",
		"./node_modules/.bin/playwright install chromium",
		"/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright",
		"playwright install-deps chromium",
		"apt-get install -y --no-install-recommends ripgrep=13.0.0-4+b2",
		"COPY --from=repository-module-cache /out/go-proxy /opt/super-dolphin-gate/runtime/go-proxy",
		"COPY --from=ripgrep-seed /out/bin/rg /opt/super-dolphin-gate/runtime/bin/rg",
		"libgtk-3-dev libwebkit2gtk-4.1-dev libsoup-3.0-dev pkg-config procps xauth xvfb",
		"test -x /usr/bin/Xvfb && test -x /usr/bin/xauth && test -x /usr/bin/xvfb-run",
		"pkg-config --exists gtk+-3.0 webkit2gtk-4.1 gio-unix-2.0 libsoup-3.0",
		"test \"$(rg --version | head -n 1)\" = \"ripgrep 13.0.0\"",
		"USER 65532:65532",
		"RUN --network=none xvfb-run -a sh -ec 'test -n \"$DISPLAY\"'",
		"RUN --network=none node -e",
		"GOPROXY=off",
		"NPM_CONFIG_CACHE=/out/frontend/npm-cache npm ci --ignore-scripts --no-audit --no-fund",
		"NPM_CONFIG_CACHE=/out/frontend/npm-cache npm ci --ignore-scripts --no-audit --no-fund --offline",
		"COPY --from=frontend-seed /out/frontend/npm-cache /opt/super-dolphin-gate/runtime/frontend/npm-cache",
		"chmod -R a+rX /out/frontend/node_modules/.cache/ms-playwright",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runtime dependency Dockerfile is missing %q", required)
		}
	}
	truth, err := os.ReadFile(filepath.Join(root, dockerfilePath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(truth), "playwright install") || strings.Contains(string(truth), "apt-get") {
		t.Fatal("ordinary truth image build attempts to install browser dependencies")
	}
}

func TestRuntimeDependencyRefreshSeedsViteCacheBeforeManifest(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, runtimeDepsBuildPath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	viteCacheSeed := "mkdir -p /opt/super-dolphin-gate/runtime/frontend/vite-cache"
	manifestWrite := "/tmp/super-dolphin-gate worker runtime-seed write /tmp/runtime-manifest-source /opt/super-dolphin-gate/runtime"
	viteCacheSeedIndex := strings.Index(text, viteCacheSeed)
	manifestWriteIndex := strings.Index(text, manifestWrite)
	if viteCacheSeedIndex < 0 || manifestWriteIndex < 0 || viteCacheSeedIndex >= manifestWriteIndex {
		t.Fatalf("runtime dependency Dockerfile must explicitly seed Vite cache before writing its manifest")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readManifest(t *testing.T, root string) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value manifest
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func asStrings(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("value is not an array: %T", value)
	}
	result := make([]string, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("array item is not a string: %T", item)
		}
		result[index] = text
	}
	return result
}
