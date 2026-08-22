package cicontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAcceptedBaselineStateStrictRoundTripAndValidate(t *testing.T) {
	state := testAcceptedBaselineState()
	if err := state.Validate(); err != nil {
		t.Fatalf("valid accepted baseline state rejected: %v", err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal accepted baseline state: %v", err)
	}
	decoded, err := DecodeAcceptedBaselineState(encoded)
	if err != nil {
		t.Fatalf("strict accepted baseline round-trip failed: %v", err)
	}
	if !reflect.DeepEqual(decoded, state) {
		t.Fatalf("decoded accepted baseline state differs:\n got=%#v\nwant=%#v", decoded, state)
	}
}

func TestDecodeAcceptedBaselineStateRejectsStrictJSONDrift(t *testing.T) {
	encoded := mustMarshalAcceptedBaselineState(t, testAcceptedBaselineState())
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode test object: %v", err)
	}
	object["unexpected"] = json.RawMessage(`true`)
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("marshal unknown-field fixture: %v", err)
	}

	schemaMarker := fmt.Appendf(nil, `"schema_version":%d`, BaselineStateSchemaVersion)
	duplicateMarker := fmt.Appendf(nil, `,"schema_version":%d`, BaselineStateSchemaVersion)
	duplicate := bytes.Replace(encoded, schemaMarker, append(append([]byte(nil), schemaMarker...), duplicateMarker...), 1)
	trailing := append(append([]byte(nil), encoded...), []byte(`{}`)...)
	runtimeImageJSON, err := json.Marshal(testAcceptedBaselineState().RuntimeImage)
	if err != nil {
		t.Fatalf("marshal runtime image fixture: %v", err)
	}
	runtimeImageField := append([]byte(`"runtime_image":`), runtimeImageJSON...)
	missing := bytes.Replace(encoded, append(append([]byte(nil), runtimeImageField...), ','), nil, 1)
	null := bytes.Replace(encoded, runtimeImageField, []byte(`"runtime_image":null`), 1)

	for name, fixture := range map[string][]byte{
		"unknown field":   unknown,
		"duplicate field": duplicate,
		"trailing JSON":   trailing,
		"missing field":   missing,
		"null field":      null,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAcceptedBaselineState(fixture); err == nil {
				t.Fatalf("DecodeAcceptedBaselineState accepted %s", name)
			}
		})
	}
}

func TestAcceptedBaselineStateRejectsIdentityAndDigestDrift(t *testing.T) {
	base := testAcceptedBaselineState()
	for name, mutate := range map[string]func(*AcceptedBaselineState){
		"schema":              func(state *AcceptedBaselineState) { state.SchemaVersion++ },
		"zero generation":     func(state *AcceptedBaselineState) { state.Generation = 0 },
		"provider":            func(state *AcceptedBaselineState) { state.ExecutionProvider = "docker/v1" },
		"region":              func(state *AcceptedBaselineState) { state.RegionID = "" },
		"commit":              func(state *AcceptedBaselineState) { state.MainCommit = "not-a-git-object" },
		"tree":                func(state *AcceptedBaselineState) { state.MainTree = strings.Repeat("z", 40) },
		"platform":            func(state *AcceptedBaselineState) { state.Platform = "linux/arm64" },
		"policy digest":       func(state *AcceptedBaselineState) { state.PolicyDigest = "sha256:bad" },
		"toolchain digest":    func(state *AcceptedBaselineState) { state.ToolchainDigest = "sha256:bad" },
		"gate digest":         func(state *AcceptedBaselineState) { state.GateBinarySHA256 = "sha256:bad" },
		"runtime seed digest": func(state *AcceptedBaselineState) { state.RuntimeSeedSHA256 = "sha256:bad" },
		"manifest digest":     func(state *AcceptedBaselineState) { state.BaselineManifestDigest = "sha256:bad" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("Validate accepted %s drift", name)
			}
		})
	}
}

func TestAcceptedBaselineStateRejectsImageCacheAndProjectCacheDrift(t *testing.T) {
	base := testAcceptedBaselineState()
	for name, mutate := range map[string]func(*AcceptedBaselineState){
		"mutable runtime image": func(state *AcceptedBaselineState) { state.RuntimeImage = "registry.example/runtime:latest" },
		"image digest mismatch": func(state *AcceptedBaselineState) { state.ImageDigest = testDigest("0") },
		"cache not ready":       func(state *AcceptedBaselineState) { state.ImageCacheReady = false },
		"unsafe cache ID":       func(state *AcceptedBaselineState) { state.ImageCacheID = "../cache" },
		"unsafe snapshot ID":    func(state *AcceptedBaselineState) { state.ImageCacheSnapshotID = "snapshot/cache" },
		"missing project cache": func(state *AcceptedBaselineState) { state.OCIProjectCache = nil },
		"project cache image drift": func(state *AcceptedBaselineState) {
			state.OCIProjectCache.Image = "registry.example/other@sha256:" + strings.Repeat("f", 64)
		},
		"project cache digest drift":    func(state *AcceptedBaselineState) { state.OCIProjectCache.ContentManifestSHA256 = "sha256:bad" },
		"project cache tree drift":      func(state *AcceptedBaselineState) { state.OCIProjectCache.MainTree = strings.Repeat("d", 40) },
		"project cache toolchain drift": func(state *AcceptedBaselineState) { state.OCIProjectCache.ToolchainDigest = testDigest("0") },
		"project cache platform drift":  func(state *AcceptedBaselineState) { state.OCIProjectCache.Platform = "linux/arm64" },
		"project cache path drift":      func(state *AcceptedBaselineState) { state.OCIProjectCache.CachePath = "/tmp/go-build" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.OCIProjectCache = cloneAcceptedBaselineProjectCache(base.OCIProjectCache)
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("Validate accepted %s drift", name)
			}
		})
	}
}

func TestAcceptedBaselineStateRejectsTimestampDrift(t *testing.T) {
	base := testAcceptedBaselineState()
	for name, mutate := range map[string]func(*AcceptedBaselineState){
		"missing created": func(state *AcceptedBaselineState) { state.CreatedAt = time.Time{} },
		"non UTC created": func(state *AcceptedBaselineState) {
			state.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("UTC+1", 3600))
		},
		"accepted before created": func(state *AcceptedBaselineState) { state.AcceptedAt = state.CreatedAt.Add(-time.Second) },
		"renewed before accepted": func(state *AcceptedBaselineState) { state.RenewedAt = state.AcceptedAt.Add(-time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			if mutate != nil {
				mutate(&candidate)
			}
			if err := candidate.Validate(); err == nil {
				t.Fatalf("Validate accepted %s drift", name)
			}
		})
	}
}

func testAcceptedBaselineState() AcceptedBaselineState {
	image := "registry.example/runtime@sha256:" + strings.Repeat("a", 64)
	mainTree := strings.Repeat("c", 40)
	toolchainDigest := testDigest("e")
	return AcceptedBaselineState{
		SchemaVersion: BaselineStateSchemaVersion, Generation: 1,
		ExecutionProvider: ExecutionProviderID, RegionID: "cn-shenzhen",
		MainCommit: strings.Repeat("b", 40), MainTree: mainTree, Platform: TargetPlatform,
		PolicyDigest: testDigest("d"), ToolchainDigest: toolchainDigest, RuntimeImage: image,
		ImageCacheID: "imc-test", ImageCacheSnapshotID: "snapshot-test", ImageCacheReady: true,
		ImageDigest: testDigest("a"), OCIProjectCache: &AcceptedBaselineOCIProjectCache{
			Image: image, ContentManifestSHA256: testDigest("5"), MainTree: mainTree,
			ToolchainDigest: toolchainDigest, Platform: TargetPlatform, CachePath: AcceptedBaselineOCIProjectCachePath,
		},
		GateBinarySHA256: testDigest("f"), RuntimeSeedSHA256: testDigest("1"), BaselineManifestDigest: testDigest("2"),
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		AcceptedAt: time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
		RenewedAt:  time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC),
	}
}

func testDigest(letter string) string { return "sha256:" + strings.Repeat(letter, 64) }

func mustMarshalAcceptedBaselineState(t *testing.T, state AcceptedBaselineState) []byte {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal accepted baseline fixture: %v", err)
	}
	return data
}

func cloneAcceptedBaselineProjectCache(cache *AcceptedBaselineOCIProjectCache) *AcceptedBaselineOCIProjectCache {
	if cache == nil {
		return nil
	}
	clone := *cache
	return &clone
}
