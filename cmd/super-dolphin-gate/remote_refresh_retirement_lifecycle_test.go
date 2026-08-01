package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestPromoteRemoteBaselineRetiresOnlyReplacedAnchorChain(t *testing.T) {
	accepted := remoteBaselineCurrentAndPreviousFixture()
	state := remoteBaselineAnchorReplacementState(accepted)
	carryRemoteBaselineHistory(&state, accepted)
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	retired := *accepted.PreviousAnchor
	cache := &fakeRemoteBaselineDataCacheClient{describe: [][]datacache.DataCache{{
		{ID: retired.DataCacheID, Status: datacache.StatusAvailable, Bucket: retired.DataCacheBucket, Path: retired.DataCachePath},
	}, {}}}
	store := &fakeRemoteBaselineOSSStore{}

	assertRemoteBaselineConditions(t, "staged previous chain", state.PreviousAnchor != nil, state.PreviousAnchor != nil && *state.PreviousAnchor == accepted.CurrentAnchorRef(), reflect.DeepEqual(state.PreviousDeltas, accepted.DeltaRefs()))
	assertRemoteBaselineConditions(t, "staged retired anchor", state.RetiredAnchor != nil, state.RetiredAnchor != nil && *state.RetiredAnchor == retired)
	if persisted, err := promoteRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		cache: cache, store: store, statePath: statePath,
		verifySuccessor: func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error { return nil },
	}, &state); err != nil || !persisted {
		t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
	}
	if !reflect.DeepEqual(cache.deleted, []string{retired.DataCacheID}) {
		t.Fatalf("deleted DataCaches = %v, want only %q", cache.deleted, retired.DataCacheID)
	}
	if !reflect.DeepEqual(store.deletedPrefixes, []string{
		retired.SourceObjectPrefix,
		accepted.PreviousDeltas[0].SourceObjectPrefix,
	}) {
		t.Fatalf("deleted prefixes = %v", store.deletedPrefixes)
	}
	assertRemoteBaselineConditions(t, "promoted state", state.DataCacheID == "edc-current52", state.RetiredAnchor == nil, len(state.RetiredDeltas) == 0, state.PreviousAnchor != nil, state.PreviousAnchor != nil && state.PreviousAnchor.DataCacheID == accepted.DataCacheID)
	persisted, err := loadRemoteBaselineState(statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	assertRemoteBaselineConditions(t, "persisted previous chain", persisted.PreviousAnchor != nil, persisted.PreviousAnchor != nil && *persisted.PreviousAnchor == accepted.CurrentAnchorRef(), reflect.DeepEqual(persisted.PreviousDeltas, accepted.DeltaRefs()))
}

func TestPromoteRemoteBaselinePreservesDeltaReuseLiveResources(t *testing.T) {
	accepted := remoteBaselineDeltaCurrentAndPreviousFixture()
	state := remoteBaselineDeltaReuseState(accepted)
	carryRemoteBaselineHistory(&state, accepted)
	cache := &fakeRemoteBaselineDataCacheClient{}
	store := &fakeRemoteBaselineOSSStore{}

	if persisted, err := promoteRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		cache: cache, store: store, statePath: filepath.Join(t.TempDir(), "baseline-state.json"),
		verifySuccessor: func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error { return nil },
	}, &state); err != nil || !persisted {
		t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
	}
	assertRemoteBaselineConditions(t, "delta promotion changed live chain", state.DataCacheID == accepted.DataCacheID, state.Anchor == accepted.Anchor, reflect.DeepEqual(state.Deltas[:len(accepted.Deltas)], accepted.Deltas))
	assertRemoteBaselineConditions(t, "delta previous chain", state.PreviousAnchor != nil, state.PreviousAnchor != nil && *state.PreviousAnchor == accepted.CurrentAnchorRef(), reflect.DeepEqual(state.PreviousDeltas, accepted.DeltaRefs()))
	assertRemoteBaselineConditions(t, "delta promotion retired live resources", len(cache.deleted) == 0, len(store.deletedPrefixes) == 0, state.RetiredAnchor == nil, len(state.RetiredDeltas) == 0)
}

func TestPromoteRemoteBaselineStateWriteFailureDoesNotDeleteLiveResources(t *testing.T) {
	accepted := remoteBaselineCurrentAndPreviousFixture()
	state := remoteBaselineAnchorReplacementState(accepted)
	carryRemoteBaselineHistory(&state, accepted)
	statePath := filepath.Join(t.TempDir(), "baseline-state-directory")
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	cache := &fakeRemoteBaselineDataCacheClient{}
	store := &fakeRemoteBaselineOSSStore{}

	persisted, err := promoteRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		cache: cache, store: store, statePath: statePath,
		verifySuccessor: func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error { return nil },
	}, &state)

	if err == nil || persisted {
		t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
	}
	if len(cache.deleted) != 0 || len(store.deletedPrefixes) != 0 {
		t.Fatalf("state write failure deleted live resources: caches=%v prefixes=%v", cache.deleted, store.deletedPrefixes)
	}
}

func TestPromoteRemoteBaselineDefersCleanupUntilSuccessorIsVerifiedPersistedAndReadable(t *testing.T) {
	state := remoteBaselineAnchorReplacementState(remoteBaselineCurrentAndPreviousFixture())
	tests := []struct {
		name       string
		verifyErr  error
		writeErr   error
		readErr    error
		mismatch   bool
		persisted  bool
		wantEvents []string
	}{
		{name: "reverify failure", verifyErr: errors.New("unavailable"), wantEvents: []string{"verify"}},
		{name: "state write failure", writeErr: errors.New("disk full"), wantEvents: []string{"verify", "write"}},
		{name: "final read failure", readErr: errors.New("corrupt read"), persisted: true, wantEvents: []string{"verify", "write", "read"}},
		{name: "final read mismatch", mismatch: true, persisted: true, wantEvents: []string{"verify", "write", "read"}},
		{name: "success", persisted: true, wantEvents: []string{"verify", "write", "read", "legacy", "retired"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make([]string, 0, 5)
			session := remoteBaselineRefreshSession{
				verifySuccessor: func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error {
					events = append(events, "verify")
					return test.verifyErr
				},
				writeState: func(string, remoteci.BaselineState) error {
					events = append(events, "write")
					return test.writeErr
				},
				readState: func(string, bool) (remoteci.BaselineState, error) {
					events = append(events, "read")
					loaded := state
					if test.mismatch {
						loaded.Generation++
					}
					return loaded, test.readErr
				},
				cleanupLegacy: func(context.Context, remoteBaselineDataCacheClient, remoteBaselineOSSStore, *remoteLegacyBaselineMigration) error {
					events = append(events, "legacy")
					return nil
				},
				cleanupRetired: func(context.Context, remoteBaselineDataCacheClient, remoteBaselineOSSStore, string, *remoteci.BaselineState) error {
					events = append(events, "retired")
					return nil
				},
			}
			persisted, err := promoteRemoteBaseline(context.Background(), session, &state)
			if test.verifyErr != nil || test.writeErr != nil || test.readErr != nil || test.mismatch {
				if err == nil || persisted != test.persisted {
					t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
				}
			} else if err != nil || persisted != test.persisted {
				t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
			}
			if !reflect.DeepEqual(events, test.wantEvents) {
				t.Fatalf("promotion events = %v, want %v", events, test.wantEvents)
			}
		})
	}
}

func TestPromoteRemoteBaselineRealWriteReadRoundTripUsesPersistentEquivalence(t *testing.T) {
	state := remoteBaselineAnchorReplacementState(remoteBaselineCurrentAndPreviousFixture())
	state.Deltas = []remoteci.BaselineDeltaRef{}
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	persisted, err := promoteRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		statePath:       statePath,
		verifySuccessor: func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error { return nil },
		cleanupLegacy: func(context.Context, remoteBaselineDataCacheClient, remoteBaselineOSSStore, *remoteLegacyBaselineMigration) error {
			return nil
		},
		cleanupRetired: func(context.Context, remoteBaselineDataCacheClient, remoteBaselineOSSStore, string, *remoteci.BaselineState) error {
			return nil
		},
	}, &state)
	if err != nil || !persisted {
		t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
	}
}

func TestPromotionUncertaintyRetainsSuccessorArtifactsAndCache(t *testing.T) {
	state := remoteBaselineAnchorReplacementState(remoteBaselineCurrentAndPreviousFixture())
	store := &fakeRemoteBaselineOSSStore{}
	cache := &fakeRemoteBaselineDataCacheClient{}
	persisted, err := promoteRemoteBaseline(context.Background(), remoteBaselineRefreshSession{
		verifySuccessor: func(context.Context, remoteBaselineRefreshSession, remoteci.BaselineState) error { return nil },
		writeState:      func(string, remoteci.BaselineState) error { return nil },
		readState: func(string, bool) (remoteci.BaselineState, error) {
			return remoteci.BaselineState{}, errors.New("read unavailable")
		},
	}, &state)
	if err == nil || !persisted {
		t.Fatalf("promoteRemoteBaseline() = persisted %v, error %v", persisted, err)
	}
	accepted := false
	if persisted {
		accepted = true
	}
	resultErr := error(err)
	cleanupUnacceptedRemoteArtifacts(&resultErr, store, state.SourceObjectPrefix, &accepted)
	cleanupUnacceptedRemoteCache(&resultErr, cache, datacache.DataCache{ID: state.DataCacheID, Bucket: state.DataCacheBucket, Path: state.DataCachePath}, &accepted)
	if len(store.deletedPrefixes) != 0 || len(cache.deleted) != 0 {
		t.Fatalf("uncertain promotion deleted successor resources: prefixes=%v caches=%v", store.deletedPrefixes, cache.deleted)
	}
}

func remoteBaselineAnchorReplacementState(accepted remoteci.BaselineState) remoteci.BaselineState {
	state := accepted
	state.Generation = accepted.Generation + 1
	state.MainCommit, state.MainTree = repeatRemoteHex("c", 40), repeatRemoteHex("d", 40)
	state.BaselineManifestDigest = "sha256:" + repeatRemoteHex("e", 64)
	state.SourceObjectPrefix = fmt.Sprintf("baseline-artifacts/%d/", state.Generation)
	state.AcceptedAt = accepted.AcceptedAt.Add(time.Minute)
	state.DataCacheID = fmt.Sprintf("edc-current%d", state.Generation)
	state.DataCachePath = fmt.Sprintf("/super-dolphin/ci/baselines/%d", state.Generation)
	state.Anchor = remoteci.BaselineCacheRef{
		Generation: state.Generation, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: state.BaselineManifestDigest, MainCommit: state.MainCommit, MainTree: state.MainTree,
		DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath,
		SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: state.SourceObjectPrefix, AcceptedAt: state.AcceptedAt,
	}
	state.Deltas, state.PreviousAnchor, state.PreviousDeltas = nil, nil, nil
	state.RetiredAnchor, state.RetiredDeltas = nil, nil
	return state
}

func remoteBaselineDeltaReuseState(accepted remoteci.BaselineState) remoteci.BaselineState {
	state := accepted
	state.Generation = accepted.Generation + 1
	state.MainCommit, state.MainTree = repeatRemoteHex("c", 40), repeatRemoteHex("d", 40)
	state.BaselineManifestDigest = "sha256:" + repeatRemoteHex("e", 64)
	state.SourceObjectPrefix = fmt.Sprintf("baseline-artifacts/%d/", state.Generation)
	state.AcceptedAt = accepted.AcceptedAt.Add(time.Minute)
	state.Deltas = append(accepted.DeltaRefs(), remoteci.BaselineDeltaRef{
		Generation: state.Generation, SourceObjectPrefix: state.SourceObjectPrefix,
		ManifestDigest: state.BaselineManifestDigest, BaseCommit: accepted.MainCommit, BaseTree: accepted.MainTree,
		MainCommit: state.MainCommit, MainTree: state.MainTree, AcceptedAt: state.AcceptedAt,
	})
	state.PreviousAnchor, state.PreviousDeltas = nil, nil
	state.RetiredAnchor, state.RetiredDeltas = nil, nil
	return state
}

func remoteBaselineCurrentAndPreviousFixture() remoteci.BaselineState {
	created := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	state := remoteci.BaselineState{
		SchemaVersion: remoteci.BaselineStateSchemaVersion, Generation: 51,
		SourceHistoryVersion: remoteci.BaselineSourceHistorySchemaVersion,
		MainCommit:           repeatRemoteHex("a", 40), MainTree: repeatRemoteHex("b", 40),
		Platform: "linux/arm64", PolicyDigest: "sha256:" + repeatRemoteHex("1", 64),
		ToolchainDigest:        "sha256:" + repeatRemoteHex("2", 64),
		RuntimeImage:           "registry.example/runtime@sha256:" + repeatRemoteHex("3", 64),
		GateBinarySHA256:       "sha256:" + repeatRemoteHex("4", 64),
		RuntimeSeedSHA256:      "sha256:" + repeatRemoteHex("5", 64),
		BaselineManifestDigest: "sha256:" + repeatRemoteHex("6", 64),
		DataCacheID:            "edc-current51", DataCacheBucket: "super-dolphin-ci",
		DataCachePath: "/super-dolphin/ci/baselines/51", DataCacheSizeGiB: 20,
		SourceObjectPrefix: "baseline-artifacts/51/",
		CreatedAt:          created, AcceptedAt: created.Add(51 * time.Minute),
	}
	state.Anchor = remoteci.BaselineCacheRef{
		Generation: state.Generation, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: state.BaselineManifestDigest, MainCommit: state.MainCommit, MainTree: state.MainTree,
		DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath,
		SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: state.SourceObjectPrefix, AcceptedAt: state.AcceptedAt,
	}
	previous := remoteci.BaselineCacheRef{
		Generation: 49, Kind: remoteci.BaselineCacheKindAnchor,
		ManifestDigest: "sha256:" + repeatRemoteHex("7", 64),
		MainCommit:     repeatRemoteHex("8", 40), MainTree: repeatRemoteHex("9", 40),
		DataCacheID: "edc-previous49", DataCacheBucket: state.DataCacheBucket,
		DataCachePath: "/super-dolphin/ci/baselines/49", SizeGiB: state.DataCacheSizeGiB,
		SourceObjectPrefix: "baseline-artifacts/49/", AcceptedAt: created.Add(49 * time.Minute),
	}
	state.PreviousAnchor = &previous
	state.PreviousDeltas = []remoteci.BaselineDeltaRef{{
		Generation: 50, SourceObjectPrefix: "baseline-artifacts/50/",
		ManifestDigest: "sha256:" + repeatRemoteHex("f", 64),
		BaseCommit:     previous.MainCommit, BaseTree: previous.MainTree,
		MainCommit: repeatRemoteHex("e", 40), MainTree: repeatRemoteHex("0", 40),
		AcceptedAt: created.Add(50 * time.Minute),
	}}
	return state
}

func remoteBaselineDeltaCurrentAndPreviousFixture() remoteci.BaselineState {
	state := remoteBaselineCurrentAndPreviousFixture()
	anchor := *state.PreviousAnchor
	state.Anchor = anchor
	state.DataCacheID, state.DataCachePath = anchor.DataCacheID, anchor.DataCachePath
	state.Deltas = append([]remoteci.BaselineDeltaRef(nil), state.PreviousDeltas...)
	previousTip := state.PreviousDeltas[len(state.PreviousDeltas)-1]
	state.Deltas = append(state.Deltas, remoteci.BaselineDeltaRef{
		Generation: state.Generation, SourceObjectPrefix: state.SourceObjectPrefix,
		ManifestDigest: state.BaselineManifestDigest, BaseCommit: previousTip.MainCommit, BaseTree: previousTip.MainTree,
		MainCommit: state.MainCommit, MainTree: state.MainTree, AcceptedAt: state.AcceptedAt,
	})
	return state
}
func TestCleanupRetiredRemoteBaselineDeletesCacheAndObjectsBeforeClearingJournal(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	state := remoteBaselineStateWithRetiredFixture()
	if err := writeRemoteBaselineState(statePath, state); err != nil {
		t.Fatal(err)
	}
	retired := *state.RetiredAnchor
	cache := &fakeRemoteBaselineDataCacheClient{
		describe: [][]datacache.DataCache{{
			{
				ID: retired.DataCacheID, Status: datacache.StatusAvailable,
				Bucket: retired.DataCacheBucket, Path: retired.DataCachePath,
			},
		}, {}},
	}
	store := &fakeRemoteBaselineOSSStore{}
	if err := cleanupRetiredRemoteBaseline(
		context.Background(),
		cache,
		store,
		statePath,
		&state,
	); err != nil {
		t.Fatalf("cleanupRetiredRemoteBaseline() error = %v", err)
	}
	assertRemoteBaselineConditions(t, "cleanup state", state.RetiredAnchor == nil, len(state.RetiredDeltas) == 0, len(cache.deleted) == 1, len(store.deletedPrefixes) == 2, store.deletedPrefixes[0] == retired.SourceObjectPrefix, store.deletedPrefixes[1] == "baseline-artifacts/2/")
	loaded, err := loadRemoteBaselineState(statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RetiredAnchor != nil || len(loaded.RetiredDeltas) != 0 {
		t.Fatalf("persisted retired cleanup = %#v/%#v", loaded.RetiredAnchor, loaded.RetiredDeltas)
	}
}

func assertRemoteBaselineConditions(t *testing.T, message string, conditions ...bool) {
	t.Helper()
	for _, condition := range conditions {
		if !condition {
			t.Fatal(message)
		}
	}
}

func TestCleanupRetiredRemoteBaselineResumesAfterOSSFailure(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline-state.json")
	state := remoteBaselineStateWithRetiredFixture()
	retired := *state.RetiredAnchor
	if err := writeRemoteBaselineState(statePath, state); err != nil {
		t.Fatal(err)
	}
	cache := &fakeRemoteBaselineDataCacheClient{describe: [][]datacache.DataCache{{}, {}}}
	store := &fakeRemoteBaselineOSSStore{
		deletePrefixErrors: []error{errors.New("injected OSS failure"), nil},
	}
	assertRetiredRemoteBaselineCleanupFails(t, cache, store, statePath, &state)
	assertRetiredRemoteBaselineJournalPersists(t, statePath, state, retired)
	assertRetiredRemoteBaselineCleanupRetry(t, cache, store, statePath, &state, retired)
	assertRetiredRemoteBaselineCleanupState(t, statePath, state)
}

func assertRetiredRemoteBaselineCleanupFails(
	t *testing.T,
	cache *fakeRemoteBaselineDataCacheClient,
	store *fakeRemoteBaselineOSSStore,
	statePath string,
	state *remoteci.BaselineState,
) {
	t.Helper()
	if err := cleanupRetiredRemoteBaseline(
		context.Background(), cache, store, statePath, state,
	); err == nil {
		t.Fatal("cleanupRetiredRemoteBaseline() accepted an OSS cleanup failure")
	}
}

func assertRetiredRemoteBaselineJournalPersists(
	t *testing.T,
	statePath string,
	state remoteci.BaselineState,
	retired remoteci.BaselineCacheRef,
) {
	t.Helper()
	if state.RetiredAnchor == nil || *state.RetiredAnchor != retired || len(state.RetiredDeltas) != 1 {
		t.Fatalf("retired journal after failure = %#v/%#v, want %#v", state.RetiredAnchor, state.RetiredDeltas, retired)
	}
	persisted, err := loadRemoteBaselineState(statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RetiredAnchor == nil || *persisted.RetiredAnchor != retired || len(persisted.RetiredDeltas) != 1 {
		t.Fatalf("persisted retired journal after failure = %#v/%#v, want %#v", persisted.RetiredAnchor, persisted.RetiredDeltas, retired)
	}
}

func assertRetiredRemoteBaselineCleanupRetry(
	t *testing.T,
	cache *fakeRemoteBaselineDataCacheClient,
	store *fakeRemoteBaselineOSSStore,
	statePath string,
	state *remoteci.BaselineState,
	retired remoteci.BaselineCacheRef,
) {
	t.Helper()
	if err := cleanupRetiredRemoteBaseline(
		context.Background(), cache, store, statePath, state,
	); err != nil {
		t.Fatalf("cleanupRetiredRemoteBaseline() retry error = %v", err)
	}
	if state.RetiredAnchor != nil || len(state.RetiredDeltas) != 0 || len(store.deletedPrefixes) != 3 ||
		store.deletedPrefixes[0] != retired.SourceObjectPrefix ||
		store.deletedPrefixes[1] != retired.SourceObjectPrefix ||
		store.deletedPrefixes[2] != "baseline-artifacts/2/" {
		t.Fatalf("cleanup retry state=%#v prefixes=%v", state, store.deletedPrefixes)
	}
}

func assertRetiredRemoteBaselineCleanupState(
	t *testing.T,
	statePath string,
	state remoteci.BaselineState,
) {
	t.Helper()
	persisted, err := loadRemoteBaselineState(statePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RetiredAnchor != nil || len(persisted.RetiredDeltas) != 0 ||
		persisted.DataCacheID != state.DataCacheID ||
		persisted.PreviousAnchor == nil || state.PreviousAnchor == nil ||
		persisted.PreviousAnchor.DataCacheID != state.PreviousAnchor.DataCacheID {
		t.Fatalf("persisted state after cleanup retry = %#v", persisted)
	}
}
