package remoteci

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRepositoryWorkloadManifestV5Contract(t *testing.T) {
	t.Run("rejects v4", testRepositoryWorkloadManifestRejectsV4)
	t.Run("separates command digest", testRepositoryWorkloadCommandDigestIgnoresRoutingAndEnvironment)
	t.Run("validates profiles", testRepositoryWorkloadProfiles)
	t.Run("guards command fields", testRepositoryWorkloadCommandDigestFieldGuard)
	t.Run("tracks command fields", testRepositoryWorkloadCommandDigestTracksSemanticFields)
	t.Run("canonical round trip", TestRepositoryWorkloadManifestCanonicalRoundTrip)
	t.Run("strict JSON", TestRepositoryWorkloadManifestRejectsNonStrictJSON)
	t.Run("JSON field guard", TestRepositoryWorkloadManifestJSONFieldGuard)
	t.Run("ABI digest", TestRepositoryWorkloadManifestABIDigestIsStableAndRoundTrips)
}

func testRepositoryWorkloadManifestRejectsV4(t *testing.T) {
	manifest := repositoryWorkloadManifestFixture()
	encoded := mustCanonicalRepositoryWorkloadManifest(t, manifest)
	v4 := bytes.Replace(encoded, []byte(PortableWorkloadManifestSchemaVersion), []byte("portable-workload-manifest/v4"), 1)
	if _, err := DecodeRepositoryWorkloadManifest(v4); err == nil {
		t.Fatal("DecodeRepositoryWorkloadManifest() accepted retired v4 bytes")
	}
}

func testRepositoryWorkloadCommandDigestIgnoresRoutingAndEnvironment(t *testing.T) {
	base := decodeCanonicalRepositoryWorkloadManifest(t, repositoryWorkloadManifestFixture())
	tests := []struct {
		name   string
		mutate func(*RepositoryWorkload)
	}{
		{name: "routing resources", mutate: func(workload *RepositoryWorkload) {
			workload.BootstrapEstimateMS++
			workload.CPUMilli++
			workload.MemoryBytes++
		}},
		{name: "platform runtime environment", mutate: func(workload *RepositoryWorkload) {
			workload.Platform = RepositoryWorkloadPlatformWindowsAMD64
			workload.Runtime = RepositoryWorkloadRuntimeWindowsNative
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := repositoryWorkloadManifestFixture()
			test.mutate(&changed.Workloads[0])
			changed = decodeCanonicalRepositoryWorkloadManifest(t, changed)
			if changed.Workloads[0].CommandDigest != base.Workloads[0].CommandDigest {
				t.Fatalf("%s changed command digest: base=%s changed=%s", test.name, base.Workloads[0].CommandDigest, changed.Workloads[0].CommandDigest)
			}
			if changed.ManifestDigest == base.ManifestDigest {
				t.Fatalf("%s did not change manifest digest", test.name)
			}
		})
	}
}

func testRepositoryWorkloadProfiles(t *testing.T) {
	workloadID := repositoryWorkloadManifestFixture().Workloads[0].ID
	tests := []struct {
		name   string
		mutate func(*RepositoryWorkloadManifest)
	}{
		{name: "missing", mutate: func(manifest *RepositoryWorkloadManifest) { manifest.Profiles = nil }},
		{name: "empty required", mutate: func(manifest *RepositoryWorkloadManifest) { manifest.Profiles[0].RequiredWorkloadIDs = nil }},
		{name: "unknown profile", mutate: func(manifest *RepositoryWorkloadManifest) { manifest.Profiles[0].Profile = gate.Profile("unknown") }},
		{name: "unknown workload", mutate: func(manifest *RepositoryWorkloadManifest) {
			manifest.Profiles[0].RequiredWorkloadIDs = []string{"unknown"}
		}},
		{name: "duplicate workload", mutate: func(manifest *RepositoryWorkloadManifest) {
			manifest.Profiles[0].RequiredWorkloadIDs = []string{workloadID, workloadID}
		}},
		{name: "unsorted workloads", mutate: func(manifest *RepositoryWorkloadManifest) {
			other := manifest.Workloads[0]
			other.ID, other.CommandDigest = "a", ""
			manifest.Workloads = append([]RepositoryWorkload{other}, manifest.Workloads...)
			manifest.Profiles[0].RequiredWorkloadIDs = []string{workloadID, "a"}
		}},
		{name: "duplicate profile", mutate: func(manifest *RepositoryWorkloadManifest) {
			manifest.Profiles = append(manifest.Profiles, manifest.Profiles[0])
		}},
		{name: "unsorted profiles", mutate: func(manifest *RepositoryWorkloadManifest) {
			manifest.Profiles = []RepositoryWorkloadProfile{
				{Profile: gate.ProfilePush, RequiredWorkloadIDs: []string{workloadID}},
				{Profile: gate.ProfileLocalFast, RequiredWorkloadIDs: []string{workloadID}},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := repositoryWorkloadManifestFixture()
			test.mutate(&manifest)
			if _, err := manifest.CanonicalEncode(); err == nil {
				t.Fatal("CanonicalEncode() accepted invalid profile binding")
			}
		})
	}
}

func testRepositoryWorkloadCommandDigestFieldGuard(t *testing.T) {
	producer := repositoryManifestJSONFields(t, reflect.TypeFor[RepositoryWorkload]())
	consumer := repositoryManifestJSONFields(t, reflect.TypeFor[repositoryWorkloadCommandMaterial]())
	exemptions := map[string]string{
		"platform":              "environment identity owner",
		"runtime":               "environment identity owner",
		"bootstrap_estimate_ms": "routing estimate owner",
		"cpu_milli":             "routing resource owner",
		"memory_bytes":          "routing resource owner",
		"command_digest":        "derived command digest output",
	}
	for field := range producer {
		if _, ok := consumer[field]; ok {
			continue
		}
		if exemptions[field] == "" {
			t.Fatalf("producer field %q has no command digest consumer or reasoned exemption", field)
		}
	}
	for field := range consumer {
		if _, ok := producer[field]; !ok {
			t.Fatalf("command digest consumer field %q is stale", field)
		}
	}
	for field, reason := range exemptions {
		if reason == "" {
			t.Fatalf("command digest exemption %q has no reason", field)
		}
		if _, ok := producer[field]; !ok {
			t.Fatalf("command digest exemption %q is stale", field)
		}
		if _, ok := consumer[field]; ok {
			t.Fatalf("command digest exemption %q duplicates a real consumer", field)
		}
	}
}

func testRepositoryWorkloadCommandDigestTracksSemanticFields(t *testing.T) {
	base := repositoryWorkloadManifestFixture().Workloads[0]
	baseDigest, err := repositoryWorkloadCommandDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*RepositoryWorkload)
	}{
		{name: "id", mutate: func(workload *RepositoryWorkload) { workload.ID += "-changed" }},
		{name: "kind", mutate: func(workload *RepositoryWorkload) { workload.Kind = RepositoryWorkloadKindGoPackage }},
		{name: "module root", mutate: func(workload *RepositoryWorkload) { workload.ModuleRoot = "tools" }},
		{name: "package", mutate: func(workload *RepositoryWorkload) { workload.Package = "." }},
		{name: "name", mutate: func(workload *RepositoryWorkload) { workload.Name += "Changed" }},
		{name: "execution policy", mutate: func(workload *RepositoryWorkload) { workload.ExecutionPolicy = RepositoryWorkloadExecutionPolicyRace }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			digest, err := repositoryWorkloadCommandDigest(changed)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseDigest {
				t.Fatal("semantic command field did not change command digest")
			}
		})
	}
}

func repositoryManifestJSONFields(t *testing.T, typ reflect.Type) map[string]struct{} {
	t.Helper()
	fields := make(map[string]struct{}, typ.NumField())
	for field := range typ.Fields() {
		name := field.Tag.Get("json")
		if name == "" {
			t.Fatalf("%s.%s is missing a JSON field name", typ, field.Name)
		}
		fields[name] = struct{}{}
	}
	return fields
}

func decodeCanonicalRepositoryWorkloadManifest(t *testing.T, manifest RepositoryWorkloadManifest) RepositoryWorkloadManifest {
	t.Helper()
	encoded := mustCanonicalRepositoryWorkloadManifest(t, manifest)
	decoded, err := DecodeRepositoryWorkloadManifest(encoded)
	if err != nil {
		t.Fatalf("DecodeRepositoryWorkloadManifest() error = %v", err)
	}
	return decoded
}
