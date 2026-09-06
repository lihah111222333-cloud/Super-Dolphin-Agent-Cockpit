package accesswait

import (
	"reflect"
	"strings"
	"testing"
)

// TestAccessWaitFieldGuard keeps the durable access/wait wire contracts closed:
// exported fields must be enumerable, and request/handle/result records must
// not grow a provider payload or an execution-bearing field by accident.
func TestAccessWaitFieldGuard(t *testing.T) {
	assertAccessWaitFieldContract(t)
}

func testAccessWaitFieldGuard(t *testing.T) {
	t.Helper()
	assertAccessWaitFieldContract(t)
}

func assertAccessWaitFieldContract(t *testing.T) {
	types := accessWaitDurableTypes()
	for _, typ := range types {
		t.Run("json/"+typ.Name(), func(t *testing.T) {
			assertAccessWaitJSONTags(t, typ)
			assertAccessWaitNoPayloadFields(t, typ)
		})
	}
	assertAccessWaitSelectorFields(t)
	assertAccessWaitMatchedFields(t)
}

func accessWaitDurableTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[FenceSnapshot](),
		reflect.TypeFor[AccessGrantRequest](),
		reflect.TypeFor[AccessGrant](),
		reflect.TypeFor[RevokeGrantRequest](),
		reflect.TypeFor[ExpireGrantRequest](),
		reflect.TypeFor[AccessRequest](),
		reflect.TypeFor[AccessAuthorization](),
		reflect.TypeFor[EventSelector](),
		reflect.TypeFor[WaitRequest](),
		reflect.TypeFor[WaitHandle](),
		reflect.TypeFor[PollRequest](),
		reflect.TypeFor[CancelRequest](),
		reflect.TypeFor[WaitResult](),
	}
}

func assertAccessWaitJSONTags(t *testing.T, typ reflect.Type) {
	t.Helper()
	seen := make(map[string]string, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("%s.%s has no enumerable JSON tag", typ.Name(), field.Name)
		}
		if previous, duplicate := seen[name]; duplicate {
			t.Fatalf("%s JSON field %q is used by both %s and %s", typ.Name(), name, previous, field.Name)
		}
		seen[name] = field.Name
	}
}

func assertAccessWaitNoPayloadFields(t *testing.T, typ reflect.Type) {
	t.Helper()
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" {
			continue
		}
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		combined := strings.ToLower(field.Name + " " + jsonName)
		for _, forbidden := range []string{"body", "plaintext", "content", "command", "provider_secret", "providersecret", "secret"} {
			if strings.Contains(combined, forbidden) {
				t.Fatalf("%s.%s contains forbidden durable payload field %q", typ.Name(), field.Name, forbidden)
			}
		}
	}
}

func assertAccessWaitSelectorFields(t *testing.T) {
	t.Helper()
	typ := reflect.TypeFor[EventSelector]()
	for _, name := range []string{"Version", "SourceID", "SourceEpoch", "Lane", "TargetAgentID", "EventTypes"} {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("EventSelector is missing required selector field %s", name)
		}
	}
	for _, name := range []string{"Selector"} {
		if _, ok := reflect.TypeFor[WaitRequest]().FieldByName(name); !ok {
			t.Fatalf("WaitRequest is missing required selector field %s", name)
		}
		if _, ok := reflect.TypeFor[WaitHandle]().FieldByName(name); !ok {
			t.Fatalf("WaitHandle is missing required selector field %s", name)
		}
	}
}

func assertAccessWaitMatchedFields(t *testing.T) {
	t.Helper()
	typ := reflect.TypeFor[WaitHandle]()
	for _, name := range []string{"MatchedEventID", "MatchedSourceSequence", "MatchedCanonicalCursor"} {
		if _, ok := typ.FieldByName(name); !ok {
			t.Fatalf("WaitHandle is missing required matched field %s", name)
		}
	}
}
