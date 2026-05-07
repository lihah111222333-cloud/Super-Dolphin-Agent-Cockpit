package main

import (
	"slices"
	"testing"
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
	cfg := buildBootstrapConfig(nil, nil, newRegistry(newRegistryParams{}))
	if slices.Contains(cfg.Capabilities, "tools/memory") {
		t.Fatalf("Capabilities = %#v, want no tools/memory", cfg.Capabilities)
	}
}
