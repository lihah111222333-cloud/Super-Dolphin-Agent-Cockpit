package toolbridge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestMCP20251125IconThemeAcceptsLightAndDark(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			tool, err := decodeMCPToolWire(mcpToolWithIconJSON(
				`{"src":"https://example.test/icon.png","theme":"` + theme + `"}`,
			))
			if err != nil {
				t.Fatalf("decodeMCPToolWire() error = %v", err)
			}
			if len(tool.Icons) != 1 {
				t.Fatalf("icons length = %d, want 1", len(tool.Icons))
			}
		})
	}
}

func TestMCP20251125IconThemeRejectsInvalidEnum(t *testing.T) {
	_, err := decodeMCPToolWire(mcpToolWithIconJSON(
		`{"src":"https://example.test/icon.png","theme":"contrast"}`,
	))
	if err == nil || !strings.Contains(err.Error(), `icon 0 theme "contrast" is invalid`) {
		t.Fatalf("decodeMCPToolWire() error = %v, want invalid icon theme", err)
	}
}

func TestMCP20251125IconThemeRejectsNull(t *testing.T) {
	_, err := decodeMCPToolWire(mcpToolWithIconJSON(
		`{"src":"https://example.test/icon.png","theme":null}`,
	))
	if err == nil || !strings.Contains(err.Error(), "theme must not be null") {
		t.Fatalf("decodeMCPToolWire() error = %v, want null theme rejection", err)
	}
}

func TestMCP20251125IconRejectsUnknownNestedField(t *testing.T) {
	_, err := decodeMCPToolWire(mcpToolWithIconJSON(
		`{"src":"https://example.test/icon.png","theme":"light","contrast":true}`,
	))
	if err == nil || !strings.Contains(err.Error(), `unknown field "contrast"`) {
		t.Fatalf("decodeMCPToolWire() error = %v, want unknown nested field", err)
	}
}

func TestMCP20251125IconFieldGuard(t *testing.T) {
	iconType := reflect.TypeFor[mcpToolIcon]()
	got := make(map[string]struct{}, iconType.NumField())
	for index := range iconType.NumField() {
		field := iconType.Field(index)
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" || jsonName == "-" {
			t.Fatalf("mcpToolIcon field %q lacks a JSON contract", field.Name)
		}
		if _, duplicate := got[jsonName]; duplicate {
			t.Fatalf("mcpToolIcon JSON field %q is duplicated", jsonName)
		}
		got[jsonName] = struct{}{}
	}
	want := map[string]struct{}{
		"src":      {},
		"mimeType": {},
		"sizes":    {},
		"theme":    {},
	}
	if len(got) != len(want) {
		t.Fatalf("mcpToolIcon JSON fields = %#v, want %#v", got, want)
	}
	for jsonName := range want {
		if _, ok := got[jsonName]; !ok {
			t.Fatalf("mcpToolIcon JSON fields = %#v, missing %q", got, jsonName)
		}
	}
}

func mcpToolWithIconJSON(iconJSON string) json.RawMessage {
	return json.RawMessage(
		`{"name":"icon-tool","icons":[` + iconJSON + `],"inputSchema":{"type":"object"}}`,
	)
}
