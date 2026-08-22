package remoteportable

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type memoryPortableGitResolver struct {
	tree string
	data []byte
}

func (resolver memoryPortableGitResolver) ResolveCommitTree(_ context.Context, _ string) (string, error) {
	return resolver.tree, nil
}

func (resolver memoryPortableGitResolver) ReadTreeBlob(_ context.Context, _, _ string) (GitTreeBlob, error) {
	return GitTreeBlob{Mode: "100644", Data: resolver.data}, nil
}

func (memoryPortableGitResolver) RepositoryFacts(context.Context) (GitRepositoryFacts, error) {
	return GitRepositoryFacts{ObjectFormat: "sha1"}, nil
}

func TestPortableDescriptorBindsExactRawTreeBlob(t *testing.T) {
	manifestBytes, input, resolver := portableBindingFixture(t)
	encoded := mustPortableRunRequestBytes(t, input)
	decoded := mustDecodePortableRunRequest(t, encoded)
	facts := portableEngineFactsFixture()
	binding := mustBindPortableRun(t, decoded, resolver, facts)
	assertPortableBindingProjection(t, manifestBytes, encoded, binding)
	assertPortableEngineFactsFrozen(t, facts, binding)
}

func mustPortableRunRequestBytes(t *testing.T, input PortableRunInput) []byte {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustDecodePortableRunRequest(t *testing.T, data []byte) PortableRunInput {
	t.Helper()
	input, err := DecodePortableRunDescriptor(data)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func mustBindPortableRun(t *testing.T, input PortableRunInput, resolver memoryPortableGitResolver, facts PortableEngineFacts) BoundPortableRun {
	t.Helper()
	binding, err := BindPortableRun(context.Background(), input, resolver, facts)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func assertPortableBindingProjection(t *testing.T, manifestBytes, encoded []byte, binding BoundPortableRun) {
	t.Helper()
	if !bytes.Equal(manifestBytes, mustPortableManifestBytes(t, binding.Manifest)) {
		t.Fatal("binding decoded a different manifest")
	}
	if binding.BindingSHA256 == portableBytesDigest(encoded) || binding.BindingSHA256 == binding.ManifestDigest {
		t.Fatal("binding digest crossed a digest domain")
	}
}

func assertPortableEngineFactsFrozen(t *testing.T, facts PortableEngineFacts, binding BoundPortableRun) {
	t.Helper()
	facts.Capabilities[0] = "mutated"
	if binding.EngineFacts.Capabilities[0] != "go" {
		t.Fatal("bound engine facts retained caller-owned capability slice")
	}
	wire, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"binary_sha256"`)) || bytes.Contains(wire, []byte(`"BinarySHA256"`)) {
		t.Fatalf("bound engine facts wire fields are not canonical: %s", wire)
	}
}

func TestPortableDescriptorRejectsStrictShapeAndLocatorDrift(t *testing.T) {
	_, input, resolver := portableBindingFixture(t)
	valid := mustPortableRunRequestBytes(t, input)
	assertPortableMalformedRequests(t, valid)
	assertPortableOperationIDs(t, input)
	decoded := input
	decoded.RepositoryID = "other"
	if _, err := BindPortableRun(context.Background(), decoded, resolver, portableEngineFactsFixture()); err == nil {
		t.Fatal("binding accepted cross-repository locator drift")
	}
}

func assertPortableMalformedRequests(t *testing.T, valid []byte) {
	t.Helper()
	cases := [][]byte{
		[]byte(`null`), append(append([]byte(nil), valid...), []byte(` {}`)...),
		[]byte(`{"schema_version":"remote-ci-portable-manifest/v1","schema_version":"remote-ci-portable-manifest/v1"}`),
		[]byte(`{"schema_version":"remote-ci-portable-manifest/v1","contract_id":"remote-ci-four-node/v1","operation_id":"x","repository_id":"fixture","object_format":"sha1","commit_sha":"bad","tree_sha":"bad","manifest_path":"ci/workloads.json","manifest_blob_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`),
	}
	for _, raw := range cases {
		if _, err := DecodePortableRunDescriptor(raw); err == nil {
			t.Fatalf("descriptor accepted malformed input %s", raw)
		}
	}
	if _, err := DecodePortableRunDescriptor(mustPortableManifestBytes(t, topManifestFixture())); err == nil {
		t.Fatal("run request decoder accepted a target manifest")
	}
}

func assertPortableOperationIDs(t *testing.T, input PortableRunInput) {
	t.Helper()
	for _, operationID := range []string{"short", strings.Repeat("A", 64), "sha256:" + strings.Repeat("a", 64)} {
		candidate := input
		candidate.OperationID = operationID
		raw := mustPortableRunRequestBytes(t, candidate)
		if _, err := DecodePortableRunDescriptor(raw); err == nil {
			t.Fatalf("descriptor accepted malformed operation_id %q", operationID)
		}
	}
}

func TestPortableManifestRejectsSelfReferentialTargetObjects(t *testing.T) {
	manifestBytes := mustPortableManifestBytes(t, topManifestFixture())
	withSelfReference := bytes.Replace(manifestBytes, []byte(`"object_format":"sha1","worktree_mount"`), []byte(`"object_format":"sha1","commit_sha":"`+strings.Repeat("9", 40)+`","tree_sha":"`+strings.Repeat("8", 40)+`","manifest_path":"ci/workloads.json","worktree_mount"`), 1)
	if bytes.Equal(manifestBytes, withSelfReference) {
		t.Fatal("fixture did not contain target object anchor")
	}
	if _, err := DecodePortableManifest(withSelfReference); err == nil {
		t.Fatal("manifest accepted self-referential commit/tree fields")
	}
}

func TestPortableBindingSeparatesOperationAndRejectsEnginePolicyDrift(t *testing.T) {
	_, input, resolver := portableBindingFixture(t)
	facts := portableEngineFactsFixture()
	first, err := BindPortableRun(context.Background(), input, resolver, facts)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := input
	secondInput.OperationID = strings.Repeat("b", 64)
	second, err := BindPortableRun(context.Background(), secondInput, resolver, facts)
	if err != nil {
		t.Fatal(err)
	}
	if first.BindingSHA256 == second.BindingSHA256 {
		t.Fatal("binding digest ignored external operation identity")
	}
	driftedFacts := facts
	driftedFacts.ProtocolABIDigest = "sha256:" + strings.Repeat("b", 64)
	if _, err := BindPortableRun(context.Background(), input, resolver, driftedFacts); err == nil {
		t.Fatal("binding accepted actual engine protocol drift")
	}
}

func TestPortableBindingCrossRepositoryV3WJFixturesUseSameEngineFacts(t *testing.T) {
	facts := portableEngineFactsFixture()
	manifestV3 := topManifestFixture()
	manifestV3.Target.RepositoryID = "super-agent-v3"
	manifestV3.Modules[0].ModulePath = "example.invalid/super-agent-v3"
	manifestWJ := topManifestFixture()
	manifestWJ.Target.RepositoryID = "wjboot-v2"
	manifestWJ.Modules[0].ModulePath = "example.invalid/wjboot-v2"
	assertPortableDualPlatformFixture(t, manifestV3)
	assertPortableDualPlatformFixture(t, manifestWJ)
	bytesV3 := mustPortableManifestBytes(t, manifestV3)
	bytesWJ := mustPortableManifestBytes(t, manifestWJ)
	inputV3 := portableCrossRepositoryInput(manifestV3, bytesV3, "a", "ci/v3.json")
	inputWJ := portableCrossRepositoryInput(manifestWJ, bytesWJ, "b", "ci/wj.json")
	bindingV3 := mustBindPortableRun(t, inputV3, memoryPortableGitResolver{tree: inputV3.TreeSHA, data: bytesV3}, facts)
	bindingWJ := mustBindPortableRun(t, inputWJ, memoryPortableGitResolver{tree: inputWJ.TreeSHA, data: bytesWJ}, facts)
	if bindingV3.ManifestDigest == bindingWJ.ManifestDigest || bindingV3.BindingSHA256 == bindingWJ.BindingSHA256 {
		t.Fatal("cross-repository V3/WJ fixtures collapsed distinct target identities")
	}
}

func assertPortableDualPlatformFixture(t *testing.T, manifest PortableManifest) {
	t.Helper()
	seen := map[string]bool{}
	for _, workload := range manifest.Workloads {
		seen[workload.TargetPlatform] = true
	}
	if !seen[string(RepositoryWorkloadPlatformLinuxAMD64)] || !seen[string(RepositoryWorkloadPlatformWindowsAMD64)] {
		t.Fatalf("portable fixture platforms = %#v, want Linux and Windows", seen)
	}
}

func portableCrossRepositoryInput(manifest PortableManifest, data []byte, operationChar, path string) PortableRunInput {
	return PortableRunInput{SchemaVersion: PortableRunRequestSchemaVersion, ContractID: PortableManifestContractID, OperationID: strings.Repeat(operationChar, 64), RepositoryID: manifest.Target.RepositoryID, ObjectFormat: "sha1", CommitSHA: strings.Repeat(operationChar, 40), TreeSHA: strings.Repeat(operationChar, 40), ManifestPath: path, ManifestBlobSHA256: portableBytesDigest(data)}
}

func portableBindingFixture(t *testing.T) ([]byte, PortableRunInput, memoryPortableGitResolver) {
	t.Helper()
	manifest := topManifestFixture()
	manifestBytes, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := PortableRunInput{SchemaVersion: PortableRunRequestSchemaVersion, ContractID: PortableManifestContractID, OperationID: strings.Repeat("a", 64), RepositoryID: manifest.Target.RepositoryID, ObjectFormat: "sha1", CommitSHA: strings.Repeat("1", 40), TreeSHA: strings.Repeat("2", 40), ManifestPath: ".super-dolphin/remote-ci-workloads.json", ManifestBlobSHA256: portableBytesDigest(manifestBytes)}
	return manifestBytes, input, memoryPortableGitResolver{tree: input.TreeSHA, data: manifestBytes}
}

func portableEngineFactsFixture() PortableEngineFacts {
	digest := "sha256:" + strings.Repeat("a", 64)
	return PortableEngineFacts{BinarySHA256: digest, ProtocolABIDigest: digest, ToolchainDigest: digest, Capabilities: []string{"go"}}
}

func mustPortableManifestBytes(t *testing.T, manifest PortableManifest) []byte {
	t.Helper()
	data, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
