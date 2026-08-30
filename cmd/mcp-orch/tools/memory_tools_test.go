package tools

import "testing"

func TestMemoryToolDefinitionsExposeNoMemoryTools(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	if _, ok := registry.Lookup("memory_read"); ok {
		t.Fatal("memory_read is a retired host-tool name; provider-native memory is not exposed by mcp-orch")
	}
	if _, ok := registry.Lookup("memory_write"); ok {
		t.Fatal("memory_write is a retired host-tool name; provider-native memory is not exposed by mcp-orch")
	}
}
