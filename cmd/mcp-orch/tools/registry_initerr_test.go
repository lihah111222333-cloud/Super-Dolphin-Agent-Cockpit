package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestRegistryInitErrListReturnsError(t *testing.T) {
	initErr := errors.New("path policy invalid")
	registry := Registry{initErr: initErr}

	tools, err := registry.List()
	if tools != nil {
		t.Fatalf("Registry.List() tools = %#v, want nil when initErr is set", tools)
	}
	if err == nil {
		t.Fatal("Registry.List() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "path policy invalid") {
		t.Fatalf("Registry.List() error = %q, want initErr context", err.Error())
	}
}
