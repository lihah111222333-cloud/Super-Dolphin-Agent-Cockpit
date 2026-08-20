package remoteci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
		Workloads: []RepositoryWorkload{
			{
				ID:              "go-test/internal/devtools/remoteci/TestPortableManifest",
				Kind:            RepositoryWorkloadKindGoTest,
				ModuleRoot:      ".",
				Package:         "./internal/devtools/remoteci",
				Name:            "TestPortableManifest",
				Platform:        RepositoryWorkloadPlatformLinuxAMD64,
				ExecutionPolicy: RepositoryWorkloadExecutionPolicyNormal,
			},
		},
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
	manifest.Workloads[0].ExecutionDigest = decoded.Workloads[0].ExecutionDigest
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
	if decoded.ManifestDigest == "" || decoded.Workloads[0].ExecutionDigest == "" {
		t.Fatal("canonical encoding did not freeze manifest and entry digests")
	}
	if decoded.Workloads[0].ExecutionDigest != "sha256:898612e2d3b0bc99a3923fe271db2a157c86367c4e13827bf551c8e2650a10ac" {
		t.Fatalf("entry digest changed: %s", decoded.Workloads[0].ExecutionDigest)
	}
	if decoded.ManifestDigest != "sha256:c4565b1791f85e76684386605e2505a9a1cb2f951d6a0acbd0640ef8a1d2ba8a" {
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
		"unknown":   appendJSONBeforeObjectEnd(encoded, `,"raw_argv":["go","test"]`),
		"duplicate": appendJSONBeforeObjectEnd(encoded, `,"repository_id":"other"`),
		"null":      bytes.Replace(encoded, []byte(`"repository_id":"github.com/example/repository"`), []byte(`"repository_id":null`), 1),
		"trailing":  append(encoded, []byte(`{}`)...),
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

func TestRepositoryWorkloadManifestRecomputesCallerDigests(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	manifest.ManifestDigest = "sha256:" + strings.Repeat("f", 64)
	manifest.Workloads[0].ExecutionDigest = "sha256:" + strings.Repeat("e", 64)
	encoded, err := manifest.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() error = %v", err)
	}
	if decoded.ManifestDigest == manifest.ManifestDigest || decoded.Workloads[0].ExecutionDigest == manifest.Workloads[0].ExecutionDigest {
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
	var workloadObjects []map[string]json.RawMessage
	if err := json.Unmarshal(manifestObject["workloads"], &workloadObjects); err != nil {
		t.Fatalf("unmarshal workload objects: %v", err)
	}
	objectsByType := map[reflect.Type]map[string]json.RawMessage{
		reflect.TypeFor[RepositoryWorkloadManifest](): manifestObject,
		reflect.TypeFor[RepositoryWorkloadModule]():   moduleObjects[0],
		reflect.TypeFor[RepositoryWorkload]():         workloadObjects[0],
	}
	for typ, object := range objectsByType {
		assertRepositoryManifestJSONFieldCoverage(t, typ, object)
	}
}

func assertRepositoryManifestJSONFieldCoverage(t *testing.T, typ reflect.Type, object map[string]json.RawMessage) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
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
	if digest != "sha256:855d0f384f3108f89efa5c3752b27cd341425a226db2cd34233c6395675b9ba1" {
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
