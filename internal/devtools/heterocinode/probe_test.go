package heterocinode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type factsProviderFunc func(context.Context, Config, heteroci.ProbeRequest) (Facts, error)

func (function factsProviderFunc) Collect(ctx context.Context, config Config, request heteroci.ProbeRequest) (Facts, error) {
	return function(ctx, config, request)
}

func TestProbeProducesCoreConsumableResponse(t *testing.T) {
	config := testConfig()
	request := testRequest(t, config.Node, 7)
	now := time.UnixMilli(1_780_000_000_000)
	encoded, err := Probe(context.Background(), config, request, testFactsProvider(config), now)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	response, err := heteroci.DecodeProbeResponse(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatalf("DecodeProbeResponse() error = %v", err)
	}
	if response.NodeID != config.Node.NodeID || response.OwnerEpoch != 7 || response.HeartbeatUnixMilli != now.UnixMilli() || response.Available.CPUMilli != 3_000 {
		t.Fatalf("response = %#v", response)
	}
}

func TestProbeRejectsIdentityCapacityAndProviderFailures(t *testing.T) {
	config := testConfig()
	tests := []struct {
		name     string
		request  []byte
		provider FactsProvider
	}{
		{name: "node drift", request: testRequestForNode(t, "other", config.Node.ProbeIdentity, 1), provider: testFactsProvider(config)},
		{name: "capacity drift", request: testRequest(t, config.Node, 1), provider: factsProviderFunc(func(context.Context, Config, heteroci.ProbeRequest) (Facts, error) {
			facts, _ := testFactsProvider(config).Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 1})
			facts.Available.CPUMilli = facts.Total.CPUMilli + 1
			return facts, nil
		})},
		{name: "provider", request: testRequest(t, config.Node, 1), provider: factsProviderFunc(func(context.Context, Config, heteroci.ProbeRequest) (Facts, error) {
			return Facts{}, errors.New("provider failed")
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Probe(context.Background(), config, test.request, test.provider, time.Now()); err == nil {
				t.Fatal("Probe() accepted invalid input")
			}
		})
	}
}

func testConfig() Config {
	return Config{
		SchemaVersion: ConfigSchemaVersion,
		Node: heteroci.NodeConfig{
			NodeID: "node-a", Endpoint: "ssh://node-a", ProbeIdentity: "sha256:" + strings.Repeat("a", 64),
			Platform: heteroci.PlatformLinuxAMD64,
			Capabilities: []heteroci.CapabilityConfig{
				{Runtime: heteroci.RuntimeUbuntuContainer, Image: "example.invalid/ubuntu", ImageDigest: "sha256:" + strings.Repeat("b", 64)},
				{Runtime: heteroci.RuntimeUbuntuWineContainer, Image: "example.invalid/ubuntu-wine", ImageDigest: "sha256:" + strings.Repeat("c", 64), SourceRevision: "https://github.com/wine-mirror/wine@refs/tags/wine-9.0", QualificationSuite: "wine-windows-amd64/v1"},
			},
		},
	}
}

func testFactsProvider(config Config) FactsProvider {
	return factsProviderFunc(func(context.Context, Config, heteroci.ProbeRequest) (Facts, error) {
		active := make([]heteroci.ActiveCapabilitySnapshot, len(config.Node.Capabilities))
		for index, capability := range config.Node.Capabilities {
			active[index] = heteroci.ActiveCapabilitySnapshot{Runtime: capability.Runtime, ImageDigest: capability.ImageDigest}
		}
		return Facts{BootID: "boot-a", PhysicalAttestation: testPhysicalAttestation(), Capabilities: active, Total: heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 << 30, MaxConcurrency: 4}, Available: heteroci.ResourceCapacity{CPUMilli: 3_000, MemoryBytes: 6 << 30, MaxConcurrency: 3}}, nil
	})
}

func testPhysicalAttestation() heteroci.PhysicalHostAttestation {
	attestation := heteroci.PhysicalHostAttestation{
		SchemaVersion: heteroci.PhysicalHostAttestationSchemaVersion,
		OSID:          "ubuntu", Machine: "x86_64", MachineIDSHA256: "sha256:" + strings.Repeat("1", 64), OSReleaseIdentity: "sha256:" + strings.Repeat("2", 64), UnameIdentity: "sha256:" + strings.Repeat("3", 64), DockerOSType: "linux", DockerArchitecture: "x86_64",
	}
	attestation.Digest = attestation.CanonicalDigest()
	return attestation
}

func testRequest(t *testing.T, node heteroci.NodeConfig, epoch heteroci.RoutingOwnerEpoch) []byte {
	t.Helper()
	return testRequestForNode(t, node.NodeID, node.ProbeIdentity, epoch)
}

func testRequestForNode(t *testing.T, nodeID, probeIdentity string, epoch heteroci.RoutingOwnerEpoch) []byte {
	t.Helper()
	encoded, err := json.Marshal(heteroci.ProbeRequest{
		SchemaVersion: heteroci.ProbeRequestSchemaVersion, NodeID: nodeID,
		ProbeIdentity: probeIdentity, OwnerEpoch: epoch, CapturedAtUnixMilli: 1_780_000_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
