package heteroci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

const testImageDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDecodeManifestStrictFixedFourNodeTopology(t *testing.T) {
	manifest := testManifest(4)
	decoded, err := DecodeManifest(bytes.NewReader(mustMarshalManifest(t, manifest)))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(decoded.Nodes) != 4 {
		t.Fatalf("decoded node count = %d, want 4", len(decoded.Nodes))
	}
}

func TestManifestValidateFourNodeExecutionPoolLocksNodeIDsAndRuntimeMapping(t *testing.T) {
	valid := testManifest(4)
	if err := valid.ValidateFourNodeExecutionPool(); err != nil {
		t.Fatalf("ValidateFourNodeExecutionPool(valid) = %v", err)
	}
	unknownID := valid
	unknownID.Nodes = append([]NodeConfig(nil), valid.Nodes...)
	unknownID.Nodes[0].NodeID = "linux-node-1"
	if err := unknownID.ValidateFourNodeExecutionPool(); err == nil {
		t.Fatal("ValidateFourNodeExecutionPool accepted an unknown fixed-pool node_id")
	}
	wrongMapping := valid
	wrongMapping.Nodes = append([]NodeConfig(nil), valid.Nodes...)
	wrongMapping.Nodes[0].Platform = PlatformWindowsAMD64
	wrongMapping.Nodes[0].Capabilities = []CapabilityConfig{{Runtime: RuntimeWindowsNative}}
	if err := wrongMapping.ValidateFourNodeExecutionPool(); err == nil {
		t.Fatal("ValidateFourNodeExecutionPool accepted a mismatched node_id/platform/runtime mapping")
	}
}

func TestDecodeManifestRejectsMalformedJSONAndDuplicates(t *testing.T) {
	valid := mustMarshalManifest(t, testManifest(1))
	unknown := addRootField(t, valid, "unknown_field", json.RawMessage(`true`))
	duplicate := append([]byte(`{"authority":"LOCAL_NON_AUTHORITATIVE",`), valid[1:]...)
	missingProbeIdentity := removeNodeField(t, valid, 0, "probe_identity")
	emptyNodes := addRootField(t, removeRootField(t, valid, "nodes"), "nodes", json.RawMessage(`[]`))
	nullNodes := addRootField(t, removeRootField(t, valid, "nodes"), "nodes", json.RawMessage(`null`))

	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "unknown field", raw: unknown},
		{name: "null root", raw: []byte("null")},
		{name: "trailing value", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "missing probe identity", raw: missingProbeIdentity},
		{name: "empty nodes", raw: emptyNodes},
		{name: "null nodes", raw: nullNodes},
		{name: "duplicate root field", raw: duplicate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeManifest(bytes.NewReader(tt.raw)); err == nil {
				t.Fatalf("DecodeManifest unexpectedly accepted %s", tt.name)
			}
		})
	}
}

func TestManifestRejectsDuplicateNodeAndCapabilityIDs(t *testing.T) {
	duplicateNodeID := testManifest(2)
	duplicateNodeID.Nodes[1].NodeID = duplicateNodeID.Nodes[0].NodeID
	assertManifestRejects(t, duplicateNodeID, "duplicate node_id")

	duplicateProbeIdentity := testManifest(2)
	duplicateProbeIdentity.Nodes[1].ProbeIdentity = duplicateProbeIdentity.Nodes[0].ProbeIdentity
	assertManifestRejects(t, duplicateProbeIdentity, "duplicate probe_identity")

	duplicateEndpoint := testManifest(2)
	duplicateEndpoint.Nodes[1].Endpoint = duplicateEndpoint.Nodes[0].Endpoint
	assertManifestRejects(t, duplicateEndpoint, "duplicate endpoint")

	duplicateCapability := testManifest(1)
	duplicateCapability.Nodes[0].Capabilities = append(duplicateCapability.Nodes[0].Capabilities, duplicateCapability.Nodes[0].Capabilities[0])
	assertManifestRejects(t, duplicateCapability, "duplicate capability")
}

func TestManifestRejectsInvalidProbeIdentity(t *testing.T) {
	missing := testManifest(1)
	missing.Nodes[0].ProbeIdentity = ""
	assertManifestRejects(t, missing, "missing probe_identity")

	nonSHA256 := testManifest(1)
	nonSHA256.Nodes[0].ProbeIdentity = "probe-identity-without-sha256"
	assertManifestRejects(t, nonSHA256, "non-sha256 probe_identity")
}

func TestManifestRejectsDarwinAndCapabilityMismatches(t *testing.T) {
	wrongWindowsRuntime := testManifest(4)
	wrongWindowsRuntime.Nodes[2].Capabilities = []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "ubuntu:24.04", ImageDigest: testImageDigest}}
	assertManifestRejects(t, wrongWindowsRuntime, "Ubuntu capability on Windows")

	wrongLinuxRuntime := testManifest(4)
	wrongLinuxRuntime.Nodes[0].Capabilities = []CapabilityConfig{{Runtime: RuntimeWindowsNative}}
	assertManifestRejects(t, wrongLinuxRuntime, "Windows capability on Linux")

	darwinPlatform := testManifest(4)
	darwinPlatform.Nodes[0].Platform = PlatformDarwinARM64
	assertManifestRejects(t, darwinPlatform, "Darwin platform")

	darwinRuntime := testManifest(4)
	darwinRuntime.Nodes[0].Capabilities = []CapabilityConfig{{Runtime: RuntimeDarwinNative}}
	assertManifestRejects(t, darwinRuntime, "Darwin runtime")
}

func TestManifestRejectsNonLocalAuthority(t *testing.T) {
	manifest := testManifest(1)
	manifest.Authority = "REMOTE_AUTHORITATIVE"
	assertManifestRejects(t, manifest, "non-local authority")
}
func TestManifestValidateAcceptsGenericDarwinDarlingAndDockerOSX(t *testing.T) {
	manifests := []Manifest{}
	darwin := testManifest(1)
	darwin.Nodes[0].Platform = PlatformDarwinAMD64
	darwin.Nodes[0].Capabilities = []CapabilityConfig{testDarwinNativeCapability()}
	manifests = append(manifests, darwin)
	darling := testManifest(1)
	darling.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	manifests = append(manifests, darling)
	manifests = append(manifests, testDockerOSXManifest())
	for index, manifest := range manifests {
		if err := manifest.Validate(); err != nil {
			t.Fatalf("generic manifest %d Validate() = %v", index, err)
		}
		if err := manifest.ValidateFourNodeExecutionPool(); err == nil {
			t.Fatalf("generic manifest %d unexpectedly passed fixed pool validation", index)
		}
	}
}

func TestManifestRejectsDockerOSXQualificationAndAttestationDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CapabilityConfig)
	}{
		{name: "source revision", mutate: func(capability *CapabilityConfig) { capability.SourceRevision = "https://example.invalid/source@v1" }},
		{name: "qualification suite", mutate: func(capability *CapabilityConfig) { capability.QualificationSuite = "darwin-suite" }},
		{name: "identity leading whitespace", mutate: func(capability *CapabilityConfig) { capability.ImmutableIdentity = " " + capability.ImmutableIdentity }},
		{name: "identity trailing whitespace", mutate: func(capability *CapabilityConfig) { capability.ImmutableIdentity += " " }},
		{name: "identity control", mutate: func(capability *CapabilityConfig) { capability.ImmutableIdentity += string(rune(0x7f)) }},
		{name: "experimental opt-in", mutate: func(capability *CapabilityConfig) { capability.ExperimentalOptIn = false }},
		{name: "apple hardware attestation", mutate: func(capability *CapabilityConfig) { capability.AppleHardwareAttested = false }},
		{name: "kvm attestation", mutate: func(capability *CapabilityConfig) { capability.KVM = false }},
		{name: "compliant image attestation", mutate: func(capability *CapabilityConfig) { capability.CompliantImage = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := testDockerOSXManifest()
			test.mutate(&manifest.Nodes[0].Capabilities[0])
			assertManifestRejects(t, manifest, "Docker-OSX "+test.name)
		})
	}
}
func testManifest(_ int) Manifest {
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Authority: AuthorityLocalNonAuthoritative, Nodes: make([]NodeConfig, 0, 4)}
	for i := 0; i < 4; i++ {
		id := []string{"Ubuntu-01", "Ubuntu-02", "Windows-01", "Windows-02"}[i]
		node := NodeConfig{NodeID: id, ProbeIdentity: testProbeIdentity(i), Endpoint: "https://" + id + ".invalid/runner", Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{{Runtime: RuntimeUbuntuContainer, Image: "super-dolphin-ci-runner:ubuntu", ImageDigest: testImageDigest}}}
		if i >= 2 {
			node.Platform = PlatformWindowsAMD64
			node.Capabilities = []CapabilityConfig{{Runtime: RuntimeWindowsNative}}
		}
		manifest.Nodes = append(manifest.Nodes, node)
	}
	return manifest
}

func testProbeIdentity(index int) string {
	return fmt.Sprintf("sha256:%064x", index+1)
}

func testDarlingCapability() CapabilityConfig {
	return CapabilityConfig{
		Runtime:            RuntimeDarlingCompatRuntime,
		Image:              "super-dolphin-ci-runner:darwin-compat",
		ImageDigest:        testImageDigest,
		SourceRevision:     "https://github.com/darlinghq/darling@refs/tags/v0.1.0",
		QualificationSuite: "darwin-mach-o-smoke",
	}
}

func testWindowsCapability() CapabilityConfig {
	return CapabilityConfig{
		Runtime: RuntimeWindowsNative,
	}
}

func testDarwinNativeCapability() CapabilityConfig {
	return CapabilityConfig{
		Runtime: RuntimeDarwinNative,
	}
}

func testDockerOSXManifest() Manifest {
	manifest := testManifest(1)
	manifest.Nodes[0].Capabilities = []CapabilityConfig{
		{
			Runtime:               RuntimeDockerOSXExperimental,
			Image:                 "super-dolphin-ci-runner:docker-osx-wrapper",
			ImageDigest:           testImageDigest,
			ExperimentalOptIn:     true,
			AppleHardwareAttested: true,
			KVM:                   true,
			CompliantImage:        true,
			ImmutableIdentity:     "docker-osx-guest-fixed-test-identity",
		},
		{
			Runtime:     RuntimeUbuntuContainer,
			Image:       "super-dolphin-ci-runner:ubuntu",
			ImageDigest: testImageDigest,
		},
	}
	return manifest
}

func assertManifestRejects(t *testing.T, manifest Manifest, context string) {
	t.Helper()
	if err := manifest.Validate(); err == nil {
		t.Fatalf("%s: Validate unexpectedly succeeded", context)
	}
}

func mustMarshalManifest(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest: %v", err)
	}
	return raw
}

func addRootField(t *testing.T, raw []byte, name string, value json.RawMessage) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode manifest fixture: %v", err)
	}
	fields[name] = value
	updated, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode manifest fixture: %v", err)
	}
	return updated
}

func removeRootField(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode manifest fixture: %v", err)
	}
	delete(fields, name)
	updated, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode manifest fixture: %v", err)
	}
	return updated
}

func removeNodeField(t *testing.T, raw []byte, nodeIndex int, name string) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode manifest fixture: %v", err)
	}
	var nodes []json.RawMessage
	if err := json.Unmarshal(fields["nodes"], &nodes); err != nil {
		t.Fatalf("decode manifest nodes fixture: %v", err)
	}
	if nodeIndex < 0 || nodeIndex >= len(nodes) {
		t.Fatalf("manifest fixture node index %d out of range", nodeIndex)
	}
	var nodeFields map[string]json.RawMessage
	if err := json.Unmarshal(nodes[nodeIndex], &nodeFields); err != nil {
		t.Fatalf("decode manifest node fixture: %v", err)
	}
	delete(nodeFields, name)
	updatedNode, err := json.Marshal(nodeFields)
	if err != nil {
		t.Fatalf("encode manifest node fixture: %v", err)
	}
	nodes[nodeIndex] = updatedNode
	updatedNodes, err := json.Marshal(nodes)
	if err != nil {
		t.Fatalf("encode manifest nodes fixture: %v", err)
	}
	fields["nodes"] = updatedNodes
	updated, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode manifest fixture: %v", err)
	}
	return updated
}
