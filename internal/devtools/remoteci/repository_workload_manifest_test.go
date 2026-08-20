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
	typeFields := func(typ reflect.Type) map[string]struct{} {
		fields := make(map[string]struct{}, typ.NumField())
		for field := range typ.Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Fatalf("%s.%s has no JSON field tag", typ, field.Name)
			}
			fields[name] = struct{}{}
		}
		return fields
	}
	manifestFields := typeFields(reflect.TypeFor[RepositoryWorkloadManifest]())
	if _, ok := manifestFields["manifest_digest"]; !ok {
		t.Fatal("manifest_digest is not a dynamically enumerated JSON field")
	}
	workloadFields := typeFields(reflect.TypeFor[RepositoryWorkload]())
	for _, required := range []string{"id", "kind", "module_root", "package", "name", "platform", "execution_policy", "execution_digest"} {
		if _, ok := workloadFields[required]; !ok {
			t.Fatalf("workload field %q is missing", required)
		}
	}

	zero := repositoryWorkloadManifestFixture()
	zero.ManifestDigest = ""
	zero.Workloads[0].ExecutionDigest = ""
	if _, err := json.Marshal(zero); err != nil {
		t.Fatalf("marshal zeroed digest material: %v", err)
	}
}

func appendJSONBeforeObjectEnd(data []byte, suffix string) []byte {
	trimmed := bytes.TrimSpace(data)
	return append(append([]byte(nil), trimmed[:len(trimmed)-1]...), []byte(suffix+"}")...)
}
