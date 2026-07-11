package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	orchtools "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
)

func TestRegistryToolProviderListToolsPropagatesRegistryInitErr(t *testing.T) {
	initErr := errors.New("registry init failed")
	provider := registryToolProvider{registry: registryWithInitErrForTest(t, initErr)}

	tools, err := provider.ListTools(context.Background())
	if err == nil {
		t.Fatalf("ListTools() error = nil, tools = %#v; want registry init error", tools)
	}
	if !strings.Contains(err.Error(), "registry init failed") {
		t.Fatalf("ListTools() error = %q, want registry init context", err.Error())
	}
}

func registryWithInitErrForTest(t *testing.T, err error) orchtools.Registry {
	t.Helper()
	registry := orchtools.Registry{}
	field := reflect.ValueOf(&registry).Elem().FieldByName("initErr")
	if !field.IsValid() {
		t.Fatal("tools.Registry missing initErr field")
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(err))
	return registry
}
