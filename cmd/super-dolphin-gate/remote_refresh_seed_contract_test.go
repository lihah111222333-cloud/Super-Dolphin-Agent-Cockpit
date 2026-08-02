package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestBuildRemoteBaselineSeedRequestUsesPreviousGeneration(t *testing.T) {
	config, accepted, input, source := remoteBaselineSeedRequestFixture(t)
	invalidInput := input
	invalidInput.GateSourceDigest = ""
	if _, err := buildRemoteBaselineSeedRequest(config, invalidInput, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1); err == nil {
		t.Fatal("buildRemoteBaselineSeedRequest() accepted a missing gate source digest")
	}
	invalidInput = input
	invalidInput.GoToolchain = ""
	if _, err := buildRemoteBaselineSeedRequest(config, invalidInput, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1); err == nil {
		t.Fatal("buildRemoteBaselineSeedRequest() accepted a missing Go toolchain")
	}
	invalidInput = input
	invalidInput.RuntimeDependencySchemaVersion = ""
	if _, err := buildRemoteBaselineSeedRequest(config, invalidInput, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1); err == nil {
		t.Fatal("buildRemoteBaselineSeedRequest() accepted a missing runtime dependency schema")
	}
	invalidAccepted := accepted
	invalidAccepted.MainCommit = ""
	if _, err := buildRemoteBaselineSeedRequest(config, input, source, invalidAccepted, invalidAccepted.DataCacheSizeGiB, invalidAccepted.Generation+1); err == nil || !strings.Contains(err.Error(), "full Anchor rebuild is forbidden") {
		t.Fatalf("buildRemoteBaselineSeedRequest() invalid previous state error = %v", err)
	}
	assertRemoteBaselineFullRefreshForbidden(t, config, input, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1)
	source.Manifest.Mode, source.Manifest.BaseCommit, source.Manifest.BaseTree = remoteBaselineSourceFull, "", ""
	request := mustBuildRemoteBaselineSeedRequest(t, config, input, source, remoteci.BaselineState{}, 0, 1)
	assertRemoteBaselineSeedConditions(t, request, "first seed request unexpectedly uses previous DataCache", []bool{
		request.DataCacheBucket == "", request.PreviousDataCachePath == "",
	})
	accepted.Platform, accepted.ToolchainDigest, accepted.RuntimeImage = input.Identity.Platform, input.Identity.ToolchainDigest, input.Identity.RuntimeImage
	assertRemoteBaselineFullRefreshForbidden(t, config, input, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1)
	input.AcceptedRuntimeDependencyDigest = input.RuntimeDependencyDigest
	source.Manifest.Mode, source.Manifest.BaseCommit, source.Manifest.BaseTree = remoteBaselineSourceDelta, accepted.MainCommit, accepted.MainTree
	request = mustBuildRemoteBaselineSeedRequest(t, config, input, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1)
	assertPreviousRemoteBaselineSeedRequest(t, request, accepted, input)
	if _, exists := request.Environment["BASELINE_DIRECT_CACHE_LAYER_COUNT"]; exists {
		t.Fatal("pre-direct migration declared an empty direct cache chain")
	}
	assertRemoteBaselineSeedConditions(t, request, "compatible delta refresh", []bool{
		!request.AutoCreateEIP, request.EIPBandwidth == 0,
		request.Environment["BASELINE_STORAGE_MODE"] == remoteci.BaselineStorageModeDelta,
		request.Environment["BASELINE_TOOLCHAIN_CHANGED"] == "false",
		request.DataCacheBucket == accepted.Anchor.DataCacheBucket, request.PreviousDataCachePath == accepted.Anchor.DataCachePath,
		request.BaselineLayers.Path == "/baseline-artifacts", request.Environment["BASELINE_DELTA_MANIFEST_1"] != "",
	})
	newestLayer := remoteBaselineSeedDirectLayerFixture(accepted, 2)
	parentChain, err := remoteci.CurrentBaselineParentChainDigest(accepted)
	if err != nil {
		t.Fatal(err)
	}
	newestLayer.ParentChainSHA256 = parentChain
	accepted.DirectCacheRef = &remoteci.DirectCacheRef{Layers: []remoteci.DirectCacheLayerRef{
		newestLayer, remoteBaselineSeedDirectLayerFixture(accepted, 1),
	}}
	if err := accepted.Validate(); err != nil {
		t.Fatalf("direct layer fixture validation: %v", err)
	}
	request = mustBuildRemoteBaselineSeedRequest(t, config, input, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1)
	assertRemoteBaselineSeedConditions(t, request, "direct layer delta refresh", []bool{
		len(request.DirectCacheLayers) == 2,
		request.DirectCacheLayers[0].Generation == 2 && request.DirectCacheLayers[1].Generation == 1,
		request.Environment["BASELINE_DIRECT_CACHE_LAYER_COUNT"] == "2",
		request.Environment["BASELINE_DIRECT_CACHE_LAYER_1"] != "" && request.Environment["BASELINE_DIRECT_CACHE_LAYER_2"] != "",
	})
	toolchainInput := input
	toolchainInput.Identity.ToolchainDigest = "sha256:" + repeatRemoteHex("9", 64)
	toolchainInput.GoToolchain = "go1.26.5"
	request = mustBuildRemoteBaselineSeedRequest(t, config, toolchainInput, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1)
	assertRemoteBaselineSeedConditions(t, request, "toolchain-changing incremental refresh", []bool{
		request.Environment["BASELINE_STORAGE_MODE"] == remoteci.BaselineStorageModeDelta,
		request.Environment["BASELINE_TOOLCHAIN_CHANGED"] == "true",
		!request.AutoCreateEIP, request.EIPBandwidth == 0,
	})
	historicalInput := input
	historicalInput.RuntimeDependencySchemaVersion = "8"
	assertRemoteBaselineFullRefreshForbidden(t, config, historicalInput, source, accepted, accepted.DataCacheSizeGiB, accepted.Generation+1)
	if accepted.PolicyDigest == input.Identity.PolicyDigest {
		t.Fatal("fixture must prove policy changes do not rebuild compatible layers")
	}
	assertRemoteBaselineFullRefreshForbidden(t, config, input, source, accepted, accepted.DataCacheSizeGiB+80, accepted.Generation+1)
	assertRemoteBaselineSeedCompaction(t, config, input, source, accepted)
}

func remoteBaselineSeedDirectLayerFixture(accepted remoteci.BaselineState, generation uint64) remoteci.DirectCacheLayerRef {
	value := strconv.FormatUint(generation, 10)
	return remoteci.DirectCacheLayerRef{
		DataCacheID: "edc-direct" + value, DataCacheBucket: accepted.DataCacheBucket,
		DataCachePath: "/super-dolphin/ci/direct-cache/" + value, SizeGiB: 20, Generation: generation,
		SourceObjectPrefix: "baseline-artifacts/" + value + "/output/direct-cache/",
		ManifestDigest:     "sha256:" + repeatRemoteHex("a", 64), TreeSHA256: "sha256:" + repeatRemoteHex("b", 64),
		ParentChainSHA256: "sha256:" + repeatRemoteHex("c", 64), RuntimeGoSHA256: accepted.RuntimeSeedSHA256,
		RuntimeDepsSHA256: "sha256:" + repeatRemoteHex("d", 64),
	}
}

func remoteBaselineSeedRequestFixture(t *testing.T) (remoteRunConfig, remoteci.BaselineState, remoteBaselineRefreshInput, remoteBaselineSourceArtifact) {
	t.Helper()
	configPath := writeRemoteRunConfigFixture(t, validRemoteRunConfigJSON())
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	accepted := remoteBaselineStateFixture()
	input := remoteBaselineRefreshInput{
		Identity: remoteci.BaselineIdentity{
			MainCommit: repeatRemoteHex("a", 40), MainTree: repeatRemoteHex("b", 40),
			Platform: "linux/arm64", PolicyDigest: "sha256:" + repeatRemoteHex("c", 64),
			ToolchainDigest: "sha256:" + repeatRemoteHex("d", 64),
			RuntimeImage:    config.Runtime.Image,
		},
		GateSourceDigest:                "sha256:" + repeatRemoteHex("0", 64),
		RuntimeDependencyDigest:         "sha256:" + repeatRemoteHex("1", 64),
		AcceptedRuntimeDependencyDigest: "sha256:" + repeatRemoteHex("2", 64),
		RuntimeDependencySchemaVersion:  remoteci.RuntimeDependencySchemaVersion,
		GoToolchain:                     "go1.26.5",
		SqruffURL:                       "https://github.com/example/sqruff.tar.gz",
		SqruffSHA256:                    repeatRemoteHex("e", 64),
	}
	source := remoteBaselineSourceArtifact{
		Manifest: remoteBaselineSourceManifest{
			SchemaVersion: remoteBaselineSourceManifestSchemaVersion,
			Mode:          remoteBaselineSourceDelta,
			BaseCommit:    accepted.MainCommit,
			BaseTree:      accepted.MainTree,
			TargetCommit:  input.Identity.MainCommit,
			TargetTree:    input.Identity.MainTree,
			BundleFile:    "source.bundle",
			BundleSHA256:  repeatRemoteHex("f", 64),
			BundleSize:    1024,
		},
		ManifestSHA256: repeatRemoteHex("0", 64),
	}
	return config, accepted, input, source
}

func assertRemoteBaselineSeedCompaction(t *testing.T, config remoteRunConfig, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, accepted remoteci.BaselineState) {
	t.Helper()
	compaction := accepted
	for _, identity := range []struct{ commit, tree string }{{"5", "6"}, {"7", "8"}, {"9", "0"}} {
		baseCommit, baseTree := compaction.MainCommit, compaction.MainTree
		compaction.Generation++
		compaction.MainCommit, compaction.MainTree = repeatRemoteHex(identity.commit, 40), repeatRemoteHex(identity.tree, 40)
		compaction.BaselineManifestDigest = "sha256:" + repeatRemoteHex(identity.commit, 64)
		compaction.SourceObjectPrefix = fmt.Sprintf("baseline-artifacts/%d/", compaction.Generation)
		compaction.AcceptedAt = compaction.AcceptedAt.Add(time.Minute)
		compaction.Deltas = append(compaction.Deltas, remoteci.BaselineDeltaRef{
			Generation: compaction.Generation, SourceObjectPrefix: compaction.SourceObjectPrefix,
			ManifestDigest: compaction.BaselineManifestDigest, BaseCommit: baseCommit, BaseTree: baseTree,
			MainCommit: compaction.MainCommit, MainTree: compaction.MainTree, AcceptedAt: compaction.AcceptedAt,
		})
	}
	source.Manifest.BaseCommit, source.Manifest.BaseTree = compaction.MainCommit, compaction.MainTree
	assertRemoteBaselineFullRefreshForbidden(t, config, input, source, compaction, compaction.DataCacheSizeGiB, compaction.Generation+1)
}

func assertRemoteBaselineFullRefreshForbidden(t *testing.T, config remoteRunConfig, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, accepted remoteci.BaselineState, acceptedRecommendedSizeGiB int, generation uint64) {
	t.Helper()
	_, err := buildRemoteBaselineSeedRequest(config, input, source, accepted, acceptedRecommendedSizeGiB, generation)
	if err == nil || !strings.Contains(err.Error(), "full Anchor rebuild is forbidden") {
		t.Fatalf("buildRemoteBaselineSeedRequest() error = %v, want full Anchor rebuild rejection", err)
	}
}

func assertRemoteBaselineSeedConditions(t *testing.T, request eci.SeedRequest, message string, conditions []bool) {
	t.Helper()
	for _, condition := range conditions {
		if !condition {
			t.Fatalf("%s = %#v", message, request)
		}
	}
}

func mustBuildRemoteBaselineSeedRequest(
	t *testing.T,
	config remoteRunConfig,
	input remoteBaselineRefreshInput,
	source remoteBaselineSourceArtifact,
	accepted remoteci.BaselineState,
	acceptedRecommendedSizeGiB int,
	generation uint64,
) eci.SeedRequest {
	t.Helper()
	request, err := buildRemoteBaselineSeedRequest(
		config,
		input,
		source,
		accepted,
		acceptedRecommendedSizeGiB,
		generation,
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func assertPreviousRemoteBaselineSeedRequest(
	t *testing.T,
	request eci.SeedRequest,
	accepted remoteci.BaselineState,
	input remoteBaselineRefreshInput,
) {
	t.Helper()
	assertPreviousRemoteBaselineSeedStorage(t, request, accepted)
	assertPreviousRemoteBaselineSeedResources(t, request)
	assertPreviousRemoteBaselineSeedPaths(t, request)
	assertPreviousRemoteBaselineSeedEnvironment(t, request, input)
}

func assertPreviousRemoteBaselineSeedStorage(t *testing.T, request eci.SeedRequest, accepted remoteci.BaselineState) {
	t.Helper()
	anchor := accepted.CurrentAnchorRef()
	if request.DataCacheBucket != anchor.DataCacheBucket || request.PreviousDataCachePath != anchor.DataCachePath {
		t.Fatalf("seed request = %#v", request)
	}
	want, err := remoteBaselineSeedClientToken(request)
	if err != nil {
		t.Fatal(err)
	}
	if request.ClientToken != want {
		t.Fatalf("seed client token = %q, want %q", request.ClientToken, want)
	}
	changed := request
	changed.Resources.MemoryGiB++
	changedToken, err := remoteBaselineSeedClientToken(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedToken == request.ClientToken {
		t.Fatal("seed client token did not change with the request contract")
	}
}

func assertPreviousRemoteBaselineSeedResources(t *testing.T, request eci.SeedRequest) {
	t.Helper()
	if request.Resources.CPU != 4 || request.Resources.MemoryGiB != 16 {
		t.Fatalf("seed request = %#v", request)
	}
}

func assertPreviousRemoteBaselineSeedPaths(t *testing.T, request eci.SeedRequest) {
	t.Helper()
	if request.Input.Path != "/baseline-artifacts/3/input" || request.Output.Path != "/baseline-artifacts/3/output" {
		t.Fatalf("seed request = %#v", request)
	}
}

func assertPreviousRemoteBaselineSeedEnvironment(t *testing.T, request eci.SeedRequest, input remoteBaselineRefreshInput) {
	t.Helper()
	if request.Environment["BASELINE_MAIN_TREE"] != input.Identity.MainTree ||
		request.Environment["BASELINE_GATE_SOURCE_SHA256"] != input.GateSourceDigest ||
		request.Environment["BASELINE_GO_TOOLCHAIN"] != input.GoToolchain ||
		request.Environment["BASELINE_MANIFEST_SCHEMA_VERSION"] != strconv.FormatUint(uint64(remoteci.BaselineManifestSchemaVersion), 10) ||
		request.Environment["BASELINE_MANIFEST_MIN_COMPATIBLE_VERSION"] != strconv.FormatUint(uint64(remoteci.BaselineManifestMinimumCompatibleVersion), 10) ||
		request.Environment["BASELINE_SEED_SCRIPT_SHA256"] != digestBytes([]byte(remoteBaselineSeedScript)) ||
		request.Environment["BASELINE_SEED_SCRIPT_SIZE"] != strconv.Itoa(len(remoteBaselineSeedScript)) ||
		request.Environment["BASELINE_ANCHOR_MANIFEST_DIGEST"] == "" {
		t.Fatalf("seed request = %#v", request)
	}
	if string(request.Script) != remoteBaselineSeedBootstrapScript {
		t.Fatalf("seed request bootstrap script = %q", request.Script)
	}
}

func TestRemoteBaselineSeedScriptSyntaxAndContract(t *testing.T) {
	assertRemoteBaselineSeedScriptSizes(t)
	assertRemoteBaselineSeedScriptSyntax(t)
	assertRemoteBaselineSeedRequiredFragments(t)
	assertRemoteBaselineSeedForbiddenFragments(t)
	assertRemoteBaselineSeedCacheOrdering(t)
	assertRemoteBaselineSeedCommandCounts(t)
}

func assertRemoteBaselineSeedScriptSizes(t *testing.T) {
	t.Helper()
	if len(remoteBaselineSeedBootstrapScript) == 0 || len(remoteBaselineSeedBootstrapScript) > 32<<10 {
		t.Fatalf("seed bootstrap script size = %d", len(remoteBaselineSeedBootstrapScript))
	}
	if len(remoteBaselineSeedScript) == 0 || len(remoteBaselineSeedScript) > 256<<10 {
		t.Fatalf("seed script size = %d", len(remoteBaselineSeedScript))
	}
}

func assertRemoteBaselineSeedScriptSyntax(t *testing.T) {
	t.Helper()
	for name, script := range map[string]string{"bootstrap": remoteBaselineSeedBootstrapScript, "seed": remoteBaselineSeedScript} {
		path := filepath.Join(t.TempDir(), name+".sh")
		if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("sh -n %s script: %v: %s", name, err, output)
		}
	}
}

func assertRemoteBaselineSeedCommandCounts(t *testing.T) {
	t.Helper()
	if count := strings.Count(remoteBaselineSeedScript, "go mod download all"); count != 1 {
		t.Fatalf("disposable source whole-module-graph downloads = %d, want 1", count)
	}
	if count := countRemoteSeedShellCommand(remoteBaselineSeedScript, `GOMODCACHE="$go_mod_cache" go mod download`); count != 1 {
		t.Fatalf("locked runtime module proxy downloads = %d, want 1", count)
	}
	if count := strings.Count(remoteBaselineSeedScript, "GOFLAGS=-mod=readonly"); count != 5 {
		t.Fatalf("read-only Go dependency closure commands = %d, want 5", count)
	}
	if count := strings.Count(remoteBaselineSeedScript, "go list -deps -test ./... >/dev/null"); count != 3 {
		t.Fatalf("offline-ready Go test dependency closures = %d, want 3", count)
	}
	if count := strings.Count(remoteBaselineSeedScript, "windows/amd64 windows/arm64"); count != 1 {
		t.Fatalf("supported target dependency closure declarations = %d, want 1", count)
	}
	if count := strings.Count(remoteBaselineSeedScript, "go build -mod=readonly -trimpath -buildvcs=false -o $payload_root/runtime/bin/"); count != 3 {
		t.Fatalf("read-only runtime tool builds = %d, want 3", count)
	}
	if count := countRemoteSeedShellCommand(remoteBaselineSeedScript, "verify_source_tree_clean"); count != 4 {
		t.Fatalf("source tree clean checks = %d, want 4", count)
	}
	if count := strings.Count(remoteBaselineSeedScript, "-exec=true -run '^$' ./..."); count != 1 {
		t.Fatalf("full-tree compile-only Go cache refreshes = %d, want 1 normal build", count)
	}
	if strings.Contains(remoteBaselineSeedScript, "go test -mod=readonly -race -exec=true -run '^$' ./...") {
		t.Fatal("race cache refresh must use the canonical race package registry, not the full repository")
	}
}

func countRemoteSeedShellCommand(script, command string) int {
	count := 0
	for line := range strings.SplitSeq(script, "\n") {
		if strings.TrimSpace(line) == command {
			count++
		}
	}
	return count
}

func assertRemoteBaselineSeedRequiredFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedToolchainFragments(t)
	assertRemoteBaselineSeedGateIdentityFragments(t)
	assertRemoteBaselineSeedOfflineDependencyFragments(t)
	assertRemoteBaselineSeedRuntimeReuseFragments(t)
	assertRemoteBaselineSeedLayerFragments(t)
}

func assertRemoteBaselineSeedToolchainFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedContains(t, []string{
		"historical runtime dependency schema requires seed refresh",
		"BASELINE_FORCE_RUNTIME_REFRESH",
		"export HOME=/tmp/home",
		"export XDG_CACHE_HOME=/tmp/xdg-cache",
		"export GOENV=off",
		"export GOTELEMETRY=off",
		"BASELINE_GO_TOOLCHAIN",
		"go1.26.5/amd64) go_sha256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053",
		"go1.26.5/arm64) go_sha256=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49",
		"unsupported locked Go archive",
		"${BASELINE_GO_TOOLCHAIN}.linux-${go_arch}.tar.gz",
		"test \"$(go version | awk '{print $3}')\" = \"$BASELINE_GO_TOOLCHAIN\"",
		"node-v24.18.0-linux-x64.tar.xz",
		"Python-3.11.2.tar.xz",
		"https://mirrors.aliyun.com/golang/",
		"https://mirrors.aliyun.com/nodejs-release/",
		"https://mirrors.aliyun.com/python-release/source/",
		"runtime_dependency_goproxy=https://goproxy.cn,direct",
		"if test -n \"$previous_runtime\"; then",
		"runtime_dependency_goproxy=off",
		"run_logged()",
		"tail -n 160",
		"build_python_runtime()",
		"refresh_go_build_cache()",
		"worker go-module-overlay \"$go_mod_cache\" \"$private_go_mod_cache\"",
		"go module cache source: immutable runtime overlay",
	})
}

func assertRemoteBaselineSeedGateIdentityFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedContains(t, []string{
		"load_verified_gate()",
		"verify_gate_cli_identity()",
		"grep -Fq 'case \"cli-identity\":' \"$source_root/cmd/super-dolphin-gate/main.go\"",
		"\"$binary\" plan local-fast >/dev/null",
		"gate CLI identity mode: source-bound legacy probe",
		"candidate_gate_source=$(sed -n 's/.*\"gate_source_sha256\"",
		"test \"$previous_gate_source_sha256\" = \"$BASELINE_GATE_SOURCE_SHA256\"",
		"test \"$previous_gate_platform\" = \"$BASELINE_PLATFORM\"",
		"test \"$previous_gate_toolchain_digest\" = \"$BASELINE_TOOLCHAIN_DIGEST\"",
		"build_gate_cli()",
		"compile_gate_cli()",
		"-X main.gateSourceDigest=$BASELINE_GATE_SOURCE_SHA256",
		"-X main.gateToolchainDigest=$BASELINE_TOOLCHAIN_DIGEST",
		"run_logged gate-cli-identity verify_gate_cli_identity",
		"gate CLI mode: reuse; source=%s; elapsed_seconds=0",
		"gate CLI mode: compile; source=%s; elapsed_seconds=%s",
	})
}

func assertRemoteBaselineSeedOfflineDependencyFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedContains(t, []string{
		"verify_source_tree_clean()",
		"module_lock_manifest()",
		"runtime_dependency_manifest()",
		"unsupported runtime dependency lock schema",
		"test \"$BASELINE_FORCE_RUNTIME_REFRESH\" != true",
		"git -C \"$1\" ls-files -s -- go.mod go.sum '*/go.mod' '*/go.sum'",
		"validate_offline_module_cache()",
		"module_download_root=$stage/module-download-source",
		"cp -a \"$source_root\" \"$module_download_root\"",
		"download_go_module()",
		"GOMODCACHE=\"$go_mod_cache\" go mod download all",
		"download_go_module \"$module_download_root\" \"$source_root\"",
		"download_locked_module_proxy()",
		"git -C \"$source_root\" ls-files -- '*/go.mod'",
		"baseline source tree mutated:",
		"validate_reusable_runtime()",
		"go build -mod=readonly -trimpath -buildvcs=false",
		"GOPROXY=off GOSUMDB=off",
		"worker_source=" + gatecontract.ExecutorGoWorkloadSourcePath,
		"go test -mod=readonly -exec=true -run '^$' ./...",
		"go test -mod=readonly -tags=e2e -exec=true -run '^$' ./cmd/mcp-lsp",
		"worker race-package-patterns",
		"BASELINE_SEED_GO_PARALLELISM",
		"BASELINE_SEED_GO_MEMORY_LIMIT",
		"GOFLAGS=\"-p=$BASELINE_SEED_GO_PARALLELISM\" GOMAXPROCS=\"$BASELINE_SEED_GO_PARALLELISM\" GOMEMLIMIT=\"$BASELINE_SEED_GO_MEMORY_LIMIT\"",
		"go test -mod=readonly -race -exec=true -run '^$' \"$@\"",
		"GOPROXY=off GOSUMDB=off GOMODCACHE=\"$payload_root/runtime/go-mod-cache\" GOCACHE=\"$go_build_cache\"",
		"\"$payload_root/bin/super-dolphin-gate\" worker runtime-seed verify \"$source_root\" $payload_root/runtime",
		"$payload_root/bin/super-dolphin-gate worker runtime-seed write \"$payload_root/source\" $payload_root/runtime",
	})
}

func assertRemoteBaselineSeedRuntimeReuseFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedContains(t, []string{
		"runtime seed cache is stale; rebuilding",
		"previous-runtime-dependencies",
		"current-runtime-dependencies",
		"runtime_validation_go_mod_cache=$stage/runtime-reuse-go-mod-cache",
		"worker go-module-overlay",
		"GOMODCACHE=\"$runtime_validation_go_mod_cache\" go list -deps -test ./...",
		"previous_runtime=$stage/previous-runtime",
		"reuse_go_dependencies=0",
		"reuse_runtime_rootfs=0",
		"runtime rootfs reused",
		"runtime toolchain reused: go",
		"runtime toolchain reused: node",
		"runtime toolchain reused: python",
		"if test -n \"$previous_runtime\" && test -d \"$previous_runtime/go-mod-cache\"; then",
		"runtime dependency cache reused: go modules",
		"runtime dependency cache reused: Go module proxy",
		"runtime dependency cache reused: frontend node_modules and npm cache",
		"runtime dependency cache reused: lsp node_modules",
		"runtime dependency cache reused: Go tools",
		"runtime dependency cache reused: sqruff",
		"mv \"$previous_runtime/go-mod-cache\" \"$go_mod_cache\"",
		"mv \"$go_mod_cache\" $payload_root/runtime/go-mod-cache",
		"runtime seed changed but no incremental runtime layer was produced; full Anchor rebuild is forbidden",
		"test \"$BASELINE_STORAGE_MODE\" = delta && test \"$runtime_layer_reusable\" != 1",
		"chmod 0755 $payload_root/runtime/bin/portable-tool",
		"tool=${0##*/}",
		"xauth|xkbcomp|xvfb-run",
		"cat > $payload_root/runtime/bin/xvfb-run <<'EOF'",
		"-ac -nolisten tcp",
		"-xkbdir \"$runtime_root/rootfs/usr/share/X11/xkb\"",
		"test -S \"/tmp/.X11-unix/X$servernum\"",
		"portable Xvfb did not publish $display",
		"[baseline-seed] portable runtime validated",
		"SUPER_DOLPHIN_RUNTIME_ROOT",
		"LD_LIBRARY_PATH=$runtime_system_root/usr/lib/$runtime_multiarch:$runtime_system_root/lib/$runtime_multiarch:$runtime_system_root/usr/lib:$runtime_system_root/lib",
		"fontconfig fonts-liberation",
		"test -f /etc/fonts/fonts.conf",
		"test -d /usr/share/fonts",
		"$payload_root/runtime/rootfs/usr/share/fonts",
		"libwebkit2gtk-4.1-dev",
		"libnspr4 libnss3",
		"etc/fonts etc/ssl",
		"x11-xkb-utils xauth xkb-data xvfb",
		"source_manifest=/input/source-manifest.json",
		"git clone --quiet --no-checkout /input/source.bundle",
		"git -C \"$source_root\" fetch --quiet /input/source.bundle",
		"cp /input/sqruff.tar.gz \"$stage/sqruff.tar.gz\"",
		"npm ci --ignore-scripts --no-audit --no-fund",
		"test -d \"$stage/npm-cache/_cacache/content-v2\"",
		"test -d \"$stage/npm-cache/_cacache/index-v5\"",
		"mv \"$stage/npm-cache\" $payload_root/runtime/frontend/npm-cache",
		"mkdir -p $payload_root/runtime/frontend",
		"playwright_browsers=$playwright_modules/.cache/ms-playwright",
		"\"$playwright_cli\" install chromium",
		"runtime dependency cache ready: Playwright Chromium",
		"chromium_real=${chromium_executable}.super-dolphin-real",
		"test -f \"$system_root/etc/fonts/fonts.conf\"",
		"FONTCONFIG_SYSROOT=$system_root",
		"FONTCONFIG_PATH=$system_root/etc/fonts",
		"XDG_DATA_DIRS=$system_root/usr/local/share:$system_root/usr/share",
		"GSETTINGS_SCHEMA_DIR=$system_root/usr/share/glib-2.0/schemas",
		"exec \"$real\" \"$@\"",
		"playwright-chromium-probe",
		"await browser.newPage()",
		"await page.screenshot()",
		"tool_go_mod_cache=$stage/tool-go-mod-cache",
		"GOMODCACHE=\"$tool_go_mod_cache\"",
		"go_mod_cache=$payload_root/runtime/go-mod-cache",
		"go_build_cache=$payload_root/cache-seed/go-build",
		"install -d -m 0700 \"$go_build_cache\"",
		"chmod 0700 \"$go_build_cache\"",
	})
}

func assertRemoteBaselineSeedLayerFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedContains(t, []string{
		"runtime frontend cache is missing Playwright CLI; rebuilding frontend dependencies",
		"reuse_frontend_dependencies=0",
		"runtime frontend dependencies are missing the Playwright CLI",
		"Playwright Chromium install did not produce an executable",
		"Playwright Chromium wrapper is missing its real executable",
		"test \"$BASELINE_STORAGE_MODE\" = delta",
		"BASELINE_DELTA_MANIFEST_1 BASELINE_DELTA_MANIFEST_2 BASELINE_DELTA_MANIFEST_3 BASELINE_DELTA_MANIFEST_4",
		"SUPER_DOLPHIN_RUNTIME_ROOT=$payload_root/runtime \"$payload_root/runtime/bin/git\" -C \"$payload_root/source\" fetch",
		"layer_root=/layers/$generation/output",
		"source.delta.bundle",
		"go-build-cache.delta.tar.gz",
		"go build cache source: private delta",
		"go build cache source: empty toolchain-scoped delta",
		"runtime-go.delta.tar.gz",
		"archive_layer \"$runtime_go_archive_path\" runtime/go runtime/manifest.json",
		"runtime_go_delta=$layer_root/runtime-go.delta.tar.gz",
		"rm -rf \"$previous_go\" \"$previous_manifest\" \"$go_build_cache\"",
		"member.issym() or member.islnk()",
		"BASELINE_DIRECT_CACHE_LAYER_COUNT",
		"direct_layer_root=/direct-cache-layers/layer-$direct_layer_index/cache-seed/go-build",
		"go_cache_proxy=\"/previous/bin/super-dolphin-gate worker go-cache-proxy\"",
		"go_cache_proxy=\"$go_cache_proxy --seed $direct_layer_root\"",
		"export GOCACHEPROG=\"$go_cache_proxy --private $go_build_cache\"",
		"previous baseline schema or storage mode is incompatible; full Anchor rebuild is forbidden",
		"test \"$anchor_schema\" -lt \"$BASELINE_MANIFEST_MIN_COMPATIBLE_VERSION\"",
		"for archive in runtime-deps.tar.gz source.tar.gz go-build-cache.tar.gz; do",
		"tar -xzf \"/previous/$archive\" -C \"$payload_root\"",
		"previous_layered=1",
		"tar -xzf /previous/baseline.tar.gz -C \"$payload_root\"",
		"tar -xf /previous/baseline.tar -C \"$payload_root\"",
		"test -n \"$(find \"$go_build_cache\" -type f -print -quit)\"",
		"go build cache source: previous DataCache",
		"go build cache source: empty bootstrap",
		"go build cache mode: unchanged reuse; refresh skipped",
		"test \"$BASELINE_SOURCE_MODE\" = reuse ||",
		"test \"$BASELINE_SOURCE_MODE\" = delta && test \"$BASELINE_SOURCE_BASE_TREE\" = \"$BASELINE_MAIN_TREE\"",
		"go build cache mode: incremental refresh",
		"go build cache mode: bootstrap refresh",
		"run_logged go-cache-normal-compile compile_go_cache_normal",
		"run_logged go-cache-e2e-compile compile_go_cache_e2e",
		"run_logged go-cache-race-compile compile_go_cache_race",
		"slow_threshold_ms=100000",
		"seed stage slow: %s elapsed_ms=%s threshold_ms=%s",
		"go_cache_compile normal env",
		"go_cache_compile e2e env",
		"go_cache_compile race env",
		"archive_layer()",
		"--use-compress-program='gzip -1 -n'",
		"runtime_archive_path=$oss_output/runtime-deps.tar.gz",
		"source_archive_path=$oss_output/source.tar.gz",
		"go_cache_archive_path=$oss_output/go-build-cache.tar.gz",
		"run_logged layer-archive-runtime archive_layer",
		"run_logged layer-measure-runtime measure_layer",
		"run_logged layer-archive-source archive_layer",
		"run_logged layer-measure-source measure_layer",
		"run_logged layer-archive-go-cache archive_layer",
		"run_logged layer-measure-go-cache measure_layer",
		"run_logged layer-archive-go-cache-delta archive_layer",
		"run_logged layer-measure-go-cache-delta measure_layer",
		"direct_cache_root=$oss_output/direct-cache/cache-seed/go-build",
		"go direct cache publish: current private delta only",
		"go direct cache migration: current accessed working set only",
		"cp -a \"$go_build_cache/.\" \"$direct_cache_root/\"",
		"chmod -R a+rX,a-w \"$direct_cache_root\"",
		"DIRECT_CACHE_MANIFEST=\"$oss_output/direct-cache/manifest.json\"",
		"\"runtime_go_sha256\": os.environ[\"RUNTIME_GO_SHA256\"]",
		"\"tree_sha256\": \"sha256:\" + tree.hexdigest()",
		"\"storage_mode\":\"anchor\"",
		"\"storage_mode\":\"delta\"",
		"\"kind\":\"anchor\",\"name\":\"runtime-deps\",\"archive\":\"runtime-deps.tar.gz\"",
		"\"kind\":\"delta\",\"name\":\"source\",\"archive\":\"source.delta.bundle\"",
		"\"kind\":\"delta\",\"name\":\"go-build-cache\",\"archive\":\"go-build-cache.delta.tar.gz\"",
		"\"gate_binary_size\":$gate_size",
		"\"gate_source_sha256\":\"$BASELINE_GATE_SOURCE_SHA256\"",
		"ca_bundle=$payload_root/runtime/rootfs/etc/ssl/certs/ca-certificates.crt",
		"cp \"$ca_bundle\" \"$oss_output/ca-certificates.crt\"",
		"\"ca_bundle_sha256\":\"$ca_bundle_digest\"",
		"\"ca_bundle_size\":$ca_bundle_size",
		"SUPER_DOLPHIN_BASELINE_READY",
	})
}

func assertRemoteBaselineSeedContains(t *testing.T, fragments []string) {
	t.Helper()
	for _, required := range fragments {
		if !strings.Contains(remoteBaselineSeedScript, required) {
			t.Fatalf("seed script is missing %q", required)
		}
	}
}

func assertRemoteBaselineSeedForbiddenFragments(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedForbiddenScriptFragments(t)
	assertRemoteBaselineSeedForbiddenDuplicateFragments(t)
	assertRemoteBaselineSeedForbiddenExecutorFragments(t)
}

func assertRemoteBaselineSeedForbiddenScriptFragments(t *testing.T) {
	t.Helper()
	for _, forbidden := range []struct {
		fragment string
		message  string
	}{
		{"BASELINE_REPOSITORY_URL", "remote baseline seed must not pull source from GitHub"},
		{"BASELINE_GO_TOOLCHAIN=go1.26.5", "remote baseline seed must derive the Go toolchain from the candidate tree"},
		{"BASELINE_SQRUFF_URL", "remote baseline seed must receive the fixed sqruff artifact through OSS input"},
		{"go mod download -json all", "remote baseline seed must resolve only source-supported target modules and must not mutate go.sum"},
		{"GOPROXY=file://$payload_root/runtime/go-proxy", "accepted Go module cache must fail fast instead of rematerializing through the runtime proxy"},
		{"GOMODCACHE=\"$payload_root/runtime/go-mod-cache\" go list -deps -test", "runtime reuse validation must not mutate the accepted Go module cache"},
		{"worker runtime-seed write \"$source_root\"", "runtime seed manifest must bind the immutable archived source, not the mutable build checkout"},
		{"cp -a $previous_root/. /output/", "remote baseline seed must not write unpacked trees through OSSFS"},
		{"cp -a \"$source_root\" /output/source", "remote baseline seed must not write unpacked trees through OSSFS"},
		{"prewarm_go_build_cache", "accepted build cache must be incrementally refreshed rather than described as fully prewarmed"},
		{"\n  rm -rf $payload_root/runtime\n", "runtime dependency changes must reuse accepted immutable components instead of deleting the full runtime"},
		{"cp -a \"$previous_runtime/go-mod-cache/.\" \"$go_mod_cache/\"", "incremental refresh must move the accepted module cache instead of duplicating it"},
		{"cp -a \"$go_mod_cache\" $payload_root/runtime/go-mod-cache", "incremental refresh must not keep a second module-cache tree"},
		{"cp -a \"$previous_runtime/frontend\" $payload_root/runtime/frontend", "incremental refresh must move accepted frontend dependencies"},
		{"cp -a \"$previous_runtime/lsp\" $payload_root/runtime/lsp", "incremental refresh must move accepted LSP dependencies"},
		{"archive_path=$oss_output/baseline.tar.gz", "new baseline generations must use deterministic layered archives"},
		{"\"archive_sha256\"", "new baseline manifests must not emit legacy single-archive fields"},
		{"$source_root/.git/shallow", "remote baseline source must retain the complete history reachable from main"},
		{"tool=$(basename \"$0\")", "portable runtime tools must not depend on the host basename command"},
		{"BASELINE_STORAGE_MODE=anchor", "a previous baseline must never fall back to a full Anchor rebuild"},
		{"timeout --signal=KILL", "100-second cache targets are slow-stage ledger thresholds, not correctness timeouts"},
		{"cp -a \"$stage/anchor-go-build-cache/.\" \"$direct_cache_root/\"", "direct cache migration must publish only accessed entries, never copy the historical anchor cache"},
	} {
		if strings.Contains(remoteBaselineSeedScript, forbidden.fragment) {
			t.Fatal(forbidden.message)
		}
	}
}

func assertRemoteBaselineSeedForbiddenDuplicateFragments(t *testing.T) {
	t.Helper()
	for _, duplicate := range []string{
		"cp /previous/runtime-deps.tar.gz",
		"cp /previous/source.tar.gz",
		"cp /previous/go-build-cache.tar.gz",
		"previous_root=",
		"cp -a $previous_root/.",
		"go_build_cache=$stage/go-build-cache",
		"cp -a \"$go_build_cache/.\" $payload_root/cache-seed/go-build/",
	} {
		if strings.Contains(remoteBaselineSeedScript, duplicate) {
			t.Fatalf("remote baseline seed must reuse the accepted payload in place; found %q", duplicate)
		}
	}
}

func assertRemoteBaselineSeedForbiddenExecutorFragments(t *testing.T) {
	t.Helper()
	if strings.Contains(remoteBaselineSeedScript, "super-dolphin-gate-executor") ||
		strings.Contains(remoteBaselineSeedScript, "executor_binary_") ||
		strings.Contains(remoteBaselineSeedScript, "super-dolphin-runtime-seed") {
		t.Fatal("remote baseline seed must emit only super-dolphin-gate")
	}
}

func assertRemoteBaselineSeedCacheOrdering(t *testing.T) {
	t.Helper()
	assertRemoteBaselineSeedBuildOrdering(t)
	assertRemoteBaselineSeedArchiveOrdering(t)
	assertRemoteBaselineSeedDeltaReuseOrdering(t)
	assertRemoteBaselineSeedRuntimeGoChainOrdering(t)
	assertRemoteBaselineSeedOfflineValidationCount(t)
	assertRemoteBaselineSeedFrontendOrdering(t)
	assertRemoteBaselineSeedGoEmbedOrdering(t)
	assertRemoteBaselineSeedRuntimeReuseOrdering(t)
}

func assertRemoteBaselineSeedRuntimeGoChainOrdering(t *testing.T) {
	t.Helper()
	detect := strings.Index(remoteBaselineSeedScript, `runtime_go_count=$(grep -o '"name":"runtime-go"'`)
	validate := strings.Index(remoteBaselineSeedScript, `member.issym() or member.islnk()`)
	publishGo := strings.Index(remoteBaselineSeedScript, `mv "$runtime_go_stage/runtime/go" "$payload_root/runtime/go"`)
	publishManifest := strings.Index(remoteBaselineSeedScript, `mv "$runtime_go_stage/runtime/manifest.json" "$payload_root/runtime/manifest.json"`)
	resetCache := strings.Index(remoteBaselineSeedScript, `rm -rf "$previous_go" "$previous_manifest" "$go_build_cache"`)
	restoreCurrentCache := strings.Index(remoteBaselineSeedScript, `tar -xzf "$cache_delta" -C "$payload_root"`)
	if detect < 0 || validate < detect || publishGo < validate || publishManifest < publishGo || resetCache < publishManifest || restoreCurrentCache < resetCache {
		t.Fatal("runtime-go delta must be validated and published before old caches are discarded and the current toolchain cache is restored")
	}
}

func assertRemoteBaselineSeedBuildOrdering(t *testing.T) {
	t.Helper()
	embedSeed := strings.Index(remoteBaselineSeedScript, `cp "$payload_root/frontend-embed/index.html" "$source_root/cmd/agent-terminal/web-dist/index.html"`)
	reuseDecision := strings.Index(remoteBaselineSeedScript, "gate_cli_ready=0")
	buildFunction := strings.Index(remoteBaselineSeedScript, "build_gate_cli()")
	earlyCompile := strings.Index(remoteBaselineSeedScript, `if test "$gate_cli_ready" = 0 && test "$seeds_changed" = 0`)
	runtimeReady := strings.LastIndex(remoteBaselineSeedScript, "use_runtime $payload_root/runtime")
	finalCompile := strings.LastIndex(remoteBaselineSeedScript, `if test "$gate_cli_ready" != 1; then`)
	manifestReset := strings.LastIndex(remoteBaselineSeedScript, "rm -f $payload_root/runtime/manifest.json")
	manifestRefresh := strings.LastIndex(remoteBaselineSeedScript, "worker runtime-seed write \"$payload_root/source\"")
	manifestPrefix := remoteBaselineSeedScript[:manifestRefresh]
	sourceVerify := strings.LastIndex(manifestPrefix, "\nverify_source_tree_clean\n")
	assertRemoteBaselineConditions(t, "baseline gate CLI ordering", embedSeed >= 0, reuseDecision >= embedSeed, buildFunction >= reuseDecision, earlyCompile >= buildFunction, runtimeReady >= earlyCompile, finalCompile >= runtimeReady, sourceVerify >= finalCompile, manifestReset >= sourceVerify, manifestRefresh >= manifestReset)
	if count := countRemoteSeedShellCommand(remoteBaselineSeedScript, "compile_gate_cli"); count != 1 {
		t.Fatalf("unconditional gate CLI compile calls = %d, want 1 guarded fallback", count)
	}
	if count := strings.Count(remoteBaselineSeedScript, "if ! compile_gate_cli; then"); count != 1 {
		t.Fatalf("early gate CLI compile attempts = %d, want 1 guarded fast path", count)
	}
}

func assertRemoteBaselineSeedArchiveOrdering(t *testing.T) {
	t.Helper()
	manifestRefresh := strings.LastIndex(remoteBaselineSeedScript, "worker runtime-seed write \"$payload_root/source\"")
	manifestVerify := strings.LastIndex(remoteBaselineSeedScript, "worker runtime-seed verify \"$payload_root/source\"")
	unchangedReuse := strings.LastIndex(remoteBaselineSeedScript, "go build cache mode: unchanged reuse; refresh skipped")
	incrementalRefresh := strings.LastIndex(remoteBaselineSeedScript, "go build cache mode: incremental refresh")
	bootstrapRefresh := strings.LastIndex(remoteBaselineSeedScript, "go build cache mode: bootstrap refresh")
	finalRefresh := strings.LastIndex(remoteBaselineSeedScript, "\n  refresh_go_build_cache\n")
	buildCacheReused := strings.Contains(remoteBaselineSeedScript, "go_build_cache=$payload_root/cache-seed/go-build")
	sourceVerify := strings.LastIndex(remoteBaselineSeedScript, "\nverify_source_tree_clean\n")
	finalPermissions := strings.LastIndex(remoteBaselineSeedScript, "chmod -R a+rX $payload_root")
	archiveBuild := strings.LastIndex(remoteBaselineSeedScript, "runtime_archive_path=$oss_output/runtime-deps.tar.gz")
	if !buildCacheReused || incrementalRefresh < unchangedReuse || bootstrapRefresh < incrementalRefresh ||
		finalRefresh < bootstrapRefresh || sourceVerify < finalRefresh || finalPermissions < sourceVerify || manifestRefresh < finalPermissions ||
		manifestVerify < manifestRefresh ||
		archiveBuild < manifestVerify {
		t.Fatal("baseline must refresh private cache overlays, normalize permissions, and seal the final runtime manifest before archiving")
	}
}

func assertRemoteBaselineSeedDeltaReuseOrdering(t *testing.T) {
	t.Helper()
	restorePriorDelta := strings.Index(remoteBaselineSeedScript, "tar -xzf \"$cache_delta\" -C \"$payload_root\"")
	directLayerCount := strings.Index(remoteBaselineSeedScript, "direct_layer_count=$BASELINE_DIRECT_CACHE_LAYER_COUNT")
	directLayerRoot := strings.Index(remoteBaselineSeedScript, "direct_layer_root=/direct-cache-layers/layer-$direct_layer_index/cache-seed/go-build")
	enableLayeredReads := strings.Index(remoteBaselineSeedScript, "go_cache_proxy=\"$go_cache_proxy --seed $direct_layer_root\"")
	incrementalRefresh := strings.Index(remoteBaselineSeedScript, "go build cache mode: incremental refresh")
	incrementalRefreshComplete := strings.Index(remoteBaselineSeedScript[incrementalRefresh:], "\n  refresh_go_build_cache\n")
	archiveDelta := strings.Index(remoteBaselineSeedScript, "go_cache_archive_path=$oss_output/go-build-cache.delta.tar.gz")
	if restorePriorDelta < 0 || directLayerCount < restorePriorDelta || directLayerRoot < directLayerCount || enableLayeredReads < directLayerRoot ||
		incrementalRefresh < enableLayeredReads || incrementalRefreshComplete < 0 || archiveDelta < incrementalRefresh+incrementalRefreshComplete {
		t.Fatal("changed source must query mounted direct layers newest-first and archive only the current private miss layer")
	}
}

func assertRemoteBaselineSeedOfflineValidationCount(t *testing.T) {
	t.Helper()
	if count := strings.Count(remoteBaselineSeedScript, "GOPROXY=off GOSUMDB=off"); count != 5 {
		t.Fatalf("offline CLI compile, accepted-cache validations, and incremental refreshes = %d, want 5", count)
	}
	overlay := strings.Index(remoteBaselineSeedScript, "worker go-module-overlay")
	offlineList := strings.Index(remoteBaselineSeedScript, "GOMODCACHE=\"$runtime_validation_go_mod_cache\" go list -deps -test ./...")
	manifestVerify := strings.Index(remoteBaselineSeedScript, "worker runtime-seed verify \"$source_root\"")
	if overlay < 0 || offlineList < overlay || manifestVerify < offlineList {
		t.Fatal("runtime reuse validation must route Go metadata writes through a private overlay before verifying the immutable manifest")
	}
}

func assertRemoteBaselineSeedFrontendOrdering(t *testing.T) {
	t.Helper()
	frontendReuseCheck := strings.Index(remoteBaselineSeedScript, "runtime frontend cache is missing Playwright CLI")
	reuseValidation := strings.Index(remoteBaselineSeedScript, "validate_reusable_runtime >\"$reuse_log\"")
	frontendRoot := strings.Index(remoteBaselineSeedScript, "mkdir -p $payload_root/runtime/frontend")
	frontendMove := strings.Index(remoteBaselineSeedScript, "mv \"$source_root/frontend-app/node_modules\" $payload_root/runtime/frontend/node_modules")
	multiarchReady := strings.Index(remoteBaselineSeedScript, "printf '%s\\n' \"$runtime_multiarch\" > $payload_root/runtime/multiarch")
	runtimeReady := -1
	if multiarchReady >= 0 {
		if offset := strings.Index(remoteBaselineSeedScript[multiarchReady:], "use_runtime $payload_root/runtime"); offset >= 0 {
			runtimeReady = multiarchReady + offset
		}
	}
	chromiumWrapper := strings.Index(remoteBaselineSeedScript, "chromium_real=${chromium_executable}.super-dolphin-real")
	playwrightProbe := strings.Index(remoteBaselineSeedScript, "playwright-chromium-probe")
	if frontendReuseCheck < 0 || reuseValidation < frontendReuseCheck || frontendRoot < reuseValidation ||
		frontendMove < frontendRoot || multiarchReady < 0 ||
		runtimeReady < multiarchReady || chromiumWrapper < runtimeReady || playwrightProbe < chromiumWrapper {
		t.Fatal("baseline seed must prepare the frontend runtime and portable libraries before probing Chromium")
	}
}

func assertRemoteBaselineSeedGoEmbedOrdering(t *testing.T) {
	t.Helper()
	embedSeed := strings.Index(remoteBaselineSeedScript, `cp "$payload_root/frontend-embed/index.html" "$source_root/cmd/agent-terminal/web-dist/index.html"`)
	reuseValidation := strings.Index(remoteBaselineSeedScript, "if test \"$seeds_changed\" = 0; then")
	onlineHydration := strings.Index(remoteBaselineSeedScript, "download_go_module \"$module_download_root\" \"$source_root\"")
	if embedSeed < 0 || reuseValidation < embedSeed || onlineHydration < reuseValidation {
		t.Fatal("baseline seed must create the ignored Go embed placeholder before reusable-runtime validation and online dependency hydration")
	}
}

func assertRemoteBaselineSeedRuntimeReuseOrdering(t *testing.T) {
	t.Helper()
	preserveRuntime := strings.Index(remoteBaselineSeedScript, `mv $payload_root/runtime "$previous_runtime"`)
	reuseGo := strings.Index(remoteBaselineSeedScript, "runtime toolchain reused: go")
	downloadGo := strings.Index(remoteBaselineSeedScript, `download_file "$stage/go.tar.gz"`)
	reuseModules := strings.Index(remoteBaselineSeedScript, "runtime dependency cache reused: go modules")
	hydrateModules := strings.Index(remoteBaselineSeedScript, `download_go_module "$module_download_root" "$source_root"`)
	retireRuntime := strings.LastIndex(remoteBaselineSeedScript, `rm -rf "$previous_runtime"`)
	if preserveRuntime < 0 || reuseGo < preserveRuntime || downloadGo < reuseGo ||
		reuseModules < preserveRuntime || hydrateModules < reuseModules || retireRuntime < hydrateModules {
		t.Fatal("runtime changes must preserve the accepted runtime, reuse immutable components before downloading misses, and retire it only after hydration")
	}
}

func TestRemoteBaselinePolicyDigestBindsRegistrySeedScriptAndRuntimeInputs(t *testing.T) {
	first := remoteBaselinePolicyDigest("sha256:"+repeatRemoteHex("a", 64), "sha256:"+repeatRemoteHex("c", 64))
	registryChanged := remoteBaselinePolicyDigest("sha256:"+repeatRemoteHex("b", 64), "sha256:"+repeatRemoteHex("c", 64))
	runtimeInputsChanged := remoteBaselinePolicyDigest("sha256:"+repeatRemoteHex("a", 64), "sha256:"+repeatRemoteHex("d", 64))
	if first == registryChanged || first == runtimeInputsChanged || len(first) != len("sha256:")+64 ||
		!strings.HasPrefix(first, "sha256:") {
		t.Fatalf("policy digests = %q, %q, %q", first, registryChanged, runtimeInputsChanged)
	}
}

func TestResolveRemoteSqruffArtifact(t *testing.T) {
	args := []string{
		"SQRUFF_ARCHIVE_URL_AMD64=https://example.invalid/amd64.tar.gz",
		"SQRUFF_ARCHIVE_SHA256_AMD64=" + repeatRemoteHex("a", 64),
		"SQRUFF_ARCHIVE_URL_ARM64=https://example.invalid/arm64.tar.gz",
		"SQRUFF_ARCHIVE_SHA256_ARM64=" + repeatRemoteHex("b", 64),
	}
	artifactURL, digest, err := resolveRemoteSqruffArtifact(args, "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	if artifactURL != "https://example.invalid/arm64.tar.gz" ||
		digest != repeatRemoteHex("b", 64) {
		t.Fatalf("artifact = %q %q", artifactURL, digest)
	}
}

func TestDownloadRemoteBaselineToolArtifactVerifiesDigest(t *testing.T) {
	content := []byte("verified sqruff fixture")
	digest := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if _, err := writer.Write(content); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()
	path, err := downloadRemoteBaselineToolArtifact(context.Background(), t.TempDir(), server.URL, fmt.Sprintf("%x", digest))
	if err != nil {
		t.Fatalf("downloadRemoteBaselineToolArtifact() error = %v", err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(content) {
		t.Fatalf("artifact content = %q, want %q", actual, content)
	}
}

func TestDownloadRemoteBaselineToolArtifactRetriesTransientEOF(t *testing.T) {
	content := []byte("verified sqruff retry fixture")
	digest := sha256.Sum256(content)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			writer.Header().Set("Content-Length", strconv.Itoa(len(content)))
			if _, err := writer.Write(content[:len(content)/2]); err != nil {
				t.Errorf("write partial fixture response: %v", err)
			}
			return
		}
		if _, err := writer.Write(content); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()
	path, err := downloadRemoteBaselineToolArtifact(context.Background(), t.TempDir(), server.URL, fmt.Sprintf("%x", digest))
	if err != nil {
		t.Fatalf("downloadRemoteBaselineToolArtifact() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("download attempts = %d, want 2", attempts.Load())
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(content) {
		t.Fatalf("artifact content = %q, want %q", actual, content)
	}
}
