package localci

import (
	"reflect"
	"testing"
)

func assertSchedulerStructFields(t *testing.T, structType reflect.Type, want []string) {
	t.Helper()
	got := make([]string, structType.NumField())
	for index := range structType.NumField() {
		got[index] = structType.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields=%v want=%v", structType.Name(), got, want)
	}
}
