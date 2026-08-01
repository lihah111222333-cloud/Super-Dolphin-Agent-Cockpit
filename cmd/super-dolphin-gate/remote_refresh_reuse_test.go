package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
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
