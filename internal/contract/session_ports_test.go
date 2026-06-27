package contract

import (
	"reflect"
	"testing"
)

func TestSessionStartRequestDoesNotExposeThreadInternalFields(t *testing.T) {
	typ := reflect.TypeOf(SessionStartRequest{})
	for _, name := range []string{
		"PromptAssemblyRef",
		"PromptVersionID",
		"AgentTitle",
		"PromptKeyStale",
	} {
		if _, ok := typ.FieldByName(name); ok {
			t.Fatalf("SessionStartRequest must not expose thread-internal field %s", name)
		}
	}
}
