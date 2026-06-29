package thread

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeConfigMap_ValidJSON(t *testing.T) {
	t.Parallel()

	got, err := decodeConfigMap(json.RawMessage(`{"env":{"KEY":"VALUE"}}`))
	if err != nil {
		t.Fatalf("decodeConfigMap() error = %v", err)
	}
	if got == nil {
		t.Fatal("decodeConfigMap() = nil, want object")
	}
	env, ok := got["env"].(map[string]any)
	if !ok {
		t.Fatalf("decodeConfigMap()[\"env\"] = %#v, want object", got["env"])
	}
	if env["KEY"] != "VALUE" {
		t.Fatalf("decodeConfigMap()[\"env\"][\"KEY\"] = %#v, want VALUE", env["KEY"])
	}
}

func TestDecodeConfigMap_Null(t *testing.T) {
	t.Parallel()

	got, err := decodeConfigMap(json.RawMessage(`null`))
	if err != nil {
		t.Fatalf("decodeConfigMap(null) error = %v", err)
	}
	if got != nil {
		t.Fatalf("decodeConfigMap(null) = %#v, want nil", got)
	}
}

func TestDecodeConfigMap_EmptyObject(t *testing.T) {
	t.Parallel()

	got, err := decodeConfigMap(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("decodeConfigMap({}) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("decodeConfigMap({}) = %#v, want nil or empty map", got)
	}
}

func TestDecodeConfigMap_InvalidJSON(t *testing.T) {
	t.Parallel()

	if got, err := decodeConfigMap(json.RawMessage(`"not json"`)); err == nil {
		t.Fatalf("decodeConfigMap(\"not json\") = %#v, want error", got)
	}
}

func TestDecodeConfigMap_Array(t *testing.T) {
	t.Parallel()

	if got, err := decodeConfigMap(json.RawMessage(`[1,2,3]`)); err == nil {
		t.Fatalf("decodeConfigMap([1,2,3]) = %#v, want error", got)
	}
}

func TestDecodeConfigMap_Nil(t *testing.T) {
	t.Parallel()

	got, err := decodeConfigMap(nil)
	if err != nil {
		t.Fatalf("decodeConfigMap(nil) error = %v", err)
	}
	if got != nil {
		t.Fatalf("decodeConfigMap(nil) = %#v, want nil", got)
	}
}

func TestDecodeConfigMapRejectsRawMCPConfig(t *testing.T) {
	t.Parallel()

	_, err := decodeConfigMap(json.RawMessage(`{"mcpConfig":{"mcpServers":{"shell":{"transport":"stdio","command":"bash","args":["-lc","env"]}}}}`))
	if err == nil {
		t.Fatal("decodeConfigMap(raw mcpConfig) error = nil, want runtime MCP trust-boundary rejection")
	}
	if !strings.Contains(err.Error(), "mcpConfig") || !strings.Contains(err.Error(), "trusted MCP server") {
		t.Fatalf("decodeConfigMap(raw mcpConfig) error = %v, want trusted MCP server rejection", err)
	}
}
