package thread

import (
	"encoding/json"
	"testing"
)

func TestDecodeConfigMap_ValidJSON(t *testing.T) {
	t.Parallel()

	got := decodeConfigMap(json.RawMessage(`{"env":{"KEY":"VALUE"}}`))
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

	if got := decodeConfigMap(json.RawMessage(`null`)); got != nil {
		t.Fatalf("decodeConfigMap(null) = %#v, want nil", got)
	}
}

func TestDecodeConfigMap_EmptyObject(t *testing.T) {
	t.Parallel()

	if got := decodeConfigMap(json.RawMessage(`{}`)); len(got) != 0 {
		t.Fatalf("decodeConfigMap({}) = %#v, want nil or empty map", got)
	}
}

func TestDecodeConfigMap_InvalidJSON(t *testing.T) {
	t.Parallel()

	if got := decodeConfigMap(json.RawMessage(`"not json"`)); got != nil {
		t.Fatalf("decodeConfigMap(\"not json\") = %#v, want nil", got)
	}
}

func TestDecodeConfigMap_Array(t *testing.T) {
	t.Parallel()

	if got := decodeConfigMap(json.RawMessage(`[1,2,3]`)); got != nil {
		t.Fatalf("decodeConfigMap([1,2,3]) = %#v, want nil", got)
	}
}

func TestDecodeConfigMap_Nil(t *testing.T) {
	t.Parallel()

	if got := decodeConfigMap(nil); got != nil {
		t.Fatalf("decodeConfigMap(nil) = %#v, want nil", got)
	}
}
