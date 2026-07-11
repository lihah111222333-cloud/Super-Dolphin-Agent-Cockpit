package storeguard

import (
	"reflect"
	"testing"
	"unsafe"
)

func TestIsNilCoversInvalidAndNilCapableKinds(t *testing.T) {
	if !IsNil[any](nil) {
		t.Fatal("expected nil interface to produce an invalid nil Value")
	}

	channel := make(chan struct{})
	t.Cleanup(func() { close(channel) })
	number := 41
	var nilInterface any
	var nonnilInterface any = &number
	tests := map[string]struct {
		nilValue    reflect.Value
		nonnilValue reflect.Value
	}{
		"channel":        {reflect.ValueOf((chan struct{})(nil)), reflect.ValueOf(channel)},
		"function":       {reflect.ValueOf((func())(nil)), reflect.ValueOf(func() {})},
		"interface":      {reflect.ValueOf(&nilInterface).Elem(), reflect.ValueOf(&nonnilInterface).Elem()},
		"map":            {reflect.ValueOf((map[string]string)(nil)), reflect.ValueOf(map[string]string{})},
		"pointer":        {reflect.ValueOf((*int)(nil)), reflect.ValueOf(&number)},
		"slice":          {reflect.ValueOf(([]byte)(nil)), reflect.ValueOf([]byte{})},
		"unsafe_pointer": {reflect.ValueOf(unsafe.Pointer(nil)), reflect.ValueOf(unsafe.Pointer(&number))},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			if !isNilValue(values.nilValue) {
				t.Fatalf("expected nil-capable %s to be detected", name)
			}
			if isNilValue(values.nonnilValue) {
				t.Fatalf("expected nonnil %s to remain available", name)
			}
		})
	}
}

func TestIsNilRejectsScalarValues(t *testing.T) {
	for name, value := range map[string]any{
		"bool":   true,
		"int":    41,
		"string": "value",
		"struct": struct{}{},
	} {
		t.Run(name, func(t *testing.T) {
			if IsNil(value) {
				t.Fatalf("expected scalar %s to be nonnil", name)
			}
		})
	}
}
