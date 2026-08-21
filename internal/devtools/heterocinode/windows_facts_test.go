package heterocinode

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type windowsRunnerFixture struct {
	output []byte
	args   []string
}

func (fixture *windowsRunnerFixture) Run(_ context.Context, args ...string) ([]byte, error) {
	fixture.args = append([]string(nil), args...)
	return append([]byte(nil), fixture.output...), nil
}

func TestWindowsFactsProviderCollectsCIMCapacity(t *testing.T) {
	config := testConfig()
	config.Node.Platform = heteroci.PlatformWindowsAMD64
	config.Node.Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}
	runner := &windowsRunnerFixture{output: []byte(`{"processor_count":64,"total_memory_bytes":85899345920,"free_memory_bytes":64424509440,"boot_id":"2026-08-21T00:00:00Z"}`)}
	provider := &WindowsFactsProvider{runner: runner}
	facts, err := provider.Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 1})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if facts.Total.CPUMilli != 64_000 || facts.Available.MemoryBytes != 64_424_509_440 || facts.Total.MaxConcurrency != 8 || facts.Available.MaxConcurrency != 8 {
		t.Fatalf("facts = %#v", facts)
	}
	if len(runner.args) != 7 || runner.args[5] != "-EncodedCommand" {
		t.Fatalf("PowerShell args = %#v", runner.args)
	}
	if _, err := base64.StdEncoding.DecodeString(runner.args[6]); err != nil {
		t.Fatalf("encoded command is invalid: %v", err)
	}
}

func TestWindowsFactsProviderRejectsMalformedOrUnboundedFacts(t *testing.T) {
	config := testConfig()
	config.Node.Platform = heteroci.PlatformWindowsAMD64
	config.Node.Capabilities = []heteroci.CapabilityConfig{{Runtime: heteroci.RuntimeWindowsNative}}
	for name, output := range map[string][]byte{
		"unknown": []byte(`{"processor_count":64,"total_memory_bytes":10,"free_memory_bytes":5,"boot_id":"boot","extra":1}`),
		"free":    []byte(`{"processor_count":64,"total_memory_bytes":10,"free_memory_bytes":11,"boot_id":"boot"}`),
		"boot":    []byte(`{"processor_count":64,"total_memory_bytes":10,"free_memory_bytes":5,"boot_id":""}`),
	} {
		t.Run(name, func(t *testing.T) {
			provider := &WindowsFactsProvider{runner: &windowsRunnerFixture{output: output}}
			if _, err := provider.Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 1}); err == nil {
				t.Fatal("Collect() accepted invalid facts")
			}
		})
	}
}
