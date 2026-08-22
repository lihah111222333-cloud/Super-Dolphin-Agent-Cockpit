package heteroci

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestRoutingTypesFreezeWireEnums(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "schema", got: RoutingDecisionSchemaVersion, want: "heteroci-routing-decision/v4"},
		{name: "local executor", got: string(ExecutorKindLocalNode), want: "local_node"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("wire value = %q, want %q", test.got, test.want)
			}
		})
	}
}

func TestCapacityBlockerErrorIsStableAndSafe(t *testing.T) {
	if got := (CapacityBlockerError{}).Error(); got != "capacity blocker: no blocked units" {
		t.Fatalf("nil blocker slice error = %q, want stable empty error", got)
	}
	if got := (CapacityBlockerError{Blockers: make([]CapacityBlocker, 0)}).Error(); got != "capacity blocker: no blocked units" {
		t.Fatalf("empty blocker slice error = %q, want stable empty error", got)
	}
	blockers := []CapacityBlocker{
		{UnitID: "unit-z", TargetPlatform: PlatformWindowsAMD64, RequiredRuntime: RuntimeWindowsNative, Reason: CapacityBlockerReasonCapacityUnavailable},
		{UnitID: "unit-a", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, Reason: CapacityBlockerReasonCapacityUnavailable},
		{UnitID: "unit-m", TargetPlatform: PlatformDarwinAMD64, RequiredRuntime: RuntimeDarwinNative, Reason: CapacityBlockerReasonCapacityUnavailable},
	}
	reversed := append([]CapacityBlocker(nil), blockers...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	got := (CapacityBlockerError{Blockers: blockers}).Error()
	if reversedGot := (CapacityBlockerError{Blockers: reversed}).Error(); reversedGot != got {
		t.Fatalf("blocker error depends on input order: forward=%q reverse=%q", got, reversedGot)
	}
	last := -1
	for _, unitID := range []string{`unit-a`, `unit-m`, `unit-z`} {
		position := strings.Index(got, `unit_id="`+unitID+`"`)
		if position < 0 || position <= last {
			t.Fatalf("blocker order is not canonical for %q in %q", unitID, got)
		}
		last = position
	}
	for _, forbidden := range []string{"endpoint", "secret-token", "https://"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("blocker error leaks %q: %q", forbidden, got)
		}
	}
}

type routingWireTypeSpec struct {
	name        string
	typ         reflect.Type
	fieldTypes  map[string]reflect.Type
	fieldOrder  []string
	sliceFields map[string]reflect.Type
}

func TestRoutingWireTypesRetainAllJSONTagsAndFieldTypes(t *testing.T) {
	tests := []routingWireTypeSpec{
		{
			name: "route assignment",
			typ:  reflect.TypeFor[RouteAssignment](),
			fieldTypes: map[string]reflect.Type{
				"UnitID":        reflect.TypeFor[string](),
				"Executor":      reflect.TypeFor[ExecutorKind](),
				"NodeID":        reflect.TypeFor[string](),
				"Endpoint":      reflect.TypeFor[string](),
				"ProbeIdentity": reflect.TypeFor[string](),
				"ProbeBootID":   reflect.TypeFor[string](),
			},
			fieldOrder: []string{"UnitID", "Executor", "NodeID", "Endpoint", "ProbeIdentity", "ProbeBootID"},
		},
		{
			name: "routing decision",
			typ:  reflect.TypeFor[RoutingDecision](),
			fieldTypes: map[string]reflect.Type{
				"SchemaVersion":               reflect.TypeFor[string](),
				"Authority":                   reflect.TypeFor[string](),
				"SnapshotCapturedAtUnixMilli": reflect.TypeFor[int64](),
				"PackingPlanDigest":           reflect.TypeFor[string](),
				"Assignments":                 reflect.TypeFor[[]RouteAssignment](),
				"DecisionDigest":              reflect.TypeFor[string](),
			},
			fieldOrder:  []string{"SchemaVersion", "Authority", "SnapshotCapturedAtUnixMilli", "PackingPlanDigest", "Assignments", "DecisionDigest"},
			sliceFields: map[string]reflect.Type{"Assignments": reflect.TypeFor[RouteAssignment]()},
		},
		{
			name: "capacity blocker",
			typ:  reflect.TypeFor[CapacityBlocker](),
			fieldTypes: map[string]reflect.Type{
				"UnitID":          reflect.TypeFor[string](),
				"TargetPlatform":  reflect.TypeFor[Platform](),
				"RequiredRuntime": reflect.TypeFor[RuntimeKind](),
				"Reason":          reflect.TypeFor[string](),
			},
		},
		{
			name: "capacity blocker error",
			typ:  reflect.TypeFor[CapacityBlockerError](),
			fieldTypes: map[string]reflect.Type{
				"Blockers": reflect.TypeFor[[]CapacityBlocker](),
			},
			sliceFields: map[string]reflect.Type{"Blockers": reflect.TypeFor[CapacityBlocker]()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertRoutingWireTypeSpec(t, test)
		})
	}
}

func assertRoutingWireTypeSpec(t *testing.T, test routingWireTypeSpec) {
	t.Helper()
	if test.typ.NumField() != len(test.fieldTypes) {
		t.Fatalf("field count = %d, want %d", test.typ.NumField(), len(test.fieldTypes))
	}
	fields := make([]reflect.StructField, 0, test.typ.NumField())
	for field := range test.typ.Fields() {
		fields = append(fields, field)
	}
	assertRoutingFieldOrder(t, fields, test.fieldOrder)
	for _, field := range fields {
		wantType, ok := test.fieldTypes[field.Name]
		if !ok {
			t.Fatalf("unexpected field %q", field.Name)
		}
		if field.Type != wantType {
			t.Fatalf("field %s type = %s, want %s", field.Name, field.Type, wantType)
		}
		assertRoutingJSONField(t, field)
		if wantElement, isSlice := test.sliceFields[field.Name]; isSlice {
			assertRoutingSliceField(t, field, wantElement)
		}
	}
}

func assertRoutingFieldOrder(t *testing.T, fields []reflect.StructField, want []string) {
	t.Helper()
	if len(want) == 0 {
		return
	}
	if len(fields) != len(want) {
		t.Fatalf("field order length = %d, want %d", len(fields), len(want))
	}
	for index, wantName := range want {
		if fields[index].Name != wantName {
			t.Fatalf("field %d = %q, want %q", index, fields[index].Name, wantName)
		}
	}
}

func assertRoutingJSONField(t *testing.T, field reflect.StructField) {
	t.Helper()
	tag, ok := field.Tag.Lookup("json")
	if !ok || tag != routingJSONFieldName(field.Name) {
		t.Fatalf("field %s json tag = %q, want %q", field.Name, tag, routingJSONFieldName(field.Name))
	}
	if strings.Contains(tag, ",") {
		t.Fatalf("field %s json tag has options: %q", field.Name, tag)
	}
}

func assertRoutingSliceField(t *testing.T, field reflect.StructField, wantElement reflect.Type) {
	t.Helper()
	if field.Type.Kind() != reflect.Slice || field.Type.Elem() != wantElement {
		t.Fatalf("field %s slice type = %s, want []%s", field.Name, field.Type, wantElement)
	}
}

func TestRoutingDecisionEmptyAssignmentsEncodeAsArray(t *testing.T) {
	decision := RoutingDecision{
		SchemaVersion: RoutingDecisionSchemaVersion,
		Authority:     AuthorityLocalNonAuthoritative,
		Assignments:   make([]RouteAssignment, 0),
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("marshal empty assignments: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode marshaled routing decision: %v", err)
	}
	assignments, ok := fields["assignments"]
	if !ok {
		t.Fatal("marshaled routing decision omitted assignments")
	}
	if got := strings.TrimSpace(string(assignments)); got != "[]" {
		t.Fatalf("empty non-nil assignments encoded as %s, want []", got)
	}
	var roundTrip RoutingDecision
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal routing decision: %v", err)
	}
	if roundTrip.Assignments == nil || len(roundTrip.Assignments) != 0 {
		t.Fatalf("round-trip assignments = %#v, want non-nil empty slice", roundTrip.Assignments)
	}
}

func TestRouteAssignmentZeroValuesEncodeEveryField(t *testing.T) {
	encoded, err := json.Marshal(RouteAssignment{})
	if err != nil {
		t.Fatalf("marshal zero route assignment: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode zero route assignment: %v", err)
	}
	for field := range reflect.TypeFor[RouteAssignment]().Fields() {
		tag, ok := field.Tag.Lookup("json")
		if !ok {
			t.Fatalf("field %s has no json tag", field.Name)
		}
		raw, ok := fields[tag]
		if !ok {
			t.Fatalf("zero route assignment omitted %s", tag)
		}
		if got := strings.TrimSpace(string(raw)); got != `""` {
			t.Fatalf("zero route assignment field %s encoded as %s, want empty string", tag, got)
		}
	}
}

func routingJSONFieldName(name string) string {
	runes := []rune(name)
	var result strings.Builder
	for index, current := range runes {
		if unicode.IsUpper(current) && index > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || (unicode.IsUpper(previous) && nextIsLower) {
				result.WriteByte('_')
			}
		}
		result.WriteRune(unicode.ToLower(current))
	}
	return result.String()
}
