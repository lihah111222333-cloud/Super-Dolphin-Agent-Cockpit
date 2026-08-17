package format

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestResultLimitAPIRemovesDeprecatedVerbosity(t *testing.T) {
	assertLimitCall(t, "ResolveResultLimit default", ResolveResultLimit, []int{0, 17}, 17)
	assertLimitCall(t, "ResolveResultLimit requested", ResolveResultLimit, []int{5, 17}, 5)
	assertLimitCall(t, "ReferencesLimit", ReferencesLimit, []int{0}, 30)
	assertLimitCall(t, "CompletionLimit", CompletionLimit, []int{0}, 20)
	assertLimitCall(t, "WorkspaceSymbolLimit", WorkspaceSymbolLimit, []int{0}, 20)
	assertLimitCall(t, "DocumentSymbolLimit", DocumentSymbolLimit, []int{0}, 20)

	source, err := os.ReadFile("compact.go")
	if err != nil {
		t.Fatalf("read compact.go: %v", err)
	}
	for _, forbidden := range []string{"VerbosityCompact", "VerbosityFull", "NormalizeVerbosity", "verbosity string"} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("compact.go still contains deprecated verbosity API %q", forbidden)
		}
	}
}

func assertLimitCall(t *testing.T, name string, fn any, args []int, want int) {
	t.Helper()
	callable := reflect.ValueOf(fn)
	if got := callable.Type().NumIn(); got != len(args) {
		t.Errorf("%s input count = %d, want %d", name, got, len(args))
		return
	}
	inputs := make([]reflect.Value, len(args))
	for i, arg := range args {
		inputs[i] = reflect.ValueOf(arg)
	}
	outputs := callable.Call(inputs)
	if len(outputs) != 1 {
		t.Fatalf("%s output count = %d, want 1", name, len(outputs))
	}
	if got := int(outputs[0].Int()); got != want {
		t.Errorf("%s = %d, want %d", name, got, want)
	}
}
