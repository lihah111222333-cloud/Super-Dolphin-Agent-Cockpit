package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestReuseRemoteBaselineOnlyRenewsAcceptedGeneration(t *testing.T) {
	state := remoteBaselineStateFixture()
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	cacheFixture := datacache.DataCache{
		ID: state.DataCacheID, Status: datacache.StatusAvailable,
		Bucket: state.DataCacheBucket, Path: state.DataCachePath, SizeGiB: state.DataCacheSizeGiB,
	}
	cache := &fakeRemoteBaselineDataCacheClient{
		describe:     [][]datacache.DataCache{{cacheFixture}, {cacheFixture}},
		renewAllowed: true,
	}
	config := remoteRunConfig{}
	config.DataCache.MaxSizeGiB, config.DataCache.RetentionDays = 100, 2
	var stdout strings.Builder
	if err := reuseRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		accepted: state, acceptedRecommendedSizeGiB: state.DataCacheSizeGiB,
		cache: cache, config: config, statePath: statePath,
	}, &stdout); err != nil {
		t.Fatalf("reuseRemoteBaseline() error = %v", err)
	}
	assertRemoteBaselineRenewOnly(t, cache, state)
	assertRemoteBaselineReusedState(t, statePath, state, stdout.String())
}

func TestCreateRemoteBaselineDirectCacheUsesDedicatedReadOnlyPrefix(t *testing.T) {
	config := remoteRunConfig{}
	config.DataCache.Bucket = "cache-bucket"
	config.DataCache.PathPrefix = "/super-dolphin/ci/baselines"
	config.DataCache.MaxSizeGiB = remoteDataCacheMinimumSizeGiB
	config.DataCache.RetentionDays = 2
	config.OSS.Bucket = "oss-bucket"
	config.OSS.InternalEndpoint = "https://oss-internal.example"
	config.WorkerRoleName = "seed-role"
	cache := &fakeRemoteBaselineDataCacheClient{createResults: []datacache.DataCache{{ID: "edc-direct"}}}
	stage := remoteBaselineArtifactStage{generation: 12, outputPrefix: "baselines/12/output/"}
	manifest := remoteci.BaselineManifest{
		SchemaVersion: remoteci.BaselineManifestSchemaVersion, Generation: stage.generation,
		MainCommit: repeatRemoteHex("b", 40), MainTree: repeatRemoteHex("a", 40),
		Platform: "linux/amd64", PolicyDigest: "sha256:" + repeatRemoteHex("c", 64),
		ToolchainDigest: "sha256:" + repeatRemoteHex("d", 64), RuntimeImage: "example/runtime@sha256:" + repeatRemoteHex("e", 64),
		GateSourceSHA256: "sha256:" + repeatRemoteHex("f", 64), GateBinarySHA256: "sha256:" + repeatRemoteHex("1", 64),
		GateBinarySize: 1, RuntimeSeedManifestSHA256: "sha256:" + repeatRemoteHex("2", 64),
		CABundleSHA256: "sha256:" + repeatRemoteHex("3", 64), CABundleSize: 1, StorageMode: remoteci.BaselineStorageModeAnchor,
		Layers: []remoteci.BaselineLayer{{Generation: stage.generation, Kind: remoteci.BaselineLayerKindAnchor, Name: "runtime-deps", Archive: "runtime-deps.tar.gz", SHA256: "sha256:" + repeatRemoteHex("4", 64), Size: 1}},
	}
	created, err := createRemoteBaselineDirectCache(context.Background(), remoteBaselineRefreshSession{
		cache: cache, config: config, input: remoteBaselineRefreshInput{Identity: remoteci.BaselineIdentity{MainTree: repeatRemoteHex("a", 40)}},
	}, stage, manifest)
	if err != nil {
		t.Fatalf("createRemoteBaselineDirectCache() error = %v", err)
	}
	if created.ID != "edc-direct" || len(cache.createRequests) != 1 {
		t.Fatalf("direct cache create = %#v, requests = %#v", created, cache.createRequests)
	}
	request := cache.createRequests[0]
	if request.Path != remoteBaselineDirectCachePath(config, stage.generation) || request.SizeGiB != remoteDataCacheMinimumSizeGiB {
		t.Fatalf("direct cache path/size = %q/%d", request.Path, request.SizeGiB)
	}
	if request.Source.Path != "/baselines/12/output/direct-cache" || request.Source.Bucket != config.OSS.Bucket {
		t.Fatalf("direct cache source = %#v", request.Source)
	}
}

func TestBindRemoteBaselineDirectCacheKeepsNewestThreeAndRetiresOldest(t *testing.T) {
	state := remoteBaselineStateFixture()
	state.Generation = 5
	state.DirectCacheRef = nil
	previousState := remoteBaselineStateFixture()
	previous := &remoteci.DirectCacheRef{Layers: []remoteci.DirectCacheLayerRef{
		remoteBaselineSeedDirectLayerFixture(previousState, 4),
		remoteBaselineSeedDirectLayerFixture(previousState, 3),
		remoteBaselineSeedDirectLayerFixture(previousState, 2),
	}}
	manifest := gatecontract.GoBuildCacheDirectSeedManifest{
		RuntimeGoSHA256: state.RuntimeSeedSHA256, RuntimeDepsSHA256: "sha256:" + repeatRemoteHex("d", 64),
		TreeSHA256: "sha256:" + repeatRemoteHex("b", 64),
	}
	cache := datacache.DataCache{ID: "edc-direct5", Status: datacache.StatusAvailable, Bucket: state.DataCacheBucket,
		Path: "/super-dolphin/ci/direct-cache/5", SizeGiB: remoteDataCacheMinimumSizeGiB}
	stage := remoteBaselineArtifactStage{generation: 5, outputPrefix: "baseline-artifacts/5/output/"}
	if err := bindRemoteBaselineDirectCache(&state, previous, stage, cache, manifest, "sha256:"+repeatRemoteHex("c", 64)); err != nil {
		t.Fatalf("bindRemoteBaselineDirectCache() error = %v", err)
	}
	if state.DirectCacheRef == nil || len(state.DirectCacheRef.Layers) != 3 ||
		state.DirectCacheRef.Layers[0].Generation != 5 || state.DirectCacheRef.Layers[2].Generation != 3 {
		t.Fatalf("direct cache layers = %#v", state.DirectCacheRef)
	}
	if state.RetiredDirectCacheRef == nil || len(state.RetiredDirectCacheRef.Layers) != 1 ||
		state.RetiredDirectCacheRef.Layers[0].Generation != 2 {
		t.Fatalf("retired direct cache layers = %#v", state.RetiredDirectCacheRef)
	}
}

func TestReuseRemoteBaselineRejectsCapacityChange(t *testing.T) {
	state := remoteBaselineStateFixture()
	config := remoteRunConfig{}
	config.DataCache.MaxSizeGiB = 100
	if err := reuseRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		accepted: state, acceptedRecommendedSizeGiB: state.DataCacheSizeGiB + 80, config: config,
	}, io.Discard); err == nil || !strings.Contains(err.Error(), "a new Anchor is required") {
		t.Fatalf("reuseRemoteBaseline() capacity migration error = %v", err)
	}
}

func TestRemoteBaselineCanReuseRequiresCompleteHistoryIdentityAndCapacity(t *testing.T) {
	state := remoteBaselineStateFixture()
	identity := remoteci.BaselineIdentity{
		MainCommit: state.MainCommit, MainTree: state.MainTree, Platform: state.Platform,
		PolicyDigest: state.PolicyDigest, ToolchainDigest: state.ToolchainDigest,
		RuntimeImage: state.RuntimeImage,
	}
	if !remoteBaselineCanReuse(state, identity, state.DataCacheSizeGiB) {
		t.Fatal("current complete baseline was not reusable")
	}
	legacy := state
	legacy.SourceHistoryVersion = 0
	if remoteBaselineCanReuse(legacy, identity, state.DataCacheSizeGiB) {
		t.Fatal("legacy shallow baseline was reusable")
	}
	changedIdentity := identity
	changedIdentity.MainTree = repeatRemoteHex("f", 40)
	if remoteBaselineCanReuse(state, changedIdentity, state.DataCacheSizeGiB) {
		t.Fatal("identity drift was reusable")
	}
	if remoteBaselineCanReuse(state, identity, state.DataCacheSizeGiB+1) {
		t.Fatal("capacity drift was reusable")
	}
}

func TestVerifyRemoteBaselineSuccessorRejectsMigrationSentinel(t *testing.T) {
	state := remoteBaselineStateFixture()
	state.SourceHistoryVersion = 0
	config := remoteRunConfig{}
	config.Runtime.Image = state.RuntimeImage
	config.DataCache.Bucket = state.DataCacheBucket
	config.DataCache.PathPrefix = "/super-dolphin/ci/baselines"
	if err := verifyRemoteBaselineSuccessor(context.Background(), remoteBaselineRefreshSession{config: config}, state); err == nil || !strings.Contains(err.Error(), "source history is incomplete") {
		t.Fatalf("verifyRemoteBaselineSuccessor() error = %v", err)
	}
}

func assertRemoteBaselineRenewOnly(
	t *testing.T,
	cache *fakeRemoteBaselineDataCacheClient,
	state remoteci.BaselineState,
) {
	t.Helper()
	if cache.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", cache.createCalls)
	}
	if len(cache.renewed) != 1 {
		t.Fatalf("renew calls = %#v", cache.renewed)
	}
	renewed := cache.renewed[0]
	if renewed.id != state.DataCacheID {
		t.Fatalf("renewed DataCache = %q, want %q", renewed.id, state.DataCacheID)
	}
	if renewed.retentionDays != 2 {
		t.Fatalf("renewed retention = %d, want 2", renewed.retentionDays)
	}
	expectedPrefix := fmt.Sprintf("sdci-renew-%d-", state.CurrentAnchorRef().Generation)
	if !strings.HasPrefix(renewed.token, expectedPrefix) {
		t.Fatalf("renew token = %q", renewed.token)
	}
	if strings.Contains(renewed.token, "T") || len(renewed.token) != len(expectedPrefix)+len("20060102") {
		t.Fatalf("renew token is not daily: %q", renewed.token)
	}
}

func assertRemoteBaselineReusedState(
	t *testing.T,
	statePath string,
	previous remoteci.BaselineState,
	output string,
) {
	t.Helper()
	loaded, err := loadRemoteBaselineState(statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Generation != previous.Generation {
		t.Fatalf("reused generation = %d, want %d", loaded.Generation, previous.Generation)
	}
	if loaded.DataCacheID != previous.DataCacheID {
		t.Fatalf("reused DataCache = %q, want %q", loaded.DataCacheID, previous.DataCacheID)
	}
	if !loaded.AcceptedAt.After(previous.AcceptedAt) {
		t.Fatalf("reused AcceptedAt = %s, want after %s", loaded.AcceptedAt, previous.AcceptedAt)
	}
	if !strings.Contains(output, `"reused": true`) {
		t.Fatalf("reuse output = %s", output)
	}
}
