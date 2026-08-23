package heteroci

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

const testImageDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDecodeManifestStrictSingleUbuntuWineTopology(t *testing.T) {
	manifest := testSingleUbuntuWineManifest()
	decoded, err := DecodeManifest(bytes.NewReader(mustMarshalManifest(t, manifest)))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if err := decoded.ValidateSingleUbuntuWineExecutionPool(); err != nil {
		t.Fatalf("ValidateSingleUbuntuWineExecutionPool: %v", err)
	}
}

func TestManifestValidateSingleUbuntuWineExecutionPool(t *testing.T) {
	valid := testSingleUbuntuWineManifest()
	valid.Nodes[0].NodeID = "caller-supplied-node"
	valid.Nodes[0].Endpoint = "ssh://caller-supplied-host:2222"
	if err := valid.ValidateSingleUbuntuWineExecutionPool(); err != nil {
		t.Fatalf("ValidateSingleUbuntuWineExecutionPool(valid) = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "zero nodes", mutate: func(manifest *Manifest) { manifest.Nodes = nil }},
		{name: "multiple nodes", mutate: func(manifest *Manifest) { manifest.Nodes = append(manifest.Nodes, manifest.Nodes[0]) }},
		{name: "physical Windows platform", mutate: func(manifest *Manifest) { manifest.Nodes[0].Platform = PlatformWindowsAMD64 }},
		{name: "capabilities not sorted", mutate: func(manifest *Manifest) {
			manifest.Nodes[0].Capabilities[0], manifest.Nodes[0].Capabilities[1] = manifest.Nodes[0].Capabilities[1], manifest.Nodes[0].Capabilities[0]
		}},
		{name: "missing Ubuntu capability", mutate: func(manifest *Manifest) { manifest.Nodes[0].Capabilities = []CapabilityConfig{testWineCapability()} }},
		{name: "missing Wine capability", mutate: func(manifest *Manifest) { manifest.Nodes[0].Capabilities = []CapabilityConfig{testUbuntuCapability()} }},
		{name: "retired runtime", mutate: func(manifest *Manifest) { manifest.Nodes[0].Capabilities[1].Runtime = RuntimeKind("retired_runtime") }},
		{name: "Wine image digest missing", mutate: func(manifest *Manifest) { manifest.Nodes[0].Capabilities[1].ImageDigest = "" }},
		{name: "Wine source revision missing", mutate: func(manifest *Manifest) { manifest.Nodes[0].Capabilities[1].SourceRevision = "" }},
		{name: "Wine source revision floating", mutate: func(manifest *Manifest) {
			manifest.Nodes[0].Capabilities[1].SourceRevision = "https://github.com/wine-mirror/wine@main"
		}},
		{name: "Wine qualification suite missing", mutate: func(manifest *Manifest) { manifest.Nodes[0].Capabilities[1].QualificationSuite = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := testSingleUbuntuWineManifest()
			test.mutate(&manifest)
			if err := manifest.ValidateSingleUbuntuWineExecutionPool(); err == nil {
				t.Fatal("ValidateSingleUbuntuWineExecutionPool unexpectedly succeeded")
			}
		})
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

func TestManifestRejectsInvalidProbeIdentityAndWineFields(t *testing.T) {
	missing := testManifest(1)
	missing.Nodes[0].ProbeIdentity = ""
	assertManifestRejects(t, missing, "missing probe_identity")

	nonSHA256 := testManifest(1)
	nonSHA256.Nodes[0].ProbeIdentity = "probe-identity-without-sha256"
	assertManifestRejects(t, nonSHA256, "non-sha256 probe_identity")

	wrongWineImage := testSingleUbuntuWineManifest()
	wrongWineImage.Nodes[0].Capabilities[1].Image = ""
	assertManifestRejects(t, wrongWineImage, "missing Wine image")

	wrongRuntime := testSingleUbuntuWineManifest()
	wrongRuntime.Nodes[0].Capabilities[1].Runtime = RuntimeKind("retired_runtime")
	assertManifestRejects(t, wrongRuntime, "retired runtime")
}

func TestManifestRejectsNonLocalAuthority(t *testing.T) {
	manifest := testManifest(1)
	manifest.Authority = "REMOTE_AUTHORITATIVE"
	assertManifestRejects(t, manifest, "non-local authority")
}

func testManifest(count int) Manifest {
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Authority: AuthorityLocalNonAuthoritative, Nodes: make([]NodeConfig, 0, count)}
	for i := range count {
		id := fmt.Sprintf("linux-%02d", i+1)
		manifest.Nodes = append(manifest.Nodes, NodeConfig{
			NodeID: id, ProbeIdentity: testProbeIdentity(i), Endpoint: "https://" + id + ".invalid/runner",
			Platform: PlatformLinuxAMD64, Capabilities: []CapabilityConfig{testUbuntuCapability()},
		})
	}
	return manifest
}

func testSingleUbuntuWineManifest() Manifest {
	manifest := testManifest(1)
	manifest.Nodes[0].NodeID = "wine-node"
	manifest.Nodes[0].Endpoint = "ssh://wine-host.invalid/runner"
	manifest.Nodes[0].Capabilities = []CapabilityConfig{testUbuntuCapability(), testWineCapability()}
	return manifest
}

func testUbuntuCapability() CapabilityConfig {
	return CapabilityConfig{Runtime: RuntimeUbuntuContainer, Image: "super-dolphin-ci-runner:ubuntu", ImageDigest: testImageDigest}
}

func testWineCapability() CapabilityConfig {
	return CapabilityConfig{Runtime: RuntimeUbuntuWineContainer, Image: "super-dolphin-ci-runner:ubuntu-wine", ImageDigest: testImageDigest, SourceRevision: "https://github.com/wine-mirror/wine@refs/tags/wine-9.0", QualificationSuite: "wine-windows-amd64/v1"}
}

func testProbeIdentity(index int) string {
	return fmt.Sprintf("sha256:%064x", index+1)
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
