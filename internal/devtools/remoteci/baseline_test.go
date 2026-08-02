package remoteci

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestBaselineStateOCIOnlyRoundTrip(t *testing.T) {
	state := validBaselineState()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var loaded BaselineState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if err := loaded.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !loaded.Matches(loaded.identity()) {
		t.Fatal("loaded OCI state does not match its identity")
	}
}

func TestBaselineStateRejectsUnknownAndMultipleJSONValues(t *testing.T) {
	data, err := json.Marshal(validBaselineState())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{append(data[:len(data)-1], []byte(`,"data_cache_id":"edc-legacy"}`)...), append(data, []byte(` {}`)...)} {
		var state BaselineState
		if err := json.Unmarshal(input, &state); err == nil {
			t.Fatal("UnmarshalJSON() accepted invalid wire data")
		}
	}
}

func TestBaselineStateValidationRequiresImageCacheAuthority(t *testing.T) {
	for name, mutate := range map[string]func(*BaselineState){
		"missing ID":          func(state *BaselineState) { state.ImageCacheID = "" },
		"missing snapshot":    func(state *BaselineState) { state.ImageCacheSnapshotID = "" },
		"not ready":           func(state *BaselineState) { state.ImageCacheReady = false },
		"wrong image digest":  func(state *BaselineState) { state.ImageDigest = digest("f") },
		"missing description": func(state *BaselineState) { state.OCIProjectCache = nil },
		"wrong source path":   func(state *BaselineState) { state.SourceSnapshotImagePath = "/tmp/source-snapshot/manifest.json" },
	} {
		t.Run(name, func(t *testing.T) {
			state := validBaselineState()
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() accepted incomplete ECI image cache authority")
			}
		})
	}
}

func TestBaselineStateRejectsACRRuntimeAndOCIProjectCacheImages(t *testing.T) {
	for name, mutate := range map[string]func(*BaselineState){
		"runtime root": func(state *BaselineState) { state.RuntimeImage = "aliyuncs.com/runtime@" + digest("e") },
		"runtime port": func(state *BaselineState) {
			state.RuntimeImage = "registry.cn-shenzhen.aliyuncs.com:5000/runtime@" + digest("e")
		},
		"runtime trailing dot": func(state *BaselineState) {
			state.RuntimeImage = "registry.cn-shenzhen.aliyuncs.com./runtime@" + digest("e")
		},
		"OCI cache": func(state *BaselineState) {
			state.OCIProjectCache = validBaselineOCIProjectCache(state.MainTree, state.ToolchainDigest, state.Platform, "registry.cn-shenzhen.aliyuncs.com/runtime@"+digest("e"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := validBaselineState()
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate accepted an Alibaba Cloud ACR image")
			}
		})
	}
}

func TestBaselineStateFieldRegistry(t *testing.T) {
	if BaselineStateSchemaVersion != 11 {
		t.Fatalf("BaselineStateSchemaVersion = %d", BaselineStateSchemaVersion)
	}
	assertBaselineFields(t, reflect.TypeFor[BaselineState](), []string{"SchemaVersion", "Generation", "MainCommit", "MainTree", "Platform", "PolicyDigest", "ToolchainDigest", "RuntimeImage", "ImageCacheID", "ImageCacheSnapshotID", "ImageCacheReady", "ImageDigest", "OCIProjectCache", "GateBinarySHA256", "RuntimeSeedSHA256", "BaselineManifestDigest", "SourceSnapshotManifestDigest", "SourceSnapshotImagePath", "SourceSnapshotClosureDigest", "CreatedAt", "AcceptedAt", "RenewedAt"})
	assertBaselineFields(t, reflect.TypeFor[BaselineOCIProjectCache](), []string{"Image", "ContentManifestSHA256", "MainTree", "ToolchainDigest", "Platform", "CachePath"})
}

func validBaselineState() BaselineState {
	created := time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC)
	mainTree, toolchain, runtimeImage := strings.Repeat("b", 40), digest("d"), "registry.example/runtime@"+digest("e")
	accepted := created.Add(3 * time.Minute)
	return BaselineState{SchemaVersion: BaselineStateSchemaVersion, Generation: 3, MainCommit: strings.Repeat("a", 40), MainTree: mainTree, Platform: "linux/amd64", PolicyDigest: digest("c"), ToolchainDigest: toolchain, RuntimeImage: runtimeImage, ImageCacheID: "imc-baseline-3", ImageCacheSnapshotID: "snap-baseline-3", ImageCacheReady: true, ImageDigest: digest("e"), OCIProjectCache: validBaselineOCIProjectCache(mainTree, toolchain, "linux/amd64", runtimeImage), GateBinarySHA256: digest("1"), RuntimeSeedSHA256: digest("2"), BaselineManifestDigest: digest("3"), SourceSnapshotManifestDigest: digest("4"), SourceSnapshotImagePath: cicontract.SourceSnapshotManifestPath, SourceSnapshotClosureDigest: digest("5"), CreatedAt: created, AcceptedAt: accepted, RenewedAt: accepted}
}

func validBaselineOCIProjectCache(mainTree, toolchainDigest, platform, runtimeImage string) *BaselineOCIProjectCache {
	return &BaselineOCIProjectCache{Image: runtimeImage, ContentManifestSHA256: digest("a"), MainTree: mainTree, ToolchainDigest: toolchainDigest, Platform: platform, CachePath: OCIProjectGoBuildCachePath}
}

func digest(value string) string { return "sha256:" + strings.Repeat(value, 64) }
func assertBaselineFields(t *testing.T, structType reflect.Type, expected []string) {
	t.Helper()
	actual := make([]string, 0, structType.NumField())
	for field := range structType.Fields() {
		actual = append(actual, field.Name)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s fields = %v, want %v", structType.Name(), actual, expected)
	}
}
