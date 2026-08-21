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

type factsProviderFunc func(context.Context, Config) (Facts, error)

func (function factsProviderFunc) Collect(ctx context.Context, config Config) (Facts, error) {
	return function(ctx, config)
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
		{name: "capacity drift", request: testRequest(t, config.Node, 1), provider: factsProviderFunc(func(context.Context, Config) (Facts, error) {
			facts, _ := testFactsProvider(config).Collect(context.Background(), config)
			facts.Available.CPUMilli = facts.Total.CPUMilli + 1
			return facts, nil
		})},
		{name: "provider", request: testRequest(t, config.Node, 1), provider: factsProviderFunc(func(context.Context, Config) (Facts, error) {
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
			Capabilities: []heteroci.CapabilityConfig{{
				Runtime: heteroci.RuntimeUbuntuContainer, Image: "example.invalid/ubuntu",
				ImageDigest: "sha256:" + strings.Repeat("b", 64),
			}},
		},
	}
}

func testFactsProvider(config Config) FactsProvider {
	return factsProviderFunc(func(context.Context, Config) (Facts, error) {
		return Facts{
			BootID: "boot-a",
			Capabilities: []heteroci.ActiveCapabilitySnapshot{{
				Runtime: heteroci.RuntimeUbuntuContainer, ImageDigest: config.Node.Capabilities[0].ImageDigest,
			}},
			Total:     heteroci.ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 << 30, MaxConcurrency: 4},
			Available: heteroci.ResourceCapacity{CPUMilli: 3_000, MemoryBytes: 6 << 30, MaxConcurrency: 3},
		}, nil
	})
}

func testRequest(t *testing.T, node heteroci.NodeConfig, epoch heteroci.RoutingOwnerEpoch) []byte {
	t.Helper()
	return testRequestForNode(t, node.NodeID, node.ProbeIdentity, epoch)
}

func testRequestForNode(t *testing.T, nodeID, probeIdentity string, epoch heteroci.RoutingOwnerEpoch) []byte {
	t.Helper()
	encoded, err := json.Marshal(heteroci.ProbeRequest{
		SchemaVersion: heteroci.ProbeRequestSchemaVersion, NodeID: nodeID,
		ProbeIdentity: probeIdentity, OwnerEpoch: epoch,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
