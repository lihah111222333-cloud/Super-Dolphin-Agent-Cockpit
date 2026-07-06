package nodeexec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestOpKindConstants_FourKinds: 蓝图 v2 §9 ops 4 个动词。
func TestOpKindConstants_FourKinds(t *testing.T) {
	t.Parallel()
	all := []OpKind{
		OpKindUpdateDAG,
		OpKindAddNode,
		OpKindUpdateNode,
		OpKindRemoveNode,
	}
	if got, want := len(all), 4; got != want {
		t.Fatalf("OpKind count = %d, want %d", got, want)
	}
	seen := make(map[OpKind]bool, len(all))
	for _, k := range all {
		if k == "" {
			t.Errorf("empty OpKind constant detected")
		}
		if seen[k] {
			t.Errorf("duplicate OpKind value: %q", k)
		}
		seen[k] = true
	}
}

// TestOpsImplementOp 编译时检查 4 个 typed struct 都满足 Op 接口。
func TestOpsImplementOp(t *testing.T) {
	t.Parallel()
	var _ Op = OpUpdateDAG{}
	var _ Op = OpAddNode{}
	var _ Op = OpUpdateNode{}
	var _ Op = OpRemoveNode{}
}

func TestOps_KindReturns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		op   Op
		want OpKind
	}{
		{OpUpdateDAG{}, OpKindUpdateDAG},
		{OpAddNode{}, OpKindAddNode},
		{OpUpdateNode{}, OpKindUpdateNode},
		{OpRemoveNode{}, OpKindRemoveNode},
	}
	for _, tc := range cases {
		if got := tc.op.Kind(); got != tc.want {
			t.Errorf("Kind = %q, want %q", got, tc.want)
		}
	}
}

// TestOps_MixedRoundTrip: 混合 4 种 op 的 marshal/unmarshal 双向往返。
func TestOps_MixedRoundTrip(t *testing.T) {
	t.Parallel()
	title := "新标题"
	cron := "0 8 * * *"
	deps := []string{"a", "b"}
	original := Ops{
		OpUpdateDAG{Patch: DAGPatch{Title: &title, CronExpr: &cron}},
		OpAddNode{Node: NodeSpec{
			NodeKey:   "n1",
			Title:     "节点 1",
			NodeType:  "agent",
			DependsOn: []string{"upstream"},
			Config:    json.RawMessage(`{"exec":{"model":"opus"}}`),
		}},
		OpUpdateNode{NodeKey: "n1", Patch: NodePatch{DependsOn: &deps}},
		OpRemoveNode{NodeKey: "old"},
	}

	marshaled := marshalOpsForTest(t, original)
	assertOpDiscriminators(t, marshaled)
	roundtrip := unmarshalOpsForTest(t, marshaled)
	assertRoundtripKinds(t, roundtrip, original)
	assertRoundtripPayloads(t, roundtrip, title)
}

func TestOpsReadsWritesRoundTrip(t *testing.T) {
	raw := []byte(`[
		{"op":"add_node","node":{"node_key":"materialize","title":"Materialize","node_type":"agent","reads":["shared://inputs/source.md"],"writes":["shared://outputs/report.md"]}},
		{"op":"update_node","node_key":"materialize","patch":{"reads":["shared://inputs/new-source.md"],"writes":[]}}
	]`)

	roundtrip := unmarshalOpsForTest(t, raw)
	add, ok := roundtrip[0].(OpAddNode)
	if !ok {
		t.Fatalf("ops[0] = %T, want OpAddNode", roundtrip[0])
	}
	assertNodeexecStringSliceField(t, add.Node, "Reads", []string{"shared://inputs/source.md"})
	assertNodeexecStringSliceField(t, add.Node, "Writes", []string{"shared://outputs/report.md"})
	update, ok := roundtrip[1].(OpUpdateNode)
	if !ok {
		t.Fatalf("ops[1] = %T, want OpUpdateNode", roundtrip[1])
	}
	assertNodeexecStringSlicePointerField(t, update.Patch, "Reads", []string{"shared://inputs/new-source.md"})
	assertNodeexecStringSlicePointerField(t, update.Patch, "Writes", []string{})
}

func marshalOpsForTest(t *testing.T, original Ops) []byte {
	t.Helper()
	marshaled, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return marshaled
}

func assertOpDiscriminators(t *testing.T, marshaled []byte) {
	t.Helper()
	var asArray []map[string]json.RawMessage
	if err := json.Unmarshal(marshaled, &asArray); err != nil {
		t.Fatalf("re-decode array: %v", err)
	}
	if got, want := len(asArray), 4; got != want {
		t.Fatalf("marshaled array len = %d, want %d", got, want)
	}
	for i, kind := range []OpKind{OpKindUpdateDAG, OpKindAddNode, OpKindUpdateNode, OpKindRemoveNode} {
		assertOpDiscriminator(t, asArray, i, kind)
	}
}

func assertOpDiscriminator(t *testing.T, asArray []map[string]json.RawMessage, i int, want OpKind) {
	t.Helper()
	var got OpKind
	if err := json.Unmarshal(asArray[i]["op"], &got); err != nil {
		t.Fatalf("ops[%d] op field: %v", i, err)
	}
	if got != want {
		t.Errorf("ops[%d] op = %q, want %q", i, got, want)
	}
}

func unmarshalOpsForTest(t *testing.T, marshaled []byte) Ops {
	t.Helper()
	var roundtrip Ops
	if err := json.Unmarshal(marshaled, &roundtrip); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return roundtrip
}

func assertRoundtripKinds(t *testing.T, roundtrip, original Ops) {
	t.Helper()
	if got, want := len(roundtrip), len(original); got != want {
		t.Fatalf("len = %d, want %d", got, want)
	}
	for i := range original {
		if roundtrip[i].Kind() != original[i].Kind() {
			t.Errorf("ops[%d] kind = %q, want %q", i, roundtrip[i].Kind(), original[i].Kind())
		}
	}
}

func assertRoundtripPayloads(t *testing.T, roundtrip Ops, title string) {
	t.Helper()
	assertRoundtripUpdateDAG(t, roundtrip[0], title)
	assertRoundtripAddNode(t, roundtrip[1])
	assertRoundtripUpdateNode(t, roundtrip[2])
	assertRoundtripRemoveNode(t, roundtrip[3])
}

func assertRoundtripUpdateDAG(t *testing.T, op Op, title string) {
	t.Helper()
	got, ok := op.(OpUpdateDAG)
	if !ok || got.Patch.Title == nil || *got.Patch.Title != title {
		t.Errorf("ops[0] roundtrip lost data: %+v", op)
	}
}

func assertRoundtripAddNode(t *testing.T, op Op) {
	t.Helper()
	got, ok := op.(OpAddNode)
	if !ok || got.Node.NodeKey != "n1" || got.Node.NodeType != "agent" {
		t.Errorf("ops[1] roundtrip lost data: %+v", op)
	}
}

func assertRoundtripUpdateNode(t *testing.T, op Op) {
	t.Helper()
	got, ok := op.(OpUpdateNode)
	if !ok || got.NodeKey != "n1" || got.Patch.DependsOn == nil || len(*got.Patch.DependsOn) != 2 {
		t.Errorf("ops[2] roundtrip lost data: %+v", op)
	}
}

func assertRoundtripRemoveNode(t *testing.T, op Op) {
	t.Helper()
	got, ok := op.(OpRemoveNode)
	if !ok || got.NodeKey != "old" {
		t.Errorf("ops[3] roundtrip lost data: %+v", op)
	}
}

// TestOps_UnmarshalRejectsUnknownKind: 未知 op 类型必须明确报错。
func TestOps_UnmarshalRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	data := []byte(`[{"op":"bogus"}]`)
	var ops Ops
	if err := json.Unmarshal(data, &ops); err == nil {
		t.Fatalf("expected error for unknown op kind, got nil")
	}
}

// TestOps_UnmarshalRejectsMissingDiscriminator: 缺 op 字段必须报错。
func TestOps_UnmarshalRejectsMissingDiscriminator(t *testing.T) {
	t.Parallel()
	data := []byte(`[{"patch":{"title":"x"}}]`)
	var ops Ops
	if err := json.Unmarshal(data, &ops); err == nil {
		t.Fatalf("expected error for missing op discriminator, got nil")
	}
}

func TestOps_UnmarshalAddNodeCarriesAssignedTo(t *testing.T) {
	t.Parallel()
	data := []byte(`[{"op":"add_node","node":{"node_key":"n1","title":"N1","node_type":"agent","assigned_to":"agent-1"}}]`)
	var ops Ops
	if err := json.Unmarshal(data, &ops); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	add, ok := ops[0].(OpAddNode)
	if !ok {
		t.Fatalf("ops[0] = %T, want OpAddNode", ops[0])
	}
	field := reflect.ValueOf(add.Node).FieldByName("AssignedTo")
	if !field.IsValid() {
		t.Fatalf("NodeSpec missing AssignedTo field; add_node assigned_to cannot persist atomically")
	}
	if got := field.String(); got != "agent-1" {
		t.Fatalf("AssignedTo = %q, want agent-1", got)
	}
}

func TestOps_UnmarshalAddNodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	cases := []string{
		`[{"op":"add_node","assigned_to":"agent-1","node":{"node_key":"n1","title":"N1","node_type":"agent"}}]`,
		`[{"op":"add_node","node":{"node_key":"n1","title":"N1","node_type":"agent","assignedTo":"agent-1"}}]`,
	}
	for _, raw := range cases {
		var ops Ops
		err := json.Unmarshal([]byte(raw), &ops)
		if err == nil {
			t.Fatalf("unmarshal %s: error = nil, want unknown-field rejection", raw)
		}
		if !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("unmarshal %s: err = %v, want unknown field", raw, err)
		}
	}
}

// TestOpsRequest_RoundTrip: 完整 OpsRequest / OpsResponse 字段。
func TestOpsRequest_RoundTrip(t *testing.T) {
	t.Parallel()
	req := OpsRequest{
		DagKey:      "dag_xxx",
		BaseVersion: 7,
		Ops:         Ops{OpRemoveNode{NodeKey: "n1"}},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got OpsRequest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DagKey != "dag_xxx" || got.BaseVersion != 7 || len(got.Ops) != 1 {
		t.Fatalf("OpsRequest roundtrip lost data: %+v", got)
	}

	resp := OpsResponse{NewVersion: 8}
	data, err = json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	var gotResp OpsResponse
	if err := json.Unmarshal(data, &gotResp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if gotResp.NewVersion != 8 {
		t.Fatalf("OpsResponse roundtrip: NewVersion = %d, want 8", gotResp.NewVersion)
	}
}

// TestNodePatch_DependsOn_ThreeStates: nil 不改 / *[] 清空 / *[a,b] 设置。
func TestNodePatch_DependsOn_ThreeStates(t *testing.T) {
	t.Parallel()
	// nil = unspecified
	noChange := NodePatch{}
	data, _ := json.Marshal(noChange)
	if string(data) != `{}` {
		t.Errorf("unspecified DependsOn should marshal to {}, got %s", data)
	}

	// *[] = clear
	empty := []string{}
	clear := NodePatch{DependsOn: &empty}
	data, _ = json.Marshal(clear)
	if string(data) != `{"depends_on":[]}` {
		t.Errorf("clear DependsOn should marshal to {\"depends_on\":[]}, got %s", data)
	}

	// *[a] = set
	set := NodePatch{DependsOn: &[]string{"a"}}
	data, _ = json.Marshal(set)
	if string(data) != `{"depends_on":["a"]}` {
		t.Errorf("set DependsOn marshal got %s", data)
	}
}

func assertNodeexecStringSliceField(t *testing.T, target any, field string, want []string) {
	t.Helper()
	fieldValue := reflect.ValueOf(target).FieldByName(field)
	if !fieldValue.IsValid() {
		t.Fatalf("%T missing %s field", target, field)
	}
	if fieldValue.Kind() != reflect.Slice || fieldValue.Type().Elem().Kind() != reflect.String {
		t.Fatalf("%T.%s type = %v, want []string", target, field, fieldValue.Type())
	}
	got := fieldValue.Interface().([]string)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%T.%s = %#v, want %#v", target, field, got, want)
	}
}

func assertNodeexecStringSlicePointerField(t *testing.T, target any, field string, want []string) {
	t.Helper()
	fieldValue := reflect.ValueOf(target).FieldByName(field)
	if !fieldValue.IsValid() {
		t.Fatalf("%T missing %s field", target, field)
	}
	if fieldValue.Kind() != reflect.Pointer || fieldValue.Type().Elem().Kind() != reflect.Slice || fieldValue.Type().Elem().Elem().Kind() != reflect.String {
		t.Fatalf("%T.%s type = %v, want *[]string", target, field, fieldValue.Type())
	}
	if fieldValue.IsNil() {
		t.Fatalf("%T.%s = nil, want %#v", target, field, want)
	}
	got := fieldValue.Elem().Interface().([]string)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%T.%s = %#v, want %#v", target, field, got, want)
	}
}
