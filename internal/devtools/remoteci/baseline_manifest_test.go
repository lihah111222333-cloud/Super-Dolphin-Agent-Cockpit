package remoteci

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBaselineManifestV9AnchorAndDelta(t *testing.T) {
	for _, manifest := range []BaselineManifest{validAnchorManifest(), validDeltaManifest()} {
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeBaselineManifest(data)
		if err != nil {
			t.Fatalf("DecodeBaselineManifest() error = %v", err)
		}
		if !reflect.DeepEqual(decoded, manifest) {
			t.Fatalf("decoded = %#v, want %#v", decoded, manifest)
		}
	}
}

func TestBaselineManifestV9RejectsStorageAndSourceChainDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BaselineManifest)
	}{
		{"anchor lacks runtime layer", func(value *BaselineManifest) { value.Layers = value.Layers[1:] }},
		{"delta lacks source chain", func(value *BaselineManifest) { value.Layers[0].BaseCommit = "" }},
		{"delta target commit drift", func(value *BaselineManifest) { value.Layers[0].TargetCommit = value.Layers[0].BaseCommit }},
		{"delta wrong kind", func(value *BaselineManifest) { value.Layers[1].Kind = BaselineCacheKindAnchor }},
		{"delta wrong archive", func(value *BaselineManifest) { value.Layers[1].Archive = "go-build-cache.tar.gz" }},
		{"delta source metadata on go cache", func(value *BaselineManifest) { value.Layers[1].BaseCommit = value.MainCommit }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validDeltaManifest()
			if test.name[:6] == "anchor" {
				manifest = validAnchorManifest()
			}
			test.mutate(&manifest)
			assertBaselineManifestRejected(t, manifest)
		})
	}
}

func TestBaselineManifestV9AcceptsCommitOnlySourceDelta(t *testing.T) {
	manifest := validDeltaManifest()
	manifest.Layers[0].BaseTree = manifest.Layers[0].TargetTree
	if err := manifest.Validate(); err != nil {
		t.Fatalf("commit-only source delta rejected: %v", err)
	}
}

func TestBaselineManifestV9RuntimeGoDeltaIsStrict(t *testing.T) {
	manifest := validDeltaManifest()
	runtimeGo := BaselineLayer{Generation: manifest.Generation, Kind: BaselineLayerKindDelta, Name: "runtime-go", Archive: "runtime-go.delta.tar.gz", SHA256: digest("7"), Size: 8192}
	manifest.Layers = []BaselineLayer{manifest.Layers[0], runtimeGo, manifest.Layers[1]}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("runtime-go delta rejected: %v", err)
	}
	for _, mutate := range []func(*BaselineManifest){
		func(value *BaselineManifest) { value.Layers[1], value.Layers[2] = value.Layers[2], value.Layers[1] },
		func(value *BaselineManifest) { value.Layers[1].Archive = "runtime-deps.tar.gz" },
		func(value *BaselineManifest) { value.Layers = append(value.Layers, runtimeGo) },
	} {
		invalid := manifest
		invalid.Layers = append([]BaselineLayer(nil), manifest.Layers...)
		mutate(&invalid)
		assertBaselineManifestRejected(t, invalid)
	}
}

func TestBaselineManifestV10RuntimeDepsDeltaBindsDigestTransition(t *testing.T) {
	manifest := validDeltaManifest()
	runtimeDeps := BaselineLayer{
		Generation: manifest.Generation, Kind: BaselineLayerKindDelta,
		Name: "runtime-deps", Archive: "runtime-deps.delta.tar.gz", SHA256: digest("8"), Size: 8192,
		BaseRuntimeDependencyDigest: digest("6"), TargetRuntimeDependencyDigest: manifest.RuntimeDependencyDigest,
	}
	manifest.Layers = []BaselineLayer{manifest.Layers[0], runtimeDeps, manifest.Layers[1]}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("runtime dependency delta rejected: %v", err)
	}
	for name, mutate := range map[string]func(*BaselineManifest){
		"missing base": func(value *BaselineManifest) { value.Layers[1].BaseRuntimeDependencyDigest = "" },
		"unchanged transition": func(value *BaselineManifest) {
			value.Layers[1].BaseRuntimeDependencyDigest = value.Layers[1].TargetRuntimeDependencyDigest
		},
		"target drift":        func(value *BaselineManifest) { value.Layers[1].TargetRuntimeDependencyDigest = digest("9") },
		"mixed runtime layer": func(value *BaselineManifest) { value.Layers[1].Name = "runtime-go" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := manifest
			invalid.Layers = append([]BaselineLayer(nil), manifest.Layers...)
			mutate(&invalid)
			assertBaselineManifestRejected(t, invalid)
		})
	}
}

func TestBaselineManifestReadsV6V7AndV8(t *testing.T) {
	legacy := validManifestIdentity(6)
	legacy.ArchiveSHA256, legacy.ArchiveSize = digest("4"), 1024
	for _, manifest := range []BaselineManifest{legacy, validV7Manifest(), validV8Manifest()} {
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeBaselineManifest(data); err != nil {
			t.Fatalf("DecodeBaselineManifest() legacy error = %v", err)
		}
	}
}

func TestBaselineManifestV9RequiresGateSourceDigest(t *testing.T) {
	manifest := validAnchorManifest()
	manifest.GateSourceSHA256 = ""
	assertBaselineManifestRejected(t, manifest)
}

func TestBaselineManifestRejectsUnknownAndMultipleJSONValues(t *testing.T) {
	data, err := json.Marshal(validAnchorManifest())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{append(data[:len(data)-1], []byte(`,"unknown":true}`)...), append(data, []byte(` {}`)...)} {
		if _, err := DecodeBaselineManifest(input); err == nil {
			t.Fatal("DecodeBaselineManifest() accepted invalid wire data")
		}
	}
}

func TestBaselineManifestFieldRegistry(t *testing.T) {
	if BaselineManifestSchemaVersion != 10 {
		t.Fatalf("BaselineManifestSchemaVersion = %d", BaselineManifestSchemaVersion)
	}
	assertBaselineFields(t, reflect.TypeFor[BaselineManifest](), []string{"SchemaVersion", "Generation", "MainCommit", "MainTree", "Platform", "PolicyDigest", "ToolchainDigest", "RuntimeImage", "GateSourceSHA256", "GateBinarySHA256", "GateBinarySize", "RuntimeSeedManifestSHA256", "RuntimeDependencyDigest", "CABundleSHA256", "CABundleSize", "StorageMode", "Layers", "ArchiveSHA256", "ArchiveSize"})
	assertBaselineFields(t, reflect.TypeFor[BaselineLayer](), []string{"Generation", "Kind", "Name", "Archive", "SHA256", "Size", "BaseCommit", "BaseTree", "TargetCommit", "TargetTree", "BaseRuntimeDependencyDigest", "TargetRuntimeDependencyDigest"})
}

func assertBaselineManifestRejected(t *testing.T, manifest BaselineManifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBaselineManifest(data); err == nil {
		t.Fatalf("DecodeBaselineManifest() accepted invalid manifest: %#v", manifest)
	}
}

func validManifestIdentity(schema uint32) BaselineManifest {
	manifest := BaselineManifest{SchemaVersion: schema, Generation: 2, MainCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", MainTree: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Platform: "linux/amd64", PolicyDigest: digest("c"), ToolchainDigest: digest("d"), RuntimeImage: "registry.example/runtime@" + digest("e"), GateBinarySHA256: digest("1"), GateBinarySize: 2048, RuntimeSeedManifestSHA256: digest("2"), CABundleSHA256: digest("3"), CABundleSize: 2048}
	if schema == BaselineManifestSchemaVersion {
		manifest.GateSourceSHA256 = digest("f")
		manifest.RuntimeDependencyDigest = digest("7")
	}
	return manifest
}

func validAnchorManifest() BaselineManifest {
	manifest := validManifestIdentity(BaselineManifestSchemaVersion)
	manifest.StorageMode = BaselineStorageModeAnchor
	manifest.Layers = []BaselineLayer{{Generation: manifest.Generation, Kind: BaselineLayerKindAnchor, Name: "runtime-deps", Archive: "runtime-deps.tar.gz", SHA256: digest("4"), Size: 1024}, {Generation: manifest.Generation, Kind: BaselineLayerKindAnchor, Name: "source", Archive: "source.tar.gz", SHA256: digest("5"), Size: 2048}, {Generation: manifest.Generation, Kind: BaselineLayerKindAnchor, Name: "go-build-cache", Archive: "go-build-cache.tar.gz", SHA256: digest("6"), Size: 4096}}
	return manifest
}

func validDeltaManifest() BaselineManifest {
	manifest := validManifestIdentity(BaselineManifestSchemaVersion)
	manifest.StorageMode = BaselineStorageModeDelta
	manifest.Layers = []BaselineLayer{{Generation: manifest.Generation, Kind: BaselineLayerKindDelta, Name: "source", Archive: "source.delta.bundle", SHA256: digest("4"), Size: 1024, BaseCommit: "cccccccccccccccccccccccccccccccccccccccc", BaseTree: "dddddddddddddddddddddddddddddddddddddddd", TargetCommit: manifest.MainCommit, TargetTree: manifest.MainTree}, {Generation: manifest.Generation, Kind: BaselineLayerKindDelta, Name: "go-build-cache", Archive: "go-build-cache.delta.tar.gz", SHA256: digest("5"), Size: 2048}}
	return manifest
}

func validV7Manifest() BaselineManifest {
	manifest := validManifestIdentity(7)
	manifest.Layers = []BaselineLayer{{Name: "runtime-deps", Archive: "runtime-deps.tar.gz", SHA256: digest("4"), Size: 1024}, {Name: "source", Archive: "source.tar.gz", SHA256: digest("5"), Size: 2048}, {Name: "go-build-cache", Archive: "go-build-cache.tar.gz", SHA256: digest("6"), Size: 4096}}
	return manifest
}

func validV8Manifest() BaselineManifest {
	manifest := validAnchorManifest()
	manifest.SchemaVersion = anchorDeltaBaselineManifestSchemaVersion
	manifest.GateSourceSHA256 = ""
	return manifest
}
