package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
)

func TestCleanupStaleRemoteBaselineCandidateRemovesCacheBeforeObjects(t *testing.T) {
	config := remoteBaselineRecoveryConfig()
	accepted := remoteBaselineStateFixture()
	stage := remoteBaselineRecoveryStage(config, accepted.Generation+1)
	stale := datacache.DataCache{
		ID: "edc-stale", Name: remoteBaselineResourceName(stage.generation),
		Status: datacache.StatusAvailable, Bucket: config.DataCache.Bucket,
		Path: remoteBaselineCachePath(config, stage.generation), SizeGiB: 20,
	}
	var events []string
	cache := &fakeRemoteBaselineDataCacheClient{
		find:     [][]datacache.DataCache{{stale}},
		describe: [][]datacache.DataCache{{stale}, {}},
		events:   &events,
	}
	store := &fakeRemoteBaselineOSSStore{events: &events}
	session := remoteBaselineRefreshSession{accepted: accepted, cache: cache, config: config, store: store}

	if err := cleanupStaleRemoteBaselineCandidate(context.Background(), session, stage); err != nil {
		t.Fatalf("cleanupStaleRemoteBaselineCandidate() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"find-cache", "delete-cache", "delete-prefix"}) {
		t.Fatalf("cleanup events = %v", events)
	}
	if !reflect.DeepEqual(cache.findBucket, []string{config.DataCache.Bucket}) ||
		!reflect.DeepEqual(cache.findPath, []string{stale.Path}) ||
		!reflect.DeepEqual(cache.findTags, []map[string]string{{
			"owner": "super-dolphin-ci", "generation": "3",
		}}) {
		t.Fatalf("candidate query = buckets %v paths %v tags %v", cache.findBucket, cache.findPath, cache.findTags)
	}
	if !reflect.DeepEqual(cache.deleted, []string{stale.ID}) ||
		!reflect.DeepEqual(store.deletedPrefixes, []string{stage.generationPrefix}) {
		t.Fatalf("deleted cache = %v prefixes = %v", cache.deleted, store.deletedPrefixes)
	}
}

func TestCleanupStaleRemoteBaselineCandidateWithoutCacheDeletesObjects(t *testing.T) {
	config := remoteBaselineRecoveryConfig()
	accepted := remoteBaselineStateFixture()
	stage := remoteBaselineRecoveryStage(config, accepted.Generation+1)
	cache := &fakeRemoteBaselineDataCacheClient{find: [][]datacache.DataCache{{}}}
	store := &fakeRemoteBaselineOSSStore{}
	session := remoteBaselineRefreshSession{accepted: accepted, cache: cache, config: config, store: store}

	if err := cleanupStaleRemoteBaselineCandidate(context.Background(), session, stage); err != nil {
		t.Fatalf("cleanupStaleRemoteBaselineCandidate() error = %v", err)
	}
	if len(cache.deleted) != 0 ||
		!reflect.DeepEqual(store.deletedPrefixes, []string{stage.generationPrefix}) {
		t.Fatalf("deleted cache = %v prefixes = %v", cache.deleted, store.deletedPrefixes)
	}
}

func TestCleanupStaleRemoteBaselineCandidateRejectsAmbiguousCache(t *testing.T) {
	config := remoteBaselineRecoveryConfig()
	accepted := remoteBaselineStateFixture()
	stage := remoteBaselineRecoveryStage(config, accepted.Generation+1)
	path := remoteBaselineCachePath(config, stage.generation)
	caches := []datacache.DataCache{
		{ID: "edc-first", Name: remoteBaselineResourceName(stage.generation), Status: datacache.StatusAvailable, Bucket: config.DataCache.Bucket, Path: path, SizeGiB: 20},
		{ID: "edc-second", Name: remoteBaselineResourceName(stage.generation), Status: datacache.StatusAvailable, Bucket: config.DataCache.Bucket, Path: path, SizeGiB: 20},
	}
	cache := &fakeRemoteBaselineDataCacheClient{find: [][]datacache.DataCache{caches}}
	store := &fakeRemoteBaselineOSSStore{}
	session := remoteBaselineRefreshSession{accepted: accepted, cache: cache, config: config, store: store}

	err := cleanupStaleRemoteBaselineCandidate(context.Background(), session, stage)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("cleanupStaleRemoteBaselineCandidate() error = %v", err)
	}
	if len(cache.deleted) != 0 || len(store.deletedPrefixes) != 0 {
		t.Fatalf("ambiguous cleanup deleted cache = %v prefixes = %v", cache.deleted, store.deletedPrefixes)
	}
}

func TestCleanupStaleRemoteBaselineCandidateRejectsAcceptedGeneration(t *testing.T) {
	config := remoteBaselineRecoveryConfig()
	accepted := remoteBaselineStateFixture()
	stage := remoteBaselineRecoveryStage(config, accepted.Generation)
	session := remoteBaselineRefreshSession{
		accepted: accepted,
		cache:    &fakeRemoteBaselineDataCacheClient{},
		config:   config,
		store:    &fakeRemoteBaselineOSSStore{},
	}

	err := cleanupStaleRemoteBaselineCandidate(context.Background(), session, stage)
	if err == nil || !strings.Contains(err.Error(), "next canonical generation") {
		t.Fatalf("cleanupStaleRemoteBaselineCandidate() error = %v", err)
	}
}

func remoteBaselineRecoveryConfig() remoteRunConfig {
	var config remoteRunConfig
	config.OSS.BaselinePrefix = "baseline-artifacts/"
	config.DataCache.Bucket = "super-dolphin-ci"
	config.DataCache.PathPrefix = "/super-dolphin/ci/baselines"
	return config
}

func remoteBaselineRecoveryStage(config remoteRunConfig, generation uint64) remoteBaselineArtifactStage {
	return remoteBaselineArtifactStage{
		generation:       generation,
		generationPrefix: remoteBaselineSourcePrefix(config, generation),
		inputPrefix:      remoteBaselineInputPrefix(config, generation),
		outputPrefix:     remoteBaselineOutputPrefix(config, generation),
	}
}
