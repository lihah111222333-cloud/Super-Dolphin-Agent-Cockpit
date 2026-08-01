package main

import (
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func remoteBaselineStateFixture() remoteci.BaselineState {
	created := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	state := remoteci.BaselineState{
		SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: 2,
		SourceHistoryVersion: remoteci.BaselineSourceHistorySchemaVersion,
		MainCommit:           repeatRemoteHex("3", 40), MainTree: repeatRemoteHex("4", 40),
		Platform: "linux/arm64", PolicyDigest: "sha256:" + repeatRemoteHex("3", 64),
		ToolchainDigest:        "sha256:" + repeatRemoteHex("4", 64),
		RuntimeImage:           "registry.example/runtime@sha256:" + repeatRemoteHex("5", 64),
		GateBinarySHA256:       "sha256:" + repeatRemoteHex("6", 64),
		RuntimeSeedSHA256:      "sha256:" + repeatRemoteHex("9", 64),
		BaselineManifestDigest: "sha256:" + repeatRemoteHex("7", 64),
		DataCacheID:            "edc-anchor", DataCacheBucket: "super-dolphin-ci",
		DataCachePath: "/super-dolphin/ci/baselines/1", DataCacheSizeGiB: 20,
		SourceObjectPrefix: "baseline-artifacts/2/",
		CreatedAt:          created, AcceptedAt: created.Add(2 * time.Minute),
	}
	state.Anchor = remoteci.BaselineCacheRef{Generation: 1, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: "sha256:" + repeatRemoteHex("8", 64), MainCommit: repeatRemoteHex("1", 40), MainTree: repeatRemoteHex("2", 40),
		DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath,
		SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: "baseline-artifacts/1/", AcceptedAt: created}
	state.Deltas = []remoteci.BaselineDeltaRef{{Generation: state.Generation, SourceObjectPrefix: state.SourceObjectPrefix,
		ManifestDigest: state.BaselineManifestDigest, BaseCommit: state.Anchor.MainCommit, BaseTree: state.Anchor.MainTree,
		MainCommit: state.MainCommit, MainTree: state.MainTree, AcceptedAt: state.AcceptedAt}}
	previous := state.Anchor
	state.PreviousAnchor = &previous
	return state
}

func remoteBaselineStateWithRetiredFixture() remoteci.BaselineState {
	state := remoteBaselineStateFixture()
	created := state.CreatedAt
	state.Generation = 6
	state.MainCommit, state.MainTree = repeatRemoteHex("6", 40), repeatRemoteHex("f", 40)
	state.BaselineManifestDigest = "sha256:" + repeatRemoteHex("6", 64)
	state.DataCacheID, state.DataCachePath = "edc-current6", "/super-dolphin/ci/baselines/6"
	state.SourceObjectPrefix = "baseline-artifacts/6/"
	state.AcceptedAt = created.Add(6 * time.Minute)
	state.Anchor = remoteci.BaselineCacheRef{
		Generation: 6, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: state.BaselineManifestDigest, MainCommit: state.MainCommit, MainTree: state.MainTree,
		DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath,
		SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: state.SourceObjectPrefix, AcceptedAt: state.AcceptedAt,
	}
	state.Deltas = nil
	previous := remoteci.BaselineCacheRef{
		Generation: 4, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: "sha256:" + repeatRemoteHex("4", 64),
		MainCommit:     repeatRemoteHex("4", 40), MainTree: repeatRemoteHex("d", 40),
		DataCacheID: "edc-previous4", DataCacheBucket: state.DataCacheBucket,
		DataCachePath: "/super-dolphin/ci/baselines/4", SizeGiB: state.DataCacheSizeGiB,
		SourceObjectPrefix: "baseline-artifacts/4/", AcceptedAt: created.Add(4 * time.Minute),
	}
	state.PreviousAnchor = &previous
	state.PreviousDeltas = []remoteci.BaselineDeltaRef{{
		Generation: 5, SourceObjectPrefix: "baseline-artifacts/5/",
		ManifestDigest: "sha256:" + repeatRemoteHex("5", 64),
		BaseCommit:     previous.MainCommit, BaseTree: previous.MainTree,
		MainCommit: repeatRemoteHex("5", 40), MainTree: repeatRemoteHex("e", 40),
		AcceptedAt: created.Add(5 * time.Minute),
	}}
	retired := remoteci.BaselineCacheRef{
		Generation: 1, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: "sha256:" + repeatRemoteHex("1", 64),
		MainCommit:     repeatRemoteHex("1", 40), MainTree: repeatRemoteHex("a", 40),
		DataCacheID: "edc-retired1", DataCacheBucket: state.DataCacheBucket,
		DataCachePath: "/super-dolphin/ci/baselines/1", SizeGiB: state.DataCacheSizeGiB,
		SourceObjectPrefix: "baseline-artifacts/1/", AcceptedAt: created.Add(time.Minute),
	}
	state.RetiredAnchor = &retired
	state.RetiredDeltas = []remoteci.BaselineDeltaRef{{
		Generation: 2, SourceObjectPrefix: "baseline-artifacts/2/",
		ManifestDigest: "sha256:" + repeatRemoteHex("2", 64),
		BaseCommit:     retired.MainCommit, BaseTree: retired.MainTree,
		MainCommit: repeatRemoteHex("2", 40), MainTree: repeatRemoteHex("b", 40),
		AcceptedAt: created.Add(2 * time.Minute),
	}}
	return state
}

func repeatRemoteHex(value string, count int) string {
	return strings.Repeat(value, count)
}
