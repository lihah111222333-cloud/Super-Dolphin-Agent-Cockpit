package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestRemoteBaselineRecommendedSizeGiB(t *testing.T) {
	config := remoteRunConfig{}
	config.DataCache.MaxSizeGiB = remoteDataCacheMaximumSizeGiB
	for _, test := range []struct {
		name       string
		payloadGiB int64
		want       int
		wantError  bool
	}{
		{name: "minimum", payloadGiB: 3, want: 20},
		{name: "grows with reserve", payloadGiB: 18, want: 23},
		{name: "below maximum", payloadGiB: 94, want: 99},
		{name: "at maximum", payloadGiB: 95, want: 100},
		{name: "above maximum", payloadGiB: 96, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := remoteBaselineCapacityManifest(test.payloadGiB * remoteDataCacheGiB)
			got, err := remoteBaselineRecommendedSizeGiB(config, manifest)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "exceeding configured maximum") {
					t.Fatalf("remoteBaselineRecommendedSizeGiB() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("remoteBaselineRecommendedSizeGiB() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRemoteBaselinePayloadBytesRejectsOverflow(t *testing.T) {
	manifest := remoteBaselineCapacityManifest(3 * remoteDataCacheGiB)
	manifest.GateBinarySize = int64(^uint64(0) >> 1)
	if _, err := remoteBaselinePayloadBytes(manifest); err == nil ||
		!strings.Contains(err.Error(), "overflows") {
		t.Fatalf("remoteBaselinePayloadBytes() overflow error = %v", err)
	}
}

func TestLoadAcceptedRemoteBaselineRecommendedSizeVerifiesAnchorManifest(t *testing.T) {
	config := remoteRunConfig{}
	config.DataCache.MaxSizeGiB = remoteDataCacheMaximumSizeGiB
	state := remoteBaselineStateFixture()
	manifest := remoteBaselineCapacityManifest(3 * remoteDataCacheGiB)
	manifest.Generation = state.Anchor.Generation
	manifest.MainCommit, manifest.MainTree = state.Anchor.MainCommit, state.Anchor.MainTree
	manifest.Platform, manifest.PolicyDigest = state.Platform, state.PolicyDigest
	manifest.ToolchainDigest, manifest.RuntimeImage = state.ToolchainDigest, state.RuntimeImage
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	state.Anchor.ManifestDigest = remoteci.BaselineManifestDigest(data)
	key := state.Anchor.SourceObjectPrefix + "output/baseline-manifest.json"
	store := &fakeRemoteBaselineOSSStore{downloads: map[string][]byte{key: data}}

	got, err := loadAcceptedRemoteBaselineRecommendedSize(context.Background(), config, store, state)
	if err != nil {
		t.Fatal(err)
	}
	if got != remoteDataCacheMinimumSizeGiB {
		t.Fatalf("accepted recommended size = %d, want %d", got, remoteDataCacheMinimumSizeGiB)
	}
	if len(store.downloadedKeys) != 1 || store.downloadedKeys[0] != key {
		t.Fatalf("downloaded keys = %#v, want %q", store.downloadedKeys, key)
	}

	state.Anchor.ManifestDigest = "sha256:" + repeatRemoteHex("f", 64)
	if _, err := loadAcceptedRemoteBaselineRecommendedSize(context.Background(), config, store, state); err == nil ||
		!strings.Contains(err.Error(), "digest drifted") {
		t.Fatalf("digest drift error = %v", err)
	}
}

func remoteBaselineCapacityManifest(payloadBytes int64) remoteci.BaselineManifest {
	const fixedArtifacts int64 = 2
	layerBytes := payloadBytes - fixedArtifacts
	return remoteci.BaselineManifest{
		SchemaVersion:             remoteci.BaselineManifestSchemaVersion,
		Generation:                1,
		MainCommit:                repeatRemoteHex("1", 40),
		MainTree:                  repeatRemoteHex("2", 40),
		Platform:                  "linux/arm64",
		PolicyDigest:              "sha256:" + repeatRemoteHex("3", 64),
		ToolchainDigest:           "sha256:" + repeatRemoteHex("4", 64),
		RuntimeImage:              "registry.example/runtime@sha256:" + repeatRemoteHex("5", 64),
		GateSourceSHA256:          "sha256:" + repeatRemoteHex("c", 64),
		GateBinarySHA256:          "sha256:" + repeatRemoteHex("6", 64),
		GateBinarySize:            1,
		RuntimeSeedManifestSHA256: "sha256:" + repeatRemoteHex("7", 64),
		CABundleSHA256:            "sha256:" + repeatRemoteHex("8", 64),
		CABundleSize:              1,
		StorageMode:               remoteci.BaselineStorageModeAnchor,
		Layers: []remoteci.BaselineLayer{
			{Generation: 1, Kind: remoteci.BaselineLayerKindAnchor, Name: "runtime-deps", Archive: "runtime-deps.tar.gz", SHA256: "sha256:" + repeatRemoteHex("9", 64), Size: layerBytes - 2},
			{Generation: 1, Kind: remoteci.BaselineLayerKindAnchor, Name: "source", Archive: "source.tar.gz", SHA256: "sha256:" + repeatRemoteHex("a", 64), Size: 1},
			{Generation: 1, Kind: remoteci.BaselineLayerKindAnchor, Name: "go-build-cache", Archive: "go-build-cache.tar.gz", SHA256: "sha256:" + repeatRemoteHex("b", 64), Size: 1},
		},
	}
}
