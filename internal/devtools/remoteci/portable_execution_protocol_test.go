package remoteci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestPortableExecutionProtocol 聚合本卡全部协议拒绝与身份守卫，供受控 host test 精确选择。
func TestPortableExecutionProtocol(t *testing.T) {
	t.Run("canonical_roundtrip", TestPortableExecutionRequestCanonicalRoundTrip)
	t.Run("strict_json", TestPortableExecutionRequestStrictJSON)
	t.Run("required_fields", TestPortableExecutionRequestEveryIdentityLeafIsRequired)
	t.Run("binding_drift", TestPortableExecutionRequestRejectsBindingDrift)
	t.Run("dynamic_abi", TestPortableExecutionProtocolABIDigestDynamicallyTracksDTOs)
}

// TestPortableExecutionRequestCanonicalRoundTrip 锁定 normal portable execute request 的唯一 canonical wire。
func TestPortableExecutionRequestCanonicalRoundTrip(t *testing.T) {
	request := validPortableExecutionRequest(t)
	encoded, err := request.CanonicalEncode()
	if err != nil {
		t.Fatalf("CanonicalEncode() error = %v", err)
	}
	decoded, err := DecodePortableExecutionRequest(encoded)
	if err != nil {
		t.Fatalf("DecodePortableExecutionRequest() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, request) {
		t.Fatalf("roundtrip = %#v, want %#v", decoded, request)
	}
	want, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, want) {
		t.Fatal("portable execution request encoding is not canonical JSON")
	}
}

// TestPortableExecutionRequestStrictJSON 锁定 null、unknown、duplicate、trailing 与非 canonical 拒绝。
func TestPortableExecutionRequestStrictJSON(t *testing.T) {
	canonical := mustPortableExecutionJSON(t, validPortableExecutionRequest(t))
	cases := map[string][]byte{
		"null":         []byte("null"),
		"unknown":      append(canonical[:len(canonical)-1], []byte(`,"unknown":1}`)...),
		"trailing":     append(append([]byte(nil), canonical...), []byte("\n{}")...),
		"noncanonical": append([]byte("\n"), canonical...),
		"null force":   bytes.Replace(canonical, []byte(`"force":false`), []byte(`"force":null`), 1),
		"nested null": bytes.Replace(
			canonical,
			mustPortableExecutionJSON(t, validPortableExecutionRequest(t).Authority),
			[]byte("null"),
			1,
		),
		"missing force": bytes.Replace(canonical, []byte(`,"force":false`), nil, 1),
	}
	duplicate := strings.Replace(string(canonical), `"schema_version":`, `"schema_version":"portable-remote-ci-execution/v1","schema_version":`, 1)
	cases["duplicate"] = []byte(duplicate)
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePortableExecutionRequest(payload); err == nil {
				t.Fatal("strict decoder accepted invalid JSON")
			}
		})
	}
}

// TestPortableExecutionRequestEveryIdentityLeafIsRequired 动态枚举 DTO 叶字段并拒绝零值身份。
func TestPortableExecutionRequestEveryIdentityLeafIsRequired(t *testing.T) {
	request := validPortableExecutionRequest(t)
	assertEveryPortableExecutionIdentityLeafRequired(t, reflect.ValueOf(&request).Elem(), nil)
}

// TestPortableExecutionRequestRejectsBindingDrift 锁定 engine、target、authority 与 audit 交叉绑定。
func TestPortableExecutionRequestRejectsBindingDrift(t *testing.T) {
	request := validPortableExecutionRequest(t)
	builderSimulationABI, err := BuilderSimulationProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PortableExecutionRequest){
		"artifact identity": func(value *PortableExecutionRequest) {
			value.Engine.Artifact.IdentityDigest = portableExecutionDigest("1")
		},
		"builder simulation ABI": func(value *PortableExecutionRequest) {
			value.Engine.Artifact.ProtocolABIDigest = builderSimulationABI
		},
		"source baseline kind": func(value *PortableExecutionRequest) {
			value.Engine.Artifact.Kind = PortableSourceBaselineArtifactKind
		},
		"source baseline contract": func(value *PortableExecutionRequest) {
			value.Engine.Artifact.ContractID = PortableBundleContractID
		},
		"artifact receipt": func(value *PortableExecutionRequest) { value.Engine.Artifact.ReceiptSHA256 = "" },
		"workload ABI": func(value *PortableExecutionRequest) {
			value.Engine.Artifact.WorkloadManifestABIDigest = portableExecutionDigest("2")
		},
		"target repository": func(value *PortableExecutionRequest) { value.Target.RepositoryID = "other-target" },
		"target mount":      func(value *PortableExecutionRequest) { value.Target.MountPath = "../target" },
		"relative config path": func(value *PortableExecutionRequest) {
			value.Authority.ConfigPath = "remote-ci.json"
		},
		"shared authority path": func(value *PortableExecutionRequest) {
			value.Authority.LedgerPath = value.Authority.ConfigPath
		},
		"accepted authority": func(value *PortableExecutionRequest) { value.Authority.AcceptedGeneration = 0 },
		"agent token":        func(value *PortableExecutionRequest) { value.Audit.AgentTokenDigest = "raw-token" },
		"profile":            func(value *PortableExecutionRequest) { value.Audit.Profile = "unknown" },
		"entrypoint":         func(value *PortableExecutionRequest) { value.Audit.Entrypoint = "unknown" },
		"local pre-commit": func(value *PortableExecutionRequest) {
			value.Audit.Entrypoint = gate.CIEntrypointGitPreCommit
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() accepted portable execution binding drift")
			}
		})
	}
}

// TestPortableExecutionProtocolABIDigestDynamicallyTracksDTOs 锁定动态 DTO ABI 与其他协议域隔离。
func TestPortableExecutionProtocolABIDigestDynamicallyTracksDTOs(t *testing.T) {
	first, err := PortableExecutionProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	second, err := PortableExecutionProtocolABIDigest()
	if err != nil || first != second || !validPortableDigest(first) {
		t.Fatalf("portable execution ABI digest = %q/%q error=%v", first, second, err)
	}
	builderABI, err := BuilderSimulationProtocolABIDigest()
	if err != nil || first == builderABI {
		t.Fatalf("builder ABI digest = %q, portable execution ABI = %q, error=%v", builderABI, first, err)
	}
	for _, required := range []reflect.Type{
		reflect.TypeFor[PortableExecutionRequest](), reflect.TypeFor[PortableExecutionEngine](),
		reflect.TypeFor[PortableExecutionArtifact](), reflect.TypeFor[PortableExecutionTarget](),
		reflect.TypeFor[PortableExecutionAuthority](), reflect.TypeFor[PortableExecutionAudit](),
		reflect.TypeFor[PortableWorkloadManifest](),
	} {
		if !slicesContainsType(portableExecutionProtocolABIRootTypes(), required) {
			t.Fatalf("portable execution ABI roots omit %s", required)
		}
	}
}

func validPortableExecutionRequest(t *testing.T) PortableExecutionRequest {
	t.Helper()
	artifactProtocolABI, err := PortableExecutionProtocolABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	workloadABI, err := RepositoryWorkloadManifestABIDigest()
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := portableExecutionDigest("a")
	builderDigest := portableExecutionDigest("b")
	toolchainDigest := portableExecutionDigest("c")
	artifact := PortableExecutionArtifact{
		ContractID: PortableExecutionRuntimeArtifactContractID, Kind: PortableExecutionRuntimeArtifactKind,
		ArtifactKey: "portable-engine/artifacts/engine.runtime", ArtifactSHA256: artifactSHA, ArtifactSizeBytes: 4096,
		ReceiptKey: "portable-engine/receipts/engine.json", ReceiptSHA256: portableExecutionDigest("f"),
		BuilderDigest: builderDigest, ToolchainDigest: toolchainDigest,
		ProtocolABIDigest: artifactProtocolABI, WorkloadManifestABIDigest: workloadABI,
	}
	identity, err := PortableExecutionRuntimeArtifactIdentityDigest(
		"remote-ci-engine", "sha1", strings.Repeat("d", 40), strings.Repeat("e", 40), artifact,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact.IdentityDigest = identity
	return PortableExecutionRequest{
		SchemaVersion:     PortableExecutionRequestSchema,
		ContractID:        PortableExecutionContractID,
		ExecutionProvider: cicontract.ExecutionProviderID,
		Engine: PortableExecutionEngine{
			RepositoryID: "remote-ci-engine", ObjectFormat: "sha1",
			CommitSHA: strings.Repeat("d", 40), TreeSHA: strings.Repeat("e", 40),
			Artifact: artifact,
		},
		Target: PortableExecutionTarget{
			RepositoryID: "target-repository", ObjectFormat: "sha1",
			CommitSHA: strings.Repeat("1", 40), TreeSHA: strings.Repeat("2", 40),
			MountPath: "targets/target-repository",
			WorkloadManifest: PortableWorkloadManifest{
				RepoRelativePath: ".super-dolphin/remote-ci-workloads.json", SHA256: portableExecutionDigest("3"),
				ABIDigest: workloadABI, SchemaVersion: PortableWorkloadManifestSchemaVersion, RepositoryID: "target-repository",
			},
		},
		Authority: PortableExecutionAuthority{
			ConfigPath: "/authority/remote-ci.json", ConfigSHA256: portableExecutionDigest("4"),
			LedgerPath: "/authority/duration-ledger.sqlite", LedgerIdentity: "ledger-file-identity-1",
			AcceptedGeneration: 22, AcceptedAuthoritySHA256: portableExecutionDigest("5"),
		},
		Audit: PortableExecutionAudit{
			AgentTokenDigest: portableExecutionDigest("6"), Profile: gate.ProfileRemoteRequired,
			Entrypoint: gate.CIEntrypointManualCLI, Force: false,
		},
	}
}

func assertEveryPortableExecutionIdentityLeafRequired(t *testing.T, value reflect.Value, fieldPath []string) {
	t.Helper()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		info := value.Type().Field(index)
		path := append(append([]string(nil), fieldPath...), info.Name)
		if field.Kind() == reflect.Struct {
			assertEveryPortableExecutionIdentityLeafRequired(t, field, path)
			continue
		}
		if field.Kind() == reflect.Bool {
			continue
		}
		candidate := validPortableExecutionRequest(t)
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

func mustPortableExecutionJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func portableExecutionDigest(hexDigit string) string {
	return "sha256:" + strings.Repeat(hexDigit, 64)
}

func slicesContainsType(values []reflect.Type, required reflect.Type) bool {
	for _, value := range values {
		if value == required {
			return true
		}
	}
	return false
}
