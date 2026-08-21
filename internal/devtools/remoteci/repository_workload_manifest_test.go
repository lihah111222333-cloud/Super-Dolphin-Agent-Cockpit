package remoteci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func repositoryWorkloadManifestFixture() RepositoryWorkloadManifest {
	return RepositoryWorkloadManifest{
		SchemaVersion: PortableWorkloadManifestSchemaVersion,
		RepositoryID:  "github.com/example/repository",
		Modules: []RepositoryWorkloadModule{
			{Root: ".", ModulePath: "github.com/example/repository"},
			{Root: "tools", ModulePath: "github.com/example/repository/tools"},
		},
		ExcludedNestedModules: []string{"vendor/example"},
		Profiles: []RepositoryWorkloadProfile{{
			Profile: gate.ProfileLocalFast, RequiredWorkloadIDs: []string{"go-test/internal/devtools/remoteci/TestPortableManifest"},
		}},
		Workloads: []RepositoryWorkload{
			{
				ID:                  "go-test/internal/devtools/remoteci/TestPortableManifest",
				Kind:                RepositoryWorkloadKindGoTest,
				ModuleRoot:          ".",
				Package:             "./internal/devtools/remoteci",
				Name:                "TestPortableManifest",
				Platform:            RepositoryWorkloadPlatformLinuxAMD64,
				Runtime:             RepositoryWorkloadRuntimeUbuntuContainer,
				BootstrapEstimateMS: 1_000,
				CPUMilli:            1_000,
				MemoryBytes:         1 << 30,
				ExecutionPolicy:     RepositoryWorkloadExecutionPolicyNormal,
			},
		},
	}
}

func TestRepositoryWorkloadManifestAcceptsWholeGoPackage(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.Workloads[0].Kind = RepositoryWorkloadKindGoPackage
	manifest.Workloads[0].Name = ""
	manifest.Workloads[0].Package = "."
	if _, err := manifest.CanonicalEncode(); err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	manifest.Workloads[0].Name = "TestForbidden"
	if _, err := manifest.CanonicalEncode(); err == nil {
		t.Fatal("CanonicalEncode() unexpectedly accepted go_package test name")
	}
}

func TestRepositoryWorkloadManifestRejectsMissingRuntime(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.Workloads[0].Runtime = ""
	if _, err := manifest.CanonicalEncode(); err == nil {
		t.Fatal("v5 manifest without a canonical runtime was accepted")
	}
}

func TestRepositoryWorkloadManifestRejectsMissingBootstrapRoutingResources(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.Workloads[0].BootstrapEstimateMS = 0
	manifest.Workloads[0].CPUMilli = 0
	manifest.Workloads[0].MemoryBytes = 0
	if _, err := manifest.CanonicalEncode(); err == nil {
		t.Fatal("manifest without explicit bootstrap routing resources was accepted")
	}
}

func TestRepositoryWorkloadManifestCanonicalRoundTrip(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	assertRepositoryManifestEncoding(t, encoded)
	decoded, err := DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() error = %v", err)
	}
	manifest.ManifestDigest = decoded.ManifestDigest
	manifest.Workloads[0].CommandDigest = decoded.Workloads[0].CommandDigest
	assertRepositoryManifestRoundTrip(t, decoded, manifest)
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func assertRepositoryManifestEncoding(t *testing.T, encoded []byte) {
	t.Helper()
	if len(encoded) == 0 || len(encoded) > repositoryWorkloadManifestMaxBytes {
		t.Fatalf("encoded manifest size = %d", len(encoded))
	}
}

func assertRepositoryManifestRoundTrip(t *testing.T, decoded, want RepositoryWorkloadManifest) {
	t.Helper()
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("round trip changed manifest: got %#v want %#v", decoded, want)
	}
	if decoded.ManifestDigest == "" || decoded.Workloads[0].CommandDigest == "" {
		t.Fatal("canonical encoding did not freeze manifest and entry digests")
	}
	if decoded.Workloads[0].CommandDigest != "sha256:e77898f2e71cc12d8c881b69ea9c8022ead534ea25a0e8c070b82765dd97a995" {
		t.Fatalf("command digest changed: %s", decoded.Workloads[0].CommandDigest)
	}
	if decoded.ManifestDigest != "sha256:3d2ace8d6ae6129092da89186526b21207b466db848f04da84a0e9bc5cd20e56" {
		t.Fatalf("manifest digest changed: %s", decoded.ManifestDigest)
	}
}

func TestRepositoryWorkloadManifestRejectsNonStrictJSON(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	cases := map[string][]byte{
		"unknown":                  appendJSONBeforeObjectEnd(encoded, `,"raw_argv":["go","test"]`),
		"retired execution digest": bytes.Replace(encoded, []byte(`"command_digest"`), []byte(`"execution_digest"`), 1),
		"duplicate":                appendJSONBeforeObjectEnd(encoded, `,"repository_id":"other"`),
		"null":                     bytes.Replace(encoded, []byte(`"repository_id":"github.com/example/repository"`), []byte(`"repository_id":null`), 1),
		"trailing":                 append(encoded, []byte(`{}`)...),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRepositoryWorkloadManifest(data); err == nil {
				t.Fatal("DecodeRepositoryWorkloadManifest() accepted invalid JSON")
			}
		})
	}
}

func TestRepositoryWorkloadManifestRejectsInvalidWorkloadShape(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RepositoryWorkloadManifest)
	}{
		{"kind", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].Kind = "shell" }},
		{"raw policy", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].ExecutionPolicy = "go test ./..." }},
		{"absolute module root", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].ModuleRoot = "/tmp" }},
		{"parent module root", func(manifest *RepositoryWorkloadManifest) { manifest.Modules[0].Root = "../" }},
		{"backslash package", func(manifest *RepositoryWorkloadManifest) {
			manifest.Workloads[0].Package = `.\\internal\\devtools\\remoteci`
		}},
		{"ellipsis package", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].Package = "./..." }},
		{"lowercase test suffix", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].Name = "Testportable" }},
		{"wrong platform", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].Platform = "darwin/arm64" }},
		{"missing runtime", func(manifest *RepositoryWorkloadManifest) { manifest.Workloads[0].Runtime = "" }},
		{"platform runtime drift", func(manifest *RepositoryWorkloadManifest) {
			manifest.Workloads[0].Runtime = RepositoryWorkloadRuntimeWindowsNative
		}},
		{"duplicate workload", func(manifest *RepositoryWorkloadManifest) {
			manifest.Workloads = append(manifest.Workloads, manifest.Workloads[0])
		}},
		{"excluded nested package", func(manifest *RepositoryWorkloadManifest) {
			manifest.ExcludedNestedModules = []string{"tools"}
			manifest.Workloads[0].Package = "./tools/internal"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest := repositoryWorkloadManifestFixture()
			test.mutate(&manifest)
			if _, err := manifest.CanonicalEncode(); err == nil {
				t.Fatal("CanonicalEncode() accepted invalid workload shape")
			}
		})
	}
}

func TestRepositoryWorkloadManifestAcceptsModuleRelativePackage(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.Workloads[0].ModuleRoot = "tools"
	manifest.Workloads[0].Package = "./internal/example"
	if _, err := manifest.CanonicalEncode(); err != nil {
		t.Fatalf("CanonicalEncode() rejected module-relative package: %v", err)
	}
}

func TestRepositoryWorkloadManifestAcceptsWindowsNativeWorkload(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.Workloads[0].Platform = RepositoryWorkloadPlatformWindowsAMD64
	manifest.Workloads[0].Runtime = RepositoryWorkloadRuntimeWindowsNative
	if _, err := manifest.CanonicalEncode(); err != nil {
		t.Fatalf("CanonicalEncode() rejected Windows native workload: %v", err)
	}
}

func TestRepositoryWorkloadManifestRecomputesCallerDigests(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
	manifest.Workloads[0].CommandDigest = "sha256:" + strings.Repeat("e", 64)
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() error = %v", err)
	}
	if decoded.ManifestDigest == manifest.ManifestDigest || decoded.Workloads[0].CommandDigest == manifest.Workloads[0].CommandDigest {
		t.Fatal("canonical encoding trusted caller-provided digest")
	}
}

func TestRepositoryWorkloadManifestJSONFieldGuard(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	encoded := mustCanonicalRepositoryWorkloadManifest(t, manifest)
	var manifestObject map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &manifestObject); err != nil {
		t.Fatalf("unmarshal manifest object: %v", err)
	}
	var moduleObjects []map[string]json.RawMessage
	if err := json.Unmarshal(manifestObject["modules"], &moduleObjects); err != nil {
		t.Fatalf("unmarshal module objects: %v", err)
	}
	var profileObjects []map[string]json.RawMessage
	if err := json.Unmarshal(manifestObject["profiles"], &profileObjects); err != nil {
		t.Fatalf("unmarshal profile objects: %v", err)
	}
	var workloadObjects []map[string]json.RawMessage
	if err := json.Unmarshal(manifestObject["workloads"], &workloadObjects); err != nil {
		t.Fatalf("unmarshal workload objects: %v", err)
	}
	objectsByType := map[reflect.Type]map[string]json.RawMessage{
		reflect.TypeFor[RepositoryWorkloadManifest](): manifestObject,
		reflect.TypeFor[RepositoryWorkloadProfile]():  profileObjects[0],
		reflect.TypeFor[RepositoryWorkloadModule]():   moduleObjects[0],
		reflect.TypeFor[RepositoryWorkload]():         workloadObjects[0],
	}
	for typ, object := range objectsByType {
		assertRepositoryManifestJSONFieldCoverage(t, typ, object)
	}
}

func assertRepositoryManifestJSONFieldCoverage(t *testing.T, typ reflect.Type, object map[string]json.RawMessage) {
	t.Helper()
	for field := range typ.Fields() {
		name, options, hasOptions := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" || hasOptions || options != "" {
			t.Fatalf("%s.%s has an invalid JSON field tag", typ, field.Name)
		}
		if _, ok := object[name]; !ok {
			t.Fatalf("producer field %s.%s has no JSON consumer coverage", typ, field.Name)
		}
	}
}

func appendJSONBeforeObjectEnd(data []byte, suffix string) []byte {
	trimmed := bytes.TrimSpace(data)
	return append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(suffix+"}")...)
}

func TestRepositoryWorkloadManifestABIDigestIsStableAndRoundTrips(t *testing.T) {
	digest := mustRepositoryWorkloadManifestABIDigest(t)
	if digest == "" || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("ABI digest = %q, want canonical sha256 digest", digest)
	}
	if digest != "sha256:5475c68732ecc3bfdcda2484982c23dbd6f00fa8df00425f8518de9c2ad7d312" {
		t.Fatalf("ABI digest drifted: %s", digest)
	}
	manifest := repositoryWorkloadManifestFixture()
	encoded := mustCanonicalRepositoryWorkloadManifest(t, manifest)
	if _, err := DecodeRepositoryWorkloadManifest(encoded); err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() with ABI digest error = %v", err)
	}
	if second := mustRepositoryWorkloadManifestABIDigest(t); second != digest {
		t.Fatalf("ABI digest is not stable: first=%q second=%q", digest, second)
	}
}

func mustRepositoryWorkloadManifestABIDigest(t *testing.T) string {
	t.Helper()
	digest, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatalf("RepositoryWorkloadManifestABIDigest() error = %v", err)
	}
	return digest
}

func mustCanonicalRepositoryWorkloadManifest(t *testing.T, manifest RepositoryWorkloadManifest) []byte {
	t.Helper()
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	return encoded
}

func TestRepositoryWorkloadManifestABIDigestRejectsSchemaDrift(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[string]()}}),
		reflect.StructOf([]reflect.StructField{
			{Name: "First", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
			{Name: "Second", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
		}),
		reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeFor[string](), Tag: `json:"value,omitempty"`}}),
		reflect.StructOf([]reflect.StructField{{Name: "value", PkgPath: "test", Type: reflect.TypeFor[string](), Tag: `json:"value"`}}),
		reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.PointerTo(reflect.TypeFor[string]()), Tag: `json:"value"`}}),
	} {
		if _, err := repositoryWorkloadManifestABIFields(typ); err == nil {
			t.Fatalf("ABI schema drift type %s was accepted", typ)
		}
	}
}
