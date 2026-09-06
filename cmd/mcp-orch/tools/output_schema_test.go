package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRegistryOutputSchemaRejectsMismatchedResult(t *testing.T) {
	called := 0
	registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{{
		Name: "schema_guard",
		Metadata: ToolMetadata{OutputSchema: Schema{
			"type":                 "object",
			"properties":           map[string]any{"ok": map[string]any{"type": "string"}},
			"required":             []string{"ok"},
			"additionalProperties": false,
		}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			called++
			return map[string]any{"ok": 42}, nil
		},
	}})
	requireOutputSchemaRegistryReady(t, registry)
	definition, ok := registry.Lookup("schema_guard")
	if !ok {
		t.Fatal("schema_guard definition missing")
	}
	value, err := definition.Handler(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "output schema") {
		t.Fatalf("Handler() error = %v, want output schema rejection", err)
	}
	if value != nil {
		t.Fatalf("Handler() value = %#v, want nil on schema rejection", value)
	}
	if called != 1 {
		t.Fatalf("handler calls = %d, want 1", called)
	}
}

func TestRegistryOutputSchemaAcceptsMatchingResult(t *testing.T) {
	registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{{
		Name: "schema_guard",
		Metadata: ToolMetadata{OutputSchema: Schema{
			"type":       "object",
			"properties": map[string]any{"ok": map[string]any{"type": "string"}},
			"required":   []string{"ok"},
		}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"ok": "yes"}, nil
		},
	}})
	requireOutputSchemaRegistryReady(t, registry)
	definition, ok := registry.Lookup("schema_guard")
	if !ok {
		t.Fatal("schema_guard definition missing")
	}
	value, err := definition.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	if got, ok := value.(map[string]any); !ok || got["ok"] != "yes" {
		t.Fatalf("Handler() value = %#v, want matching result", value)
	}
}

func TestRegistryOutputSchemaSkipsUndeclaredResult(t *testing.T) {
	registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{
		{
			Name: "schema_absent",
			Handler: func(context.Context, json.RawMessage) (any, error) {
				return 42, nil
			},
		},
	})
	requireOutputSchemaRegistryReady(t, registry)
	definition, ok := registry.Lookup("schema_absent")
	if !ok {
		t.Fatal("schema_absent definition missing")
	}
	value, err := definition.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("Handler() error = %v, want no-schema compatibility", err)
	}
	if value != 42 {
		t.Fatalf("Handler() value = %#v, want 42", value)
	}
}

func TestRegistryOutputSchemaValidatesCanonicalStructuredContent(t *testing.T) {
	registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{{
		Name:     "array_schema_guard",
		Metadata: ToolMetadata{OutputSchema: RawObjectSchema("array compatibility")},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return []string{"a", "b"}, nil
		},
	}})
	requireOutputSchemaRegistryReady(t, registry)
	definition, ok := registry.Lookup("array_schema_guard")
	if !ok {
		t.Fatal("array_schema_guard definition missing")
	}
	if _, err := definition.Handler(context.Background(), nil); err != nil {
		t.Fatalf("Handler() error = %v, want array-to-structuredContent compatibility", err)
	}
}

func TestRegistryOutputSchemaRejectsInvalidSchemaAtConstruction(t *testing.T) {
	called := 0
	registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{{
		Name:     "invalid_schema",
		Metadata: ToolMetadata{OutputSchema: Schema{"type": "not-a-json-schema-type"}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			called++
			return map[string]any{}, nil
		},
	}})
	if _, err := registry.List(); err == nil || !strings.Contains(err.Error(), "output schema") {
		t.Fatalf("Registry.List() error = %v, want construction-time output schema rejection", err)
	}
	definition, ok := registry.Lookup("invalid_schema")
	if !ok {
		t.Fatal("invalid schema registry marker missing")
	}
	if _, err := definition.Handler(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "output schema") {
		t.Fatalf("invalid schema registry handler error = %v, want fail-closed marker", err)
	}
	if called != 0 {
		t.Fatalf("handler calls = %d, want 0 after invalid schema construction", called)
	}
}

func TestRegistryOutputSchemaPreservesHandlerErrors(t *testing.T) {
	wantErr := errors.New("handler failed")
	registry := newRegistryFromToolDefinitions(nil, []ToolDefinition{{
		Name: "schema_error",
		Metadata: ToolMetadata{OutputSchema: Schema{
			"type":     "object",
			"required": []string{"ok"},
		}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return nil, wantErr
		},
	}})
	requireOutputSchemaRegistryReady(t, registry)
	definition, ok := registry.Lookup("schema_error")
	if !ok {
		t.Fatal("schema_error definition missing")
	}
	if _, err := definition.Handler(context.Background(), nil); !errors.Is(err, wantErr) {
		t.Fatalf("Handler() error = %v, want original handler error", err)
	}
}

func requireOutputSchemaRegistryReady(t *testing.T, registry Registry) {
	t.Helper()
	if registry.initErr != nil {
		t.Fatalf("newRegistryFromToolDefinitions() initErr = %v", registry.initErr)
	}
}
