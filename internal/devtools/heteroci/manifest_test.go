package heteroci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

const testImageDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDecodeManifestStrictDynamicNodeCounts(t *testing.T) {
	for _, nodeCount := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("nodes_%d", nodeCount), func(t *testing.T) {
			manifest := testManifest(nodeCount)
			decoded, err := DecodeManifest(bytes.NewReader(mustMarshalManifest(t, manifest)))
			if err != nil {
				t.Fatalf("DecodeManifest(%d nodes): %v", nodeCount, err)
			}
			if err := decoded.Validate(); err != nil {
				t.Fatalf("Validate(%d nodes): %v", nodeCount, err)
			}
			if len(decoded.Nodes) != nodeCount {
				t.Fatalf("decoded node count = %d, want %d", len(decoded.Nodes), nodeCount)
			}
		})
	}
}

func TestDecodeManifestRejectsMalformedJSONAndDuplicates(t *testing.T) {
	valid := mustMarshalManifest(t, testManifest(1))
	unknown := addRootField(t, valid, "unknown_field", json.RawMessage(`true`))
	duplicate := append([]byte(`{"authority":"LOCAL_NON_AUTHORITATIVE",`), valid[1:]...)
	missingCloudOverflow := removeRootField(t, valid, "cloud_overflow")
	missingProbeIdentity := removeNodeField(t, valid, 0, "probe_identity")
	emptyCloudOverflow := addRootField(t, removeRootField(t, valid, "cloud_overflow"), "cloud_overflow", json.RawMessage(`{}`))
	emptyNodes := addRootField(t, removeRootField(t, valid, "nodes"), "nodes", json.RawMessage(`[]`))
	nullNodes := addRootField(t, removeRootField(t, valid, "nodes"), "nodes", json.RawMessage(`null`))

	tests := []struct {
		name string
		raw  []byte
	}{
		{name: "unknown field", raw: unknown},
		{name: "null root", raw: []byte("null")},
		{name: "trailing value", raw: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "missing cloud overflow", raw: missingCloudOverflow},
		{name: "missing probe identity", raw: missingProbeIdentity},
		{name: "empty cloud overflow", raw: emptyCloudOverflow},
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

func TestManifestCloudOverflowIsStrictECIOnly(t *testing.T) {
	valid := testManifest(1)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ECI overflow manifest: %v", err)
	}

	disabled := testManifest(1)
	disabled.CloudOverflow = CloudOverflowConfig{}
	if err := disabled.Validate(); err != nil {
		t.Fatalf("disabled cloud overflow should be valid for V2: %v", err)
	}

	badProvider := testManifest(1)
	badProvider.CloudOverflow.Provider = "docker"
	assertManifestRejects(t, badProvider, "non-ECI provider")

	badPlatform := testManifest(1)
	badPlatform.CloudOverflow.Platform = "windows/amd64"
	assertManifestRejects(t, badPlatform, "non-linux/amd64 overflow")

	windowsOnly := testManifest(1)
	windowsOnly.Nodes[0].Platform = PlatformWindowsAMD64
	windowsOnly.Nodes[0].Capabilities = []CapabilityConfig{testWindowsCapability()}
	assertManifestRejects(t, windowsOnly, "enabled overflow with Windows-only inventory")

	darwinOnly := testManifest(1)
	darwinOnly.Nodes[0].Platform = PlatformDarwinARM64
	darwinOnly.Nodes[0].Capabilities = []CapabilityConfig{testDarwinNativeCapability()}
	assertManifestRejects(t, darwinOnly, "enabled overflow with Darwin-only inventory")

	withoutUbuntu := testManifest(1)
	withoutUbuntu.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	assertManifestRejects(t, withoutUbuntu, "enabled overflow without Ubuntu capability")

	withUbuntu := testManifest(2)
	withUbuntu.Nodes[0].Platform = PlatformWindowsAMD64
	withUbuntu.Nodes[0].Capabilities = []CapabilityConfig{testWindowsCapability()}
	if err := withUbuntu.Validate(); err != nil {
		t.Fatalf("enabled overflow with at least one Linux Ubuntu node should be valid: %v", err)
	}

	windowsV2 := testManifest(1)
	windowsV2.CloudOverflow = CloudOverflowConfig{}
	windowsV2.Nodes[0].Platform = PlatformWindowsAMD64
	windowsV2.Nodes[0].Capabilities = []CapabilityConfig{testWindowsCapability()}
	if err := windowsV2.Validate(); err != nil {
		t.Fatalf("disabled cloud overflow should accept a Windows-only V2 manifest: %v", err)
	}
}

func TestManifestRejectsMutuallyExclusivePlatformsAndRuntimes(t *testing.T) {
	windows := testManifest(1)
	windows.CloudOverflow = CloudOverflowConfig{}
	windows.Nodes[0].Platform = PlatformWindowsAMD64
	windows.Nodes[0].Capabilities = []CapabilityConfig{testWindowsCapability()}
	if err := windows.Validate(); err != nil {
		t.Fatalf("Windows native platform should be valid: %v", err)
	}

	darwin := testManifest(1)
	darwin.CloudOverflow = CloudOverflowConfig{}
	darwin.Nodes[0].Platform = PlatformDarwinARM64
	darwin.Nodes[0].Capabilities = []CapabilityConfig{testDarwinNativeCapability()}
	if err := darwin.Validate(); err != nil {
		t.Fatalf("Darwin native platform should be valid: %v", err)
	}

	windowsOnLinux := testManifest(1)
	windowsOnLinux.CloudOverflow = CloudOverflowConfig{}
	windowsOnLinux.Nodes[0].Capabilities = []CapabilityConfig{testWindowsCapability()}
	assertManifestRejects(t, windowsOnLinux, "Windows runtime on Linux")

	darwinOnLinux := testManifest(1)
	darwinOnLinux.CloudOverflow = CloudOverflowConfig{}
	darwinOnLinux.Nodes[0].Capabilities = []CapabilityConfig{testDarwinNativeCapability()}
	assertManifestRejects(t, darwinOnLinux, "Darwin runtime on Linux")

	windowsWithDarling := testManifest(1)
	windowsWithDarling.CloudOverflow = CloudOverflowConfig{}
	windowsWithDarling.Nodes[0].Platform = PlatformWindowsAMD64
	windowsWithDarling.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	assertManifestRejects(t, windowsWithDarling, "Darling on Windows")

	darwinWithDarling := testManifest(1)
	darwinWithDarling.CloudOverflow = CloudOverflowConfig{}
	darwinWithDarling.Nodes[0].Platform = PlatformDarwinARM64
	darwinWithDarling.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	assertManifestRejects(t, darwinWithDarling, "Darling on native Darwin")

	darlingWithNativePlatform := testManifest(1)
	darlingWithNativePlatform.CloudOverflow = CloudOverflowConfig{}
	darlingWithNativePlatform.Nodes[0].Platform = PlatformDarwinAMD64
	darlingWithNativePlatform.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	assertManifestRejects(t, darlingWithNativePlatform, "Darling/native Darwin collision")
}

func TestManifestDarlingCapabilityRequiresPinnedQualification(t *testing.T) {
	latest := testManifest(1)
	latest.CloudOverflow = CloudOverflowConfig{}
	latest.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	latest.Nodes[0].Capabilities[0].Image = "darlinghq/darling:latest"
	assertManifestRejects(t, latest, "Darling latest")

	for _, source := range []string{"main", "dev", "feature/foo"} {
		floatingSource := testManifest(1)
		floatingSource.CloudOverflow = CloudOverflowConfig{}
		floatingSource.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
		floatingSource.Nodes[0].Capabilities[0].SourceRevision = source
		assertManifestRejects(t, floatingSource, "floating Darling source "+source)
	}

	missingDigest := testManifest(1)
	missingDigest.CloudOverflow = CloudOverflowConfig{}
	missingDigest.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	missingDigest.Nodes[0].Capabilities[0].ImageDigest = ""
	assertManifestRejects(t, missingDigest, "Darling without OCI digest")

	missingQualification := testManifest(1)
	missingQualification.CloudOverflow = CloudOverflowConfig{}
	missingQualification.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	missingQualification.Nodes[0].Capabilities[0].QualificationSuite = ""
	assertManifestRejects(t, missingQualification, "Darling without qualification")

	pinned := testManifest(1)
	pinned.CloudOverflow = CloudOverflowConfig{}
	pinned.Nodes[0].Capabilities = []CapabilityConfig{testDarlingCapability()}
	if err := pinned.Validate(); err != nil {
		t.Fatalf("fixed official Darling ref should be valid: %v", err)
	}
}

func TestManifestDockerOSXIsOptInAndFullyAttested(t *testing.T) {
	defaultManifest := testManifest(1)
	if err := defaultManifest.Validate(); err != nil {
		t.Fatalf("Docker-OSX absence should remain valid: %v", err)
	}

	missing := testDockerOSXManifest()
	for _, mutate := range []struct {
		name string
		fn   func(*CapabilityConfig)
	}{
		{name: "experimental opt-in", fn: func(c *CapabilityConfig) { c.ExperimentalOptIn = false }},
		{name: "attestation", fn: func(c *CapabilityConfig) { c.AppleHardwareAttested = false }},
		{name: "kvm", fn: func(c *CapabilityConfig) { c.KVM = false }},
		{name: "compliant image", fn: func(c *CapabilityConfig) { c.CompliantImage = false }},
		{name: "digest", fn: func(c *CapabilityConfig) { c.ImageDigest = "" }},
		{name: "guest identity", fn: func(c *CapabilityConfig) { c.ImmutableIdentity = "" }},
	} {
		t.Run("missing_"+mutate.name, func(t *testing.T) {
			candidate := missing
			mutate.fn(&candidate.Nodes[0].Capabilities[0])
			assertManifestRejects(t, candidate, "Docker-OSX "+mutate.name)
		})
	}

	accepted := testDockerOSXManifest()
	if err := accepted.Validate(); err != nil {
		t.Fatalf("fully explicit Docker-OSX capability should be valid: %v", err)
	}
}

func TestManifestRejectsNonLocalAuthority(t *testing.T) {
	manifest := testManifest(1)
	manifest.Authority = "REMOTE_AUTHORITATIVE"
	assertManifestRejects(t, manifest, "non-local authority")
}

func testManifest(nodeCount int) Manifest {
	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		Authority:     AuthorityLocalNonAuthoritative,
		CloudOverflow: CloudOverflowConfig{
			Enabled:  true,
			Provider: "aliyun-eci",
			Platform: "linux/amd64",
		},
		Nodes: make([]NodeConfig, nodeCount),
	}
	for i := range manifest.Nodes {
		id := fmt.Sprintf("heteroci-node-%d", i+1)
		manifest.Nodes[i] = NodeConfig{
			NodeID:        id,
			ProbeIdentity: testProbeIdentity(i),
			Endpoint:      "https://" + id + ".invalid/runner",
			Platform:      "linux/amd64",
			Capabilities: []CapabilityConfig{{
				Runtime:     RuntimeUbuntuContainer,
				Image:       "super-dolphin-ci-runner:ubuntu",
				ImageDigest: testImageDigest,
			}},
		}
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
