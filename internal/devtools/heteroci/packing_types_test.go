package heteroci

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestPackingPlanSchemaVersionIsFrozen(t *testing.T) {
	if PackingPlanSchemaVersion != "heteroci-packing-plan/v2" {
		t.Fatalf("PackingPlanSchemaVersion = %q, want %q", PackingPlanSchemaVersion, "heteroci-packing-plan/v2")
	}
}

func TestPackingPlanJSONUsesCanonicalSnakeCase(t *testing.T) {
	wave := ExecutionWave{Wave: 7, UnitIDs: []string{"unit-a", "unit-b"}, CPUMilli: 2000, MemoryBytes: 8192, Concurrency: 2, EstimatedDurationMS: 1234}
	plan := PackingPlan{
		SchemaVersion:               PackingPlanSchemaVersion,
		Authority:                   "LOCAL_NON_AUTHORITATIVE",
		SnapshotCapturedAtUnixMilli: 1712345678901,
		NodeShards:                  []NodeShard{{NodeID: "linux-1", Waves: []ExecutionWave{wave}, EstimatedMakespanMS: 2345}},
		UnassignedUnitIDs:           []string{"unit-c"},
	}

	root := mustPackingJSONObject(t, mustMarshalPackingJSON(t, plan))
	assertExactPackingJSONKeys(t, root, []string{"schema_version", "authority", "snapshot_captured_at_unix_milli", "node_shards", "unassigned_unit_ids"})
	assertPackingJSONRaw(t, root, "schema_version", `"heteroci-packing-plan/v2"`)
	assertPackingJSONRaw(t, root, "authority", `"LOCAL_NON_AUTHORITATIVE"`)
	assertPackingJSONRaw(t, root, "snapshot_captured_at_unix_milli", `1712345678901`)
	assertPackingJSONRaw(t, root, "unassigned_unit_ids", `["unit-c"]`)

	shards := mustPackingJSONArray(t, root["node_shards"])
	if len(shards) != 1 {
		t.Fatalf("node_shards length = %d, want 1", len(shards))
	}
	node := mustPackingJSONObject(t, shards[0])
	assertExactPackingJSONKeys(t, node, []string{"node_id", "waves", "estimated_makespan_ms"})
	assertPackingJSONRaw(t, node, "node_id", `"linux-1"`)
	assertPackingJSONRaw(t, node, "estimated_makespan_ms", `2345`)

	waves := mustPackingJSONArray(t, node["waves"])
	if len(waves) != 1 {
		t.Fatalf("waves length = %d, want 1", len(waves))
	}
	encodedWave := mustPackingJSONObject(t, waves[0])
	assertExactPackingJSONKeys(t, encodedWave, []string{"wave", "unit_ids", "cpu_milli", "memory_bytes", "concurrency", "estimated_duration_ms"})
	assertPackingJSONRaw(t, encodedWave, "wave", `7`)
	assertPackingJSONRaw(t, encodedWave, "unit_ids", `["unit-a","unit-b"]`)
	assertPackingJSONRaw(t, encodedWave, "cpu_milli", `2000`)
	assertPackingJSONRaw(t, encodedWave, "memory_bytes", `8192`)
	assertPackingJSONRaw(t, encodedWave, "concurrency", `2`)
	assertPackingJSONRaw(t, encodedWave, "estimated_duration_ms", `1234`)
}

func TestPackingPlanEmptyNonNilSlicesMarshalAsArrays(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		fields []string
	}{
		{name: "plan slices", value: PackingPlan{NodeShards: make([]NodeShard, 0), UnassignedUnitIDs: make([]string, 0)}, fields: []string{"node_shards", "unassigned_unit_ids"}},
		{name: "node waves", value: NodeShard{Waves: make([]ExecutionWave, 0)}, fields: []string{"waves"}},
		{name: "wave unit ids", value: ExecutionWave{UnitIDs: make([]string, 0)}, fields: []string{"unit_ids"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := mustMarshalPackingJSON(t, tt.value)
			if bytes.Contains(encoded, []byte("null")) {
				t.Fatalf("JSON contains null for non-nil empty slice: %s", encoded)
			}
			object := mustPackingJSONObject(t, encoded)
			for _, field := range tt.fields {
				assertPackingJSONRaw(t, object, field, `[]`)
			}
		})
	}
}

func TestPackingPlanSlicesRemainDynamic(t *testing.T) {
	tests := []struct {
		name  string
		typ   reflect.Type
		field string
		elem  reflect.Type
	}{
		{name: "plan node shards", typ: reflect.TypeFor[PackingPlan](), field: "NodeShards", elem: reflect.TypeFor[NodeShard]()},
		{name: "plan unassigned ids", typ: reflect.TypeFor[PackingPlan](), field: "UnassignedUnitIDs", elem: reflect.TypeFor[string]()},
		{name: "node waves", typ: reflect.TypeFor[NodeShard](), field: "Waves", elem: reflect.TypeFor[ExecutionWave]()},
		{name: "wave unit ids", typ: reflect.TypeFor[ExecutionWave](), field: "UnitIDs", elem: reflect.TypeFor[string]()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, ok := tt.typ.FieldByName(tt.field)
			if !ok {
				t.Fatalf("field %s is missing", tt.field)
			}
			if field.Type.Kind() != reflect.Slice {
				t.Fatalf("field %s kind = %s, want slice (not fixed array)", tt.field, field.Type.Kind())
			}
			if field.Type.Elem() != tt.elem {
				t.Fatalf("field %s element type = %s, want %s", tt.field, field.Type.Elem(), tt.elem)
			}
		})
	}
}

func TestPackingPlanFieldTypesAndJSONTagsAreFrozen(t *testing.T) {
	tests := []struct {
		name   string
		typ    reflect.Type
		fields []packingFieldSpec
	}{
		{name: "execution wave", typ: reflect.TypeFor[ExecutionWave](), fields: []packingFieldSpec{
			{name: "Wave", typ: reflect.TypeFor[int](), jsonTag: "wave"},
			{name: "UnitIDs", typ: reflect.TypeFor[[]string](), jsonTag: "unit_ids"},
			{name: "CPUMilli", typ: reflect.TypeFor[int64](), jsonTag: "cpu_milli"},
			{name: "MemoryBytes", typ: reflect.TypeFor[int64](), jsonTag: "memory_bytes"},
			{name: "Concurrency", typ: reflect.TypeFor[int](), jsonTag: "concurrency"},
			{name: "EstimatedDurationMS", typ: reflect.TypeFor[int64](), jsonTag: "estimated_duration_ms"},
		}},
		{name: "node shard", typ: reflect.TypeFor[NodeShard](), fields: []packingFieldSpec{
			{name: "NodeID", typ: reflect.TypeFor[string](), jsonTag: "node_id"},
			{name: "Waves", typ: reflect.TypeFor[[]ExecutionWave](), jsonTag: "waves"},
			{name: "EstimatedMakespanMS", typ: reflect.TypeFor[int64](), jsonTag: "estimated_makespan_ms"},
		}},
		{name: "packing plan", typ: reflect.TypeFor[PackingPlan](), fields: []packingFieldSpec{
			{name: "SchemaVersion", typ: reflect.TypeFor[string](), jsonTag: "schema_version"},
			{name: "Authority", typ: reflect.TypeFor[string](), jsonTag: "authority"},
			{name: "SnapshotCapturedAtUnixMilli", typ: reflect.TypeFor[int64](), jsonTag: "snapshot_captured_at_unix_milli"},
			{name: "NodeShards", typ: reflect.TypeFor[[]NodeShard](), jsonTag: "node_shards"},
			{name: "UnassignedUnitIDs", typ: reflect.TypeFor[[]string](), jsonTag: "unassigned_unit_ids"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assertPackingStructLayout(t, tt.typ, tt.fields) })
	}
}

type packingFieldSpec struct {
	name    string
	typ     reflect.Type
	jsonTag string
}

func assertPackingStructLayout(t *testing.T, typ reflect.Type, want []packingFieldSpec) {
	t.Helper()
	if typ.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want %d", typ.Name(), typ.NumField(), len(want))
	}
	for index, spec := range want {
		field := typ.Field(index)
		if field.Name != spec.name {
			t.Errorf("field %d name = %s, want %s", index, field.Name, spec.name)
		}
		if field.Type != spec.typ {
			t.Errorf("field %s type = %s, want %s", field.Name, field.Type, spec.typ)
		}
		if got := field.Tag.Get("json"); got != spec.jsonTag {
			t.Errorf("field %s json tag = %q, want %q", field.Name, got, spec.jsonTag)
		}
	}
}

func mustMarshalPackingJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	return encoded
}

func mustPackingJSONObject(t *testing.T, encoded []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("json.Unmarshal object: %v; JSON=%s", err, encoded)
	}
	return object
}

func mustPackingJSONArray(t *testing.T, encoded []byte) []json.RawMessage {
	t.Helper()
	var array []json.RawMessage
	if err := json.Unmarshal(encoded, &array); err != nil {
		t.Fatalf("json.Unmarshal array: %v; JSON=%s", err, encoded)
	}
	return array
}

func assertExactPackingJSONKeys(t *testing.T, object map[string]json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

func assertPackingJSONRaw(t *testing.T, object map[string]json.RawMessage, field, want string) {
	t.Helper()
	got, ok := object[field]
	if !ok {
		t.Fatalf("JSON field %q is missing", field)
	}
	if string(got) != want {
		t.Fatalf("JSON field %q = %s, want %s", field, got, want)
	}
}
