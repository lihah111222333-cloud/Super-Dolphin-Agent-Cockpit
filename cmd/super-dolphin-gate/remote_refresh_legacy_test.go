package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestLoadRemoteBaselineStateForRefreshMigratesV2WithoutAcceptingIt(t *testing.T) {
	config := remoteLegacyBaselineTestConfig()
	path := filepath.Join(t.TempDir(), "baseline-state.json")
	legacy := remoteLegacyBaselineTestState(config)
	writeRemoteLegacyBaselineTestState(t, path, legacy)

	accepted, migration, err := loadRemoteBaselineStateForRefresh(path, config)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.SchemaVersion != 0 {
		t.Fatalf("accepted schema = %d, want empty current state", accepted.SchemaVersion)
	}
	if migration == nil || migration.generation != 12 || len(migration.references) != 2 {
		t.Fatalf("migration = %#v", migration)
	}
	if migration.references[0].Generation != 11 || migration.references[1].Generation != 12 {
		t.Fatalf("migration generations = %v", []uint64{migration.references[0].Generation, migration.references[1].Generation})
	}
	generation, err := nextRemoteBaselineGeneration(remoteci.BaselineState{}, migration)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 13 {
		t.Fatalf("next generation = %d, want 13", generation)
	}
}

func TestLoadRemoteBaselineStateForRefreshKeepsCurrentSchemaStrict(t *testing.T) {
	config := remoteLegacyBaselineTestConfig()
	path := filepath.Join(t.TempDir(), "baseline-state.json")
	current := remoteBaselineStateFixture()
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	accepted, migration, err := loadRemoteBaselineStateForRefresh(path, config)
	if err != nil {
		t.Fatal(err)
	}
	if migration != nil || accepted.Generation != current.Generation || accepted.SchemaVersion != remoteci.BaselineStateSchemaVersion {
		t.Fatalf("accepted = %#v, migration = %#v", accepted, migration)
	}

	data = append(data[:len(data)-1], []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadRemoteBaselineStateForRefresh(path, config); err == nil {
		t.Fatal("current state with an unknown field was accepted")
	}
}

func TestLoadRemoteBaselineStateForRefreshRejectsLegacyResourceDrift(t *testing.T) {
	config := remoteLegacyBaselineTestConfig()
	path := filepath.Join(t.TempDir(), "baseline-state.json")
	legacy := remoteLegacyBaselineTestState(config)
	legacy.DataCachePath = "/different/12"
	writeRemoteLegacyBaselineTestState(t, path, legacy)

	if _, _, err := loadRemoteBaselineStateForRefresh(path, config); err == nil ||
		!strings.Contains(err.Error(), "resource identity") {
		t.Fatalf("load drifted legacy state error = %v", err)
	}
}

func TestCleanupLegacyRemoteBaselinesDeletesOldestThenCurrent(t *testing.T) {
	config := remoteLegacyBaselineTestConfig()
	migration, err := newRemoteLegacyBaselineMigration(config, remoteLegacyBaselineTestState(config))
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeRemoteBaselineDataCacheClient{describe: [][]datacache.DataCache{
		{{ID: "edc-previous", Bucket: config.DataCache.Bucket, Path: remoteBaselineCachePath(config, 11), Status: datacache.StatusAvailable}},
		nil,
		{{ID: "edc-current", Bucket: config.DataCache.Bucket, Path: remoteBaselineCachePath(config, 12), Status: datacache.StatusAvailable}},
		nil,
	}}
	store := &fakeRemoteBaselineOSSStore{}
	if err := cleanupLegacyRemoteBaselines(context.Background(), cache, store, migration); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cache.deleted, []string{"edc-previous", "edc-current"}) {
		t.Fatalf("deleted DataCaches = %v", cache.deleted)
	}
	if !slices.Equal(store.deletedPrefixes, []string{"baseline-artifacts/11/", "baseline-artifacts/12/"}) {
		t.Fatalf("deleted prefixes = %v", store.deletedPrefixes)
	}
}

func remoteLegacyBaselineTestConfig() remoteRunConfig {
	var config remoteRunConfig
	config.DataCache.Bucket = "super-dolphin-ci"
	config.DataCache.PathPrefix = "/super-dolphin/ci/baselines"
	config.OSS.BaselinePrefix = "baseline-artifacts/"
	return config
}

func remoteLegacyBaselineTestState(config remoteRunConfig) remoteLegacyBaselineStateV2 {
	acceptedAt := time.Date(2026, 7, 27, 14, 0, 0, 0, time.UTC)
	return remoteLegacyBaselineStateV2{
		SchemaVersion: remoteLegacyBaselineStateSchemaVersionV2,
		Generation:    12,
		MainCommit:    strings.Repeat("1", 40),
		MainTree:      strings.Repeat("2", 40),
		Platform:      "linux/amd64",
		PolicyDigest:  "sha256:" + strings.Repeat("3", 64), ToolchainDigest: "sha256:" + strings.Repeat("4", 64),
		RuntimeImage:           "registry.example/runtime@sha256:" + strings.Repeat("5", 64),
		BaselineManifestDigest: "sha256:" + strings.Repeat("6", 64),
		DataCacheID:            "edc-current", DataCacheBucket: config.DataCache.Bucket,
		DataCachePath: remoteBaselineCachePath(config, 12), DataCacheSizeGiB: 20,
		SourceObjectPrefix: remoteBaselineSourcePrefix(config, 12),
		CreatedAt:          acceptedAt.Add(-time.Minute), AcceptedAt: acceptedAt,
		Previous: &remoteci.BaselineCacheRef{
			Generation: 11, DataCacheID: "edc-previous", DataCacheBucket: config.DataCache.Bucket,
			DataCachePath: remoteBaselineCachePath(config, 11), SourceObjectPrefix: remoteBaselineSourcePrefix(config, 11),
			AcceptedAt: acceptedAt.Add(-time.Hour),
		},
	}
}

func writeRemoteLegacyBaselineTestState(t *testing.T, path string, state remoteLegacyBaselineStateV2) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
