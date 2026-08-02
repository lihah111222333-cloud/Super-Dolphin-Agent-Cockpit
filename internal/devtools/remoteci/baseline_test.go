package remoteci

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestBaselineStateRejectsLegacyJSONWithMigrationRequired(t *testing.T) {
	legacy := []byte(`{"schema_version":8,"generation":1,"data_cache_id":"edc-legacy"}`)
	var state BaselineState
	err := json.Unmarshal(legacy, &state)
	if !errors.Is(err, ErrRemoteBaselineMigrationRequired) {
		t.Fatalf("UnmarshalJSON() error = %v, want migration-required", err)
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

func TestBaselineStateValidationRequiresOCIProjectCache(t *testing.T) {
	state := validBaselineState()
	state.OCIProjectCache = nil
	if err := state.Validate(); err == nil {
		t.Fatal("Validate() accepted missing OCIProjectCache")
	}
}

func TestBaselineStateFieldRegistry(t *testing.T) {
	if BaselineStateSchemaVersion != 9 {
		t.Fatalf("BaselineStateSchemaVersion = %d", BaselineStateSchemaVersion)
	}
	assertBaselineFields(t, reflect.TypeFor[BaselineState](), []string{"SchemaVersion", "Generation", "MainCommit", "MainTree", "Platform", "PolicyDigest", "ToolchainDigest", "RuntimeImage", "OCIProjectCache", "GateBinarySHA256", "RuntimeSeedSHA256", "BaselineManifestDigest", "CreatedAt", "AcceptedAt"})
	assertBaselineFields(t, reflect.TypeFor[BaselineOCIProjectCache](), []string{"Image", "ContentManifestSHA256", "MainTree", "ToolchainDigest", "Platform", "CachePath"})
}

func validBaselineState() BaselineState {
	created := time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC)
	mainTree, toolchain, runtimeImage := strings.Repeat("b", 40), digest("d"), "registry.example/runtime@"+digest("e")
	return BaselineState{SchemaVersion: BaselineStateSchemaVersion, Generation: 3, MainCommit: strings.Repeat("a", 40), MainTree: mainTree, Platform: "linux/amd64", PolicyDigest: digest("c"), ToolchainDigest: toolchain, RuntimeImage: runtimeImage, OCIProjectCache: validBaselineOCIProjectCache(mainTree, toolchain, "linux/amd64", runtimeImage), GateBinarySHA256: digest("1"), RuntimeSeedSHA256: digest("2"), BaselineManifestDigest: digest("3"), CreatedAt: created, AcceptedAt: created.Add(3 * time.Minute)}
}

func validBaselineOCIProjectCache(mainTree, toolchainDigest, platform, runtimeImage string) *BaselineOCIProjectCache {
	return &BaselineOCIProjectCache{Image: runtimeImage, ContentManifestSHA256: digest("a"), MainTree: mainTree, ToolchainDigest: toolchainDigest, Platform: platform, CachePath: OCIProjectGoBuildCachePath}
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
