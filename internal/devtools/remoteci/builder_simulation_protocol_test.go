package remoteci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestBuilderSimulationRequestCanonicalRoundTrip(t *testing.T) {
	request := validBuilderSimulationRequest(t)
	encoded, err := request.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodeBuilderSimulationRequest(encoded)
	if err != nil {
		t.Fatalf("DecodeBuilderSimulationRequest() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, request) {
		t.Fatalf("roundtrip = %#v, want %#v", decoded, request)
	}
	if !bytes.Equal(encoded, mustBuilderSimulationJSON(t, request)) {
		t.Fatal("request encoding is not canonical JSON")
	}
}

func TestBuilderSimulationRequestStrictJSON(t *testing.T) {
	canonical := mustBuilderSimulationJSON(t, validBuilderSimulationRequest(t))
	cases := map[string][]byte{
		"null":         []byte("null"),
		"unknown":      append(canonical[:len(canonical)-1], []byte(`,"unknown":1}`)...),
		"trailing":     append(append([]byte(nil), canonical...), []byte("\n{}")...),
		"noncanonical": append([]byte("\n"), canonical...),
	}
	duplicate := strings.Replace(string(canonical), `"schema_version":`, `"schema_version":"remote-ci-builder-simulation-request/v1","schema_version":`, 1)
	cases["duplicate"] = []byte(duplicate)
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBuilderSimulationRequest(payload); err == nil {
				t.Fatal("strict decoder accepted invalid JSON")
			}
		})
	}
}

func TestBuilderSimulationRequestEveryWireFieldIsRequired(t *testing.T) {
	request := validBuilderSimulationRequest(t)
	assertEveryBuilderSimulationLeafRequired(t, reflect.ValueOf(&request).Elem(), nil)
}

func TestBuilderSimulationRequestRejectsABIDriftAndImplicitInputs(t *testing.T) {
	request := validBuilderSimulationRequest(t)
	for name, mutate := range map[string]func(*BuilderSimulationRequest){
		"protocol ABI": func(value *BuilderSimulationRequest) { value.Engine.ProtocolABIDigest = builderSimulationDigest("1") },
		"manifest ABI": func(value *BuilderSimulationRequest) {
			value.Target.WorkloadManifest.ABIDigest = builderSimulationDigest("2")
		},
		"HEAD":           func(value *BuilderSimulationRequest) { value.Engine.CommitSHA = "HEAD" },
		"relative root":  func(value *BuilderSimulationRequest) { value.Target.RepositoryRoot = "." },
		"mutable image":  func(value *BuilderSimulationRequest) { value.ECI.Image = "registry.example/remote:latest" },
		"default prefix": func(value *BuilderSimulationRequest) { value.Artifact.Prefix = "" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() accepted identity drift or implicit input")
			}
		})
	}
}

func TestBuilderSimulationProtocolABIDigestIsStableAndSeparated(t *testing.T) {
	first, err := BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuilderSimulationProtocolABIDigest()
	if err != nil || first != second || !validPortableDigest(first) {
		t.Fatalf("protocol ABI digest = %q/%q error=%v", first, second, err)
	}
	workload, err := RepositoryWorkloadManifestABIDigest()
	if err != nil || workload == first {
		t.Fatalf("workload ABI digest = %q, protocol = %q, error=%v", workload, first, err)
	}
	material, err := builderSimulationProtocolABIMaterial()
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		BuilderSimulationResultSchema, BuilderSimulationEventSchema,
		BuilderSimulationOutcomeSucceeded, BuilderSimulationOutcomeFailed,
		BuilderSimulationEventStageRequestValidated, BuilderSimulationEventStageTerminal,
		reflect.TypeFor[BuilderSimulationResult]().Name(), reflect.TypeFor[BuilderSimulationEvent]().Name(),
	} {
		if !bytes.Contains(material, []byte(marker)) {
			t.Fatalf("protocol ABI material is missing %q", marker)
		}
	}
}

func TestBuilderSimulationProtocolABIRejectsAmbiguousOrUnsupportedWireTypes(t *testing.T) {
	types := []reflect.Type{
		reflect.StructOf([]reflect.StructField{
			{Name: "First", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
			{Name: "Second", Type: reflect.TypeFor[string](), Tag: `json:"same"`},
		}),
		reflect.TypeOf(struct {
			Values map[string]string `json:"values"`
		}{}),
	}
	for _, value := range types {
		var material bytes.Buffer
		if err := appendBuilderSimulationABIType(&material, value); err == nil {
			t.Fatalf("ABI accepted unsupported type %s", value)
		}
	}
}

func validBuilderSimulationRequest(t *testing.T) BuilderSimulationRequest {
	t.Helper()
	protocolABI, err := BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	workloadABI, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	return BuilderSimulationRequest{
		SchemaVersion: BuilderSimulationRequestSchema, ContractID: BuilderSimulationContractID,
		OperationID: "operation-1", ExecutionProvider: cicontract.ExecutionProviderID,
		Engine: BuilderSimulationEngine{RepositoryRoot: "/engine", RepositoryID: "remote-ci-engine", ObjectFormat: "sha1",
			CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40), ExpectedBuilderDigest: builderSimulationDigest("c"),
			ExpectedToolchainDigest: builderSimulationDigest("d"), ProtocolABIDigest: protocolABI},
		Target: BuilderSimulationTarget{RepositoryRoot: "/target", RepositoryID: "target-repository", ObjectFormat: "sha1",
			CommitSHA: strings.Repeat("e", 40), TreeSHA: strings.Repeat("f", 40), WorkloadManifest: PortableWorkloadManifest{
				RepoRelativePath: ".super-dolphin/remote-ci-workloads.json", SHA256: builderSimulationDigest("1"), ABIDigest: workloadABI,
				SchemaVersion: PortableWorkloadManifestSchemaVersion, RepositoryID: "target-repository"}},
		Artifact: BuilderSimulationArtifact{Prefix: "portable-engine/operation-1/"},
		ECI: BuilderSimulationECI{AliyunCLI: "/usr/local/bin/aliyun", CredentialProfile: "remote-ci", RegionID: "cn-test",
			VSwitches:       []cicontract.ECIVSwitch{{ID: "vsw-zone-a", ZoneID: "cn-test-a"}, {ID: "vsw-zone-b", ZoneID: "cn-test-b"}},
			SecurityGroupID: "sg-test", RAMRoleName: "role-test", ImageCacheID: "cache-test", ImageSnapshotID: "snapshot-test",
			Image: "registry.example/remote@" + builderSimulationDigest("2")},
		OSS: BuilderSimulationOSS{Bucket: "remote-ci-bucket", Endpoint: "https://oss.example", InternalEndpoint: "https://oss-internal.example"},
	}
}

func assertEveryBuilderSimulationLeafRequired(t *testing.T, value reflect.Value, fieldPath []string) {
	t.Helper()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		info := value.Type().Field(index)
		path := append(append([]string(nil), fieldPath...), info.Name)
		if field.Kind() == reflect.Struct {
			assertEveryBuilderSimulationLeafRequired(t, field, path)
			continue
		}
		candidate := validBuilderSimulationRequest(t)
		leaf := reflect.ValueOf(&candidate).Elem()
		for _, name := range path {
			leaf = leaf.FieldByName(name)
		}
		leaf.SetZero()
		if err := candidate.Validate(); err == nil {
			t.Fatalf("Validate() accepted zero field %s", strings.Join(path, "."))
		}
	}
}

func mustBuilderSimulationJSON(t *testing.T, request BuilderSimulationRequest) []byte {
	t.Helper()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func builderSimulationDigest(hexDigit string) string {
	return "sha256:" + strings.Repeat(hexDigit, 64)
}
