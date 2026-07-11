package app

import (
	"reflect"
	"testing"
	"unsafe"
)

// TestIsNilBusinessStore 覆盖 invalid Value 与全部 nil-capable reflect Kind 的 nil/nonnil 分支。
func TestIsNilBusinessStore(t *testing.T) {
	if !isNilBusinessStore[any](nil) {
		t.Fatal("expected nil interface to produce an invalid nil Value")
	}

	channel := make(chan struct{})
	t.Cleanup(func() { close(channel) })
	number := 41
	var nilInterface any
	var nonnilInterface any = &turnDedupeStoreStub{}
	tests := map[string]struct {
		nilValue    reflect.Value
		nonnilValue reflect.Value
	}{
		"channel":        {reflect.ValueOf((chan struct{})(nil)), reflect.ValueOf(channel)},
		"function":       {reflect.ValueOf((func())(nil)), reflect.ValueOf(func() {})},
		"interface":      {reflect.ValueOf(&nilInterface).Elem(), reflect.ValueOf(&nonnilInterface).Elem()},
		"map":            {reflect.ValueOf((map[string]string)(nil)), reflect.ValueOf(map[string]string{})},
		"pointer":        {reflect.ValueOf((*turnDedupeStoreStub)(nil)), reflect.ValueOf(&turnDedupeStoreStub{})},
		"slice":          {reflect.ValueOf(([]byte)(nil)), reflect.ValueOf([]byte{})},
		"unsafe_pointer": {reflect.ValueOf(unsafe.Pointer(nil)), reflect.ValueOf(unsafe.Pointer(&number))},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			if !isNilBusinessStoreValue(values.nilValue) {
				t.Fatalf("expected nil-capable %s to be detected", name)
			}
			if isNilBusinessStoreValue(values.nonnilValue) {
				t.Fatalf("expected nonnil %s to remain available", name)
			}
		})
	}
}
