//go:build !windows

package main

import (
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func TestRuntimeServerPrepareVueBridgeNonWindowsIsStrictNoop(t *testing.T) {
	adapter, ok := multilsp.NewDefaultLanguageAdapterRegistry().AdapterForLanguage("vue")
	if !ok {
		t.Fatal("Vue adapter is not registered")
	}
	original := []string{"--stdio", "--tsdk=should-not-be-added"}
	prepared, spec, err := runtimeServerPrepareVueBridge(adapter, "/foreign/vue-language-server", original)
	if err != nil {
		t.Fatalf("runtimeServerPrepareVueBridge() error = %v", err)
	}
	if spec != nil || !reflect.DeepEqual(prepared, original) {
		t.Fatalf("non-Windows Vue bridge changed wiring: args=%#v spec=%#v", prepared, spec)
	}
	prepared[0] = "mutated"
	if original[0] == prepared[0] {
		t.Fatal("non-Windows Vue bridge did not clone arguments")
	}
}
