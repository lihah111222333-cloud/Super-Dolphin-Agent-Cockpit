package tools

import "testing"

func TestMemoryToolDefinitionsExposeNoMemoryTools(t *testing.T) {
	registry := NewRegistry(Dependencies{})
	if _, ok := registry.Lookup("memory_read"); ok {
		t.Fatal("memory_read must not be exposed by mcp-orch; host-direct owns memory tools")
	}
	if _, ok := registry.Lookup("memory_write"); ok {
		t.Fatal("memory_write must not be exposed by mcp-orch; host-direct owns memory tools")
	}
}
