package remoteci

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBaselineStateSingleAnchorWithDeltas(t *testing.T) {
	state := validBaselineState()
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got := state.CurrentAnchorRef(); got != state.Anchor {
		t.Fatalf("CurrentAnchorRef() = %#v", got)
	}
	deltas := state.DeltaRefs()
	deltas[0].Generation = 99
	if state.Deltas[0].Generation == 99 {
		t.Fatal("DeltaRefs() returned mutable backing slice")
	}
}

func TestBaselineStateAcceptsCommitOnlyDelta(t *testing.T) {
	state := validBaselineState()
	state.Deltas[0].MainTree = state.Deltas[0].BaseTree
	state.Deltas[1].BaseTree = state.Deltas[0].MainTree
	if err := state.Validate(); err != nil {
		t.Fatalf("commit-only delta rejected: %v", err)
	}
}

func TestBaselineStateDirectCacheReference(t *testing.T) {
	state := validBaselineState()
	reference := validDirectCacheRef(t, state)
	state.DirectCacheRef = &reference
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var loaded BaselineState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if loaded.DirectCacheRef == nil || loaded.Validate() != nil {
		t.Fatalf("round-tripped state = %#v", loaded)
	}
	for name, mutate := range map[string]func(*DirectCacheRef){
		"missing cache ID":        func(value *DirectCacheRef) { value.DataCacheID = "" },
		"cache bucket drift":      func(value *DirectCacheRef) { value.DataCacheBucket = "another-bucket" },
		"cache path generation":   func(value *DirectCacheRef) { value.DataCachePath = "/super-dolphin/ci/direct-cache/2" },
		"broad source prefix":     func(value *DirectCacheRef) { value.SourceObjectPrefix = "baseline-artifacts/" },
		"parent chain drift":      func(value *DirectCacheRef) { value.ParentChainSHA256 = digest("9") },
		"runtime Go binding":      func(value *DirectCacheRef) { value.RuntimeGoSHA256 = digest("8") },
		"runtime deps binding":    func(value *DirectCacheRef) { value.RuntimeDepsSHA256 = "" },
		"tree digest malformed":   func(value *DirectCacheRef) { value.TreeSHA256 = "sha256:bad" },
		"generation does not tie": func(value *DirectCacheRef) { value.Generation-- },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := validBaselineState()
			invalidReference := validDirectCacheRef(t, invalid)
			mutate(&invalidReference)
			invalid.DirectCacheRef = &invalidReference
			if err := invalid.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestBaselineStateRejectsInvalidAnchorAndDeltaChains(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BaselineState)
	}{
		{"anchor digest missing", func(state *BaselineState) { state.Anchor.ManifestDigest = "" }},
		{"top cache drift", func(state *BaselineState) { state.DataCachePath = "/super-dolphin/ci/drift" }},
		{"delta digest drift", func(state *BaselineState) { state.BaselineManifestDigest = digest("9") }},
		{"delta order", func(state *BaselineState) { state.Deltas[1].Generation = state.Deltas[0].Generation }},
		{"delta overflow", func(state *BaselineState) {
			state.Deltas = append(state.Deltas, state.Deltas[1], state.Deltas[1], state.Deltas[1])
		}},
		{"delta base drift", func(state *BaselineState) { state.Deltas[1].BaseCommit = strings.Repeat("9", 40) }},
		{"previous delta without anchor", func(state *BaselineState) {
			state.PreviousDeltas = append([]BaselineDeltaRef(nil), state.Deltas...)
		}},
		{"retired anchor overlaps", func(state *BaselineState) { anchor := state.Anchor; state.RetiredAnchor = &anchor }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := validBaselineState()
			test.mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestBaselineStateMigratesV4CurrentToAnchor(t *testing.T) {
	legacy := map[string]any{"schema_version": 4, "generation": 2, "main_commit": strings.Repeat("a", 40), "main_tree": strings.Repeat("b", 40), "platform": "linux/amd64", "policy_digest": digest("c"), "toolchain_digest": digest("d"), "runtime_image": "registry.example/runtime@" + digest("e"), "gate_binary_sha256": digest("1"), "runtime_seed_manifest_sha256": digest("2"), "baseline_manifest_digest": digest("3"), "data_cache_id": "edc-current", "data_cache_bucket": "super-dolphin-ci", "data_cache_path": "/super-dolphin/ci/baselines/2", "data_cache_size_gib": 20, "source_object_prefix": "baseline-artifacts/2/", "created_at": "2026-07-27T01:00:00Z", "accepted_at": "2026-07-27T01:01:00Z", "previous": map[string]any{"generation": 1, "data_cache_id": "edc-previous", "data_cache_bucket": "super-dolphin-ci", "data_cache_path": "/super-dolphin/ci/baselines/1", "source_object_prefix": "baseline-artifacts/1/", "accepted_at": "2026-07-27T00:01:00Z"}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var state BaselineState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if state.Anchor.Kind != BaselineCacheKindAnchor || state.Anchor.ManifestDigest != state.BaselineManifestDigest || len(state.Deltas) != 0 || state.PreviousAnchor == nil || state.PreviousAnchor.ManifestDigest != "" {
		t.Fatalf("migrated state = %#v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("migrated Validate() error = %v", err)
	}
	if state.SourceHistoryVersion != 0 {
		t.Fatalf("migrated SourceHistoryVersion = %d, want legacy sentinel 0", state.SourceHistoryVersion)
	}
}

func TestBaselineStateMigrationSentinelRoundTripsCurrentSchema(t *testing.T) {
	state := validBaselineState()
	state.SourceHistoryVersion = 0
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var loaded BaselineState
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if loaded.SourceHistoryVersion != 0 || loaded.Validate() != nil {
		t.Fatalf("sentinel state = %#v", loaded)
	}
}

func TestBaselineStateMigratesV6WithoutDirectCacheReference(t *testing.T) {
	legacy := validBaselineState()
	legacy.SchemaVersion = BaselineStatePreviousSchemaVersion
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var state BaselineState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if state.SchemaVersion != BaselineStateSchemaVersion || state.DirectCacheRef != nil || state.SourceHistoryVersion != BaselineSourceHistorySchemaVersion {
		t.Fatalf("migrated state = %#v", state)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("migrated Validate() error = %v", err)
	}
}

func TestBaselineStateMigratesV5WithoutClaimingCompleteSourceHistory(t *testing.T) {
	legacy := validBaselineState()
	legacy.SchemaVersion = baselineStateLegacySchemaVersion
	legacy.SourceHistoryVersion = 0
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var legacyWire map[string]json.RawMessage
	if err := json.Unmarshal(data, &legacyWire); err != nil {
		t.Fatal(err)
	}
	delete(legacyWire, "source_history_version")
	data, err = json.Marshal(legacyWire)
	if err != nil {
		t.Fatal(err)
	}
	var state BaselineState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if state.SchemaVersion != BaselineStateSchemaVersion || state.SourceHistoryVersion != 0 {
		t.Fatalf("migrated versions = schema %d source %d", state.SchemaVersion, state.SourceHistoryVersion)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("migrated Validate() error = %v", err)
	}
}

func TestBaselineStateRejectsNewerSourceHistoryVersion(t *testing.T) {
	state := validBaselineState()
	state.SourceHistoryVersion = BaselineSourceHistorySchemaVersion + 1
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded BaselineState
	if err := json.Unmarshal(data, &decoded); err == nil {
		t.Fatal("Unmarshal() accepted a newer source history version")
	}
}

func TestBaselineStateRejectsUnknownAndMultipleValues(t *testing.T) {
	data, err := json.Marshal(validBaselineState())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{append(data[:len(data)-1], []byte(`,"unknown":true}`)...), append(data, []byte(` {}`)...)} {
		var state BaselineState
		if err := json.Unmarshal(input, &state); err == nil {
			t.Fatal("Unmarshal() accepted invalid wire data")
		}
	}
}

func TestBaselineStateFieldRegistry(t *testing.T) {
	assertBaselineFields(t, reflect.TypeFor[BaselineState](), []string{"SchemaVersion", "Generation", "MainCommit", "MainTree", "Platform", "PolicyDigest", "ToolchainDigest", "RuntimeImage", "GateBinarySHA256", "RuntimeSeedSHA256", "BaselineManifestDigest", "SourceHistoryVersion", "DataCacheID", "DataCacheBucket", "DataCachePath", "DataCacheSizeGiB", "SourceObjectPrefix", "CreatedAt", "AcceptedAt", "Anchor", "Deltas", "DirectCacheRef", "RetiredDirectCacheRef", "PreviousAnchor", "RetiredAnchor", "PreviousDeltas", "RetiredDeltas"})
	assertBaselineFields(t, reflect.TypeFor[BaselineCacheRef](), []string{"Generation", "Kind", "ManifestDigest", "MainCommit", "MainTree", "DataCacheID", "DataCacheBucket", "DataCachePath", "SizeGiB", "SourceObjectPrefix", "AcceptedAt"})
	assertBaselineFields(t, reflect.TypeFor[BaselineDeltaRef](), []string{"Generation", "SourceObjectPrefix", "ManifestDigest", "BaseCommit", "BaseTree", "MainCommit", "MainTree", "AcceptedAt"})
	assertBaselineFields(t, reflect.TypeFor[DirectCacheRef](), []string{"DataCacheID", "DataCacheBucket", "DataCachePath", "SizeGiB", "Generation", "SourceObjectPrefix", "ManifestDigest", "TreeSHA256", "ParentChainSHA256", "RuntimeGoSHA256", "RuntimeDepsSHA256"})
}

func validBaselineState() BaselineState {
	created := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	state := BaselineState{SchemaVersion: BaselineStateSchemaVersion, Generation: 3, MainCommit: strings.Repeat("a", 40), MainTree: strings.Repeat("b", 40), Platform: "linux/amd64", PolicyDigest: digest("c"), ToolchainDigest: digest("d"), RuntimeImage: "registry.example/runtime@" + digest("e"), GateBinarySHA256: digest("1"), RuntimeSeedSHA256: digest("2"), BaselineManifestDigest: digest("3"), SourceHistoryVersion: BaselineSourceHistorySchemaVersion, DataCacheID: "edc-anchor", DataCacheBucket: "super-dolphin-ci", DataCachePath: "/super-dolphin/ci/baselines/1", DataCacheSizeGiB: 20, SourceObjectPrefix: "baseline-artifacts/3/", CreatedAt: created, AcceptedAt: created.Add(3 * time.Minute)}
	anchorCommit, anchorTree := strings.Repeat("1", 40), strings.Repeat("2", 40)
	deltaCommit, deltaTree := strings.Repeat("f", 40), strings.Repeat("e", 40)
	state.Anchor = BaselineCacheRef{Generation: 1, Kind: BaselineCacheKindAnchor, ManifestDigest: digest("4"), MainCommit: anchorCommit, MainTree: anchorTree, DataCacheID: state.DataCacheID, DataCacheBucket: state.DataCacheBucket, DataCachePath: state.DataCachePath, SizeGiB: state.DataCacheSizeGiB, SourceObjectPrefix: "baseline-artifacts/1/", AcceptedAt: created}
	state.Deltas = []BaselineDeltaRef{
		{Generation: 2, SourceObjectPrefix: "baseline-artifacts/2/", ManifestDigest: digest("5"), BaseCommit: anchorCommit, BaseTree: anchorTree, MainCommit: deltaCommit, MainTree: deltaTree, AcceptedAt: created.Add(time.Minute)},
		{Generation: state.Generation, SourceObjectPrefix: state.SourceObjectPrefix, ManifestDigest: state.BaselineManifestDigest, BaseCommit: deltaCommit, BaseTree: deltaTree, MainCommit: state.MainCommit, MainTree: state.MainTree, AcceptedAt: state.AcceptedAt},
	}
	return state
}

func validDirectCacheRef(t *testing.T, state BaselineState) DirectCacheRef {
	t.Helper()
	parentChainDigest, err := CurrentBaselineParentChainDigest(state)
	if err != nil {
		t.Fatal(err)
	}
	return DirectCacheRef{
		DataCacheID:        "edc-direct",
		DataCacheBucket:    "super-dolphin-ci",
		DataCachePath:      "/super-dolphin/ci/direct-cache/3",
		SizeGiB:            20,
		Generation:         state.Generation,
		SourceObjectPrefix: "baseline-artifacts/3/output/direct-cache/",
		ManifestDigest:     digest("6"),
		TreeSHA256:         digest("7"),
		ParentChainSHA256:  parentChainDigest,
		RuntimeGoSHA256:    state.RuntimeSeedSHA256,
		RuntimeDepsSHA256:  digest("9"),
	}
}

func digest(value string) string { return "sha256:" + strings.Repeat(value, 64) }
func assertBaselineFields(t *testing.T, structType reflect.Type, expected []string) {
	t.Helper()
	actual := make([]string, 0, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		actual = append(actual, structType.Field(index).Name)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("%s fields = %v, want %v", structType.Name(), actual, expected)
	}
}
