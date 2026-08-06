package main

import (
	"slices"
	"testing"

	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
)

func TestNewRegistryDoesNotIncludeMemoryTools(t *testing.T) {
	registry := newRegistry(newRegistryParams{})
	if _, ok := registry.Lookup("memory_read"); ok {
		t.Fatal("memory_read must not be registered by mcp-orch")
	}
	if _, ok := registry.Lookup("memory_write"); ok {
		t.Fatal("memory_write must not be registered by mcp-orch")
	}
}

func TestBuildBootstrapConfigDoesNotAdvertiseMemoryCapability(t *testing.T) {
	cfg := buildBootstrapConfig(nil, nil, newRegistry(newRegistryParams{}), platformmetrics.NewBootstrapMetrics())
	if slices.Contains(cfg.Capabilities, "tools/memory") {
		t.Fatalf("Capabilities = %#v, want no tools/memory", cfg.Capabilities)
	}
}
