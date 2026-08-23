package heteroci

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestRoutingLeaseWireLiteralsAreFrozen(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "schema", got: RoutingLeaseSchemaVersion, want: "heteroci-routing-lease/v4"},
		{name: "planned", got: string(LeaseStatePlanned), want: "planned"},
		{name: "started", got: string(LeaseStateStarted), want: "started"},
		{name: "succeeded", got: string(LeaseStateSucceeded), want: "succeeded"},
		{name: "failed", got: string(LeaseStateFailed), want: "failed"},
		{name: "cancelled", got: string(LeaseStateCancelled), want: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("wire literal = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestRoutingLeaseFieldLayoutAndJSONTagsAreFrozen(t *testing.T) {
	type fieldSpec struct {
		name string
		typ  reflect.Type
		json string
	}
	want := []fieldSpec{
		{name: "SchemaVersion", typ: reflect.TypeFor[string](), json: "schema_version"},
		{name: "Authority", typ: reflect.TypeFor[string](), json: "authority"},
		{name: "RunID", typ: reflect.TypeFor[string](), json: "run_id"},
		{name: "DecisionDigest", typ: reflect.TypeFor[string](), json: "decision_digest"},
		{name: "UnitID", typ: reflect.TypeFor[string](), json: "unit_id"},
		{name: "Executor", typ: reflect.TypeFor[ExecutorKind](), json: "executor"},
		{name: "NodeID", typ: reflect.TypeFor[string](), json: "node_id"},
		{name: "Endpoint", typ: reflect.TypeFor[string](), json: "endpoint"},
		{name: "ProbeIdentity", typ: reflect.TypeFor[string](), json: "probe_identity"},
		{name: "ProbeBootID", typ: reflect.TypeFor[string](), json: "probe_boot_id"},
		{name: "State", typ: reflect.TypeFor[RoutingLeaseState](), json: "state"},
		{name: "ReservedAtUnixMilli", typ: reflect.TypeFor[int64](), json: "reserved_at_unix_milli"},
		{name: "StartedAtUnixMilli", typ: reflect.TypeFor[int64](), json: "started_at_unix_milli"},
		{name: "FinishedAtUnixMilli", typ: reflect.TypeFor[int64](), json: "finished_at_unix_milli"},
	}
	typ := reflect.TypeFor[RoutingLease]()
	if typ.NumField() != len(want) {
		t.Fatalf("RoutingLease field count = %d, want %d", typ.NumField(), len(want))
	}
	index := 0
	for field := range typ.Fields() {
		spec := want[index]
		if field.Name != spec.name {
			t.Errorf("field %d name = %s, want %s", index, field.Name, spec.name)
		}
		if field.Type != spec.typ {
			t.Errorf("field %s type = %s, want %s", field.Name, field.Type, spec.typ)
		}
		if tag, ok := field.Tag.Lookup("json"); !ok || tag != spec.json {
			t.Errorf("field %s json tag = %q, want %q", field.Name, tag, spec.json)
		}
		if strings.Contains(field.Tag.Get("json"), ",") {
			t.Errorf("field %s json tag has options: %q", field.Name, field.Tag.Get("json"))
		}
		index++
	}
}

func TestValidRoutingRunIDRejectsEmptyTrimmedAndControlValues(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "spaces", id: "   "},
		{name: "leading space", id: " run-1"},
		{name: "trailing tab", id: "run-1\t"},
		{name: "newline", id: "run\n1"},
		{name: "nul", id: "run\x001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if validRoutingRunID(test.id) {
				t.Fatalf("validRoutingRunID(%q) = true, want false", test.id)
			}
		})
	}
}

func TestValidRoutingRunIDAcceptsTrimmedNonControlValue(t *testing.T) {
	for _, id := range []string{"run-1", "run_2026-08-19", "run/segment"} {
		t.Run(id, func(t *testing.T) {
			if !validRoutingRunID(id) {
				t.Fatalf("validRoutingRunID(%q) = false, want true", id)
			}
		})
	}
}

func TestRoutingLeasePlannedZeroTimestampsRemainExplicitInJSON(t *testing.T) {
	lease := RoutingLease{
		SchemaVersion:  RoutingLeaseSchemaVersion,
		Authority:      AuthorityLocalNonAuthoritative,
		RunID:          "run-1",
		DecisionDigest: "sha256:" + strings.Repeat("0", 64),
		UnitID:         "unit-1",
		Executor:       ExecutorKindLocalNode,
		State:          LeaseStatePlanned,
	}
	encoded, err := json.Marshal(lease)
	if err != nil {
		t.Fatalf("json.Marshal(RoutingLease): %v", err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("json.Unmarshal(RoutingLease): %v; JSON=%s", err, encoded)
	}
	wantKeys := []string{
		"schema_version", "authority", "run_id", "decision_digest", "unit_id", "executor",
		"node_id", "endpoint", "probe_identity", "probe_boot_id", "state",
		"reserved_at_unix_milli", "started_at_unix_milli", "finished_at_unix_milli",
	}
	gotKeys := make([]string, 0, len(object))
	for key := range object {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	sort.Strings(wantKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("RoutingLease JSON keys = %v, want %v; JSON=%s", gotKeys, wantKeys, encoded)
	}
	for _, key := range []string{"reserved_at_unix_milli", "started_at_unix_milli", "finished_at_unix_milli"} {
		if got := string(object[key]); got != "0" {
			t.Fatalf("RoutingLease JSON field %q = %s, want explicit 0", key, got)
		}
	}
	if got := string(object["state"]); got != `"planned"` {
		t.Fatalf("RoutingLease JSON state = %s, want %q", got, `"planned"`)
	}
}
