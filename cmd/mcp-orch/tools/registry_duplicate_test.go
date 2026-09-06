package tools

import (
	"context"
	"strings"
	"testing"
)

func TestNewRegistryFromToolDefinitionsRejectsDuplicateToolNames(t *testing.T) {
	registry := newRegistryFromToolDefinitions(newToolRuntimeState(), []ToolDefinition{
		{Name: "duplicate-tool"},
		{Name: "duplicate-tool"},
	})

	if registry.initErr == nil {
		t.Fatal("newRegistryFromToolDefinitions() initErr = nil, want duplicate-name failure")
	}
	if !strings.Contains(registry.initErr.Error(), `duplicate tool name "duplicate-tool"`) {
		t.Fatalf("newRegistryFromToolDefinitions() initErr = %q, want duplicate tool name", registry.initErr)
	}
	if _, err := registry.List(); err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("Registry.List() error = %v, want duplicate-name failure", err)
	}

	tool, ok := registry.Lookup("duplicate-tool")
	if !ok {
		t.Fatal("Registry.Lookup() = false, want invalid registry marker")
	}
	if _, err := tool.Handler(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "duplicate tool name") {
		t.Fatalf("invalid registry handler error = %v, want duplicate-name failure", err)
	}
}
