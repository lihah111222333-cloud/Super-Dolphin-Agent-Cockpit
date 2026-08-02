package remoteci

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestBaselineManifestOCIOnlyRoundTrip(t *testing.T) {
	manifest := validBaselineManifest()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBaselineManifest(data)
	if err != nil {
		t.Fatalf("DecodeBaselineManifest() error = %v", err)
	}
	if !decoded.Matches(manifest.Generation, BaselineIdentity{MainCommit: manifest.MainCommit, MainTree: manifest.MainTree, Platform: manifest.Platform, PolicyDigest: manifest.PolicyDigest, ToolchainDigest: manifest.ToolchainDigest, RuntimeImage: manifest.RuntimeImage}) {
		t.Fatal("decoded manifest does not match its identity")
	}
}

func TestBaselineManifestRejectsLegacySchemaWithMigrationRequired(t *testing.T) {
	_, err := DecodeBaselineManifest([]byte(`{"schema_version":10,"generation":1,"storage_mode":"anchor"}`))
	if !errors.Is(err, ErrRemoteBaselineMigrationRequired) {
		t.Fatalf("DecodeBaselineManifest() error = %v, want migration-required", err)
	}
}

func TestBaselineManifestRejectsUnknownAndMultipleJSONValues(t *testing.T) {
	data, err := json.Marshal(validBaselineManifest())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{append(data[:len(data)-1], []byte(`,"layers":[]}`)...), append(data, []byte(` {}`)...)} {
		if _, err := DecodeBaselineManifest(input); err == nil {
			t.Fatal("DecodeBaselineManifest() accepted invalid wire data")
		}
	}
}

func TestBaselineManifestValidationRequiresOCIProjectCache(t *testing.T) {
	manifest := validBaselineManifest()
	manifest.OCIProjectCache = nil
	if err := manifest.Validate(); err == nil {
		t.Fatal("Validate() accepted missing OCIProjectCache")
	}
}

func TestBaselineManifestFieldRegistry(t *testing.T) {
	if BaselineManifestSchemaVersion != 11 {
		t.Fatalf("BaselineManifestSchemaVersion = %d", BaselineManifestSchemaVersion)
	}
	assertBaselineFields(t, reflect.TypeFor[BaselineManifest](), []string{"SchemaVersion", "Generation", "MainCommit", "MainTree", "Platform", "PolicyDigest", "ToolchainDigest", "RuntimeImage", "OCIProjectCache", "GateSourceSHA256", "GateBinarySHA256", "GateBinarySize", "RuntimeSeedManifestSHA256", "RuntimeDependencyDigest", "CABundleSHA256", "CABundleSize"})
}

func validBaselineManifest() BaselineManifest {
	state := validBaselineState()
	return BaselineManifest{SchemaVersion: BaselineManifestSchemaVersion, Generation: state.Generation, MainCommit: state.MainCommit, MainTree: state.MainTree, Platform: state.Platform, PolicyDigest: state.PolicyDigest, ToolchainDigest: state.ToolchainDigest, RuntimeImage: state.RuntimeImage, OCIProjectCache: validBaselineOCIProjectCache(state.MainTree, state.ToolchainDigest, state.Platform, state.RuntimeImage), GateSourceSHA256: digest("f"), GateBinarySHA256: state.GateBinarySHA256, GateBinarySize: 2048, RuntimeSeedManifestSHA256: state.RuntimeSeedSHA256, RuntimeDependencyDigest: digest("7"), CABundleSHA256: digest("3"), CABundleSize: 2048}
}
