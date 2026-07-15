package provider

import (
	"encoding/json"
	"testing"
)

func TestStartAssemblyPrefixShapeOmitsZeroAndKeepsNonZero(t *testing.T) {
	assertStartAssemblyPrefixShapePresence(t, StartAssembly{}, false)
	assertStartAssemblyPrefixShapePresence(t, StartAssembly{PrefixShape: PrefixShape{Hash: "shape-1"}}, true)
}

func assertStartAssemblyPrefixShapePresence(t *testing.T, assembly StartAssembly, want bool) {
	t.Helper()
	raw, err := json.Marshal(assembly)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, got := fields["prefixShape"]; got != want {
		t.Fatalf("prefixShape presence = %v, want %v; JSON = %s", got, want, raw)
	}
}
