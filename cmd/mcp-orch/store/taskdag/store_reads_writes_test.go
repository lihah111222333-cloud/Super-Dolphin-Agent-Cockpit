package taskdag

import (
	"context"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func TestSQLiteTaskDAGNodeReadsWritesRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTaskDAGSQLiteDB(t)
	store := NewStore(db).(*store)
	reads := []string{"shared://inputs/source.md", "dag:upstream/result"}
	writes := []string{"shared://outputs/report.md", "dag:final/result"}

	if _, err := store.UpsertDAG(ctx, DAG{DagKey: "dag-rw", Title: "RW DAG", Status: "draft", CreatedBy: "tester", Metadata: []byte(`{}`)}); err != nil {
		t.Fatalf("UpsertDAG() error = %v", err)
	}
	node := Node{DagKey: "dag-rw", NodeKey: "worker", Title: "Worker", NodeType: "agent", DependsOn: []byte(`[]`), Config: []byte(`{"node":"worker"}`)}
	setStringSliceField(t, &node, "Reads", reads)
	setStringSliceField(t, &node, "Writes", writes)

	upserted, err := store.UpsertNode(ctx, node)
	if err != nil {
		t.Fatalf("UpsertNode() error = %v", err)
	}
	assertStringSliceField(t, *upserted, "Reads", reads)
	assertStringSliceField(t, *upserted, "Writes", writes)

	nodes, err := store.ListNodes(ctx, "dag-rw")
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("ListNodes() len = %d, want 1", len(nodes))
	}
	assertStringSliceField(t, nodes[0], "Reads", reads)
	assertStringSliceField(t, nodes[0], "Writes", writes)

	run := createSQLiteTaskDAGRun(t, ctx, store, "run-rw", "dag-rw")
	cloneAndPromoteSQLiteRun(t, ctx, store, "dag-rw", run.ID)
	runNodes, err := store.ListRunNodes(ctx, "dag-rw", run.ID)
	if err != nil {
		t.Fatalf("ListRunNodes() error = %v", err)
	}
	if len(runNodes) != 1 {
		t.Fatalf("ListRunNodes() len = %d, want 1", len(runNodes))
	}
	assertStringSliceField(t, runNodes[0], "Reads", reads)
	assertStringSliceField(t, runNodes[0], "Writes", writes)
}

func TestTaskDAGNodeFieldGuardSQLCFieldsMustMapToDomainNode(t *testing.T) {
	domain := reflect.TypeFor[Node]()
	rows := []struct {
		name       string
		typ        reflect.Type
		exemptions map[string]string
	}{
		{name: "UpsertTaskDagNodeRow", typ: reflect.TypeFor[sqlc.UpsertTaskDagNodeRow]()},
		{name: "PatchTaskDagNodeConfigIfUnchangedRow", typ: reflect.TypeFor[sqlc.PatchTaskDagNodeConfigIfUnchangedRow]()},
		{name: "UpdateTaskDagNodeStatusIfCurrentRow", typ: reflect.TypeFor[sqlc.UpdateTaskDagNodeStatusIfCurrentRow]()},
		{name: "ListTaskDagNodesRow", typ: reflect.TypeFor[sqlc.ListTaskDagNodesRow]()},
		{name: "AssignTaskDagNodeRow", typ: reflect.TypeFor[sqlc.AssignTaskDagNodeRow]()},
		{name: "ListTaskDagRunNodesRow", typ: reflect.TypeFor[sqlc.ListTaskDagRunNodesRow]()},
		{name: "GetTaskDagRunNodeForUpdateRow", typ: reflect.TypeFor[sqlc.GetTaskDagRunNodeForUpdateRow]()},
		{name: "LookupNodesBySpawningThreadRow", typ: reflect.TypeFor[sqlc.LookupNodesBySpawningThreadRow]()},
		{name: "ListRunningTaskDagNodesByAssigneeRow", typ: reflect.TypeFor[sqlc.ListRunningTaskDagNodesByAssigneeRow]()},
		{name: "GetTaskDagNodesForUpdateRow", typ: reflect.TypeFor[sqlc.GetTaskDagNodesForUpdateRow]()},
		{name: "BindRunningTaskDagNodeTurnRow", typ: reflect.TypeFor[sqlc.BindRunningTaskDagNodeTurnRow]()},
		{name: "TouchRunningTaskDagNodeEventRow", typ: reflect.TypeFor[sqlc.TouchRunningTaskDagNodeEventRow]()},
		{name: "UpdateRunningTaskDagNodeStatusRow", typ: reflect.TypeFor[sqlc.UpdateRunningTaskDagNodeStatusRow]()},
		{name: "CompleteTaskDagNodeRow", typ: reflect.TypeFor[sqlc.CompleteTaskDagNodeRow]()},
		{name: "ClaimTaskDagNodeOutputMaterializationRow", typ: reflect.TypeFor[sqlc.ClaimTaskDagNodeOutputMaterializationRow]()},
		{name: "FailTaskDagNodeIfNonTerminalRow", typ: reflect.TypeFor[sqlc.FailTaskDagNodeIfNonTerminalRow]()},
		{name: "MarkDispatchIncompleteNodesWithoutActiveWakeupRow", typ: reflect.TypeFor[sqlc.MarkDispatchIncompleteNodesWithoutActiveWakeupRow]()},
		{name: "UpdateTaskDagNodeSpawningThreadRow", typ: reflect.TypeFor[sqlc.UpdateTaskDagNodeSpawningThreadRow](), exemptions: map[string]string{
			"Column23": "sqlc generates this name for previous_spawning_thread_id, an auxiliary CTE value not part of taskdag.Node",
		}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			assertSQLCNodeRowMapped(t, row.name, row.typ, domain, row.exemptions)
		})
	}
}

func assertSQLCNodeRowMapped(t *testing.T, rowName string, rowType, domainType reflect.Type, exemptions map[string]string) {
	t.Helper()
	for i := 0; i < rowType.NumField(); i++ {
		field := rowType.Field(i)
		if _, ok := domainType.FieldByName(field.Name); ok {
			continue
		}
		reason := exemptions[field.Name]
		if reason == "" {
			t.Fatalf("%s.%s is not mapped to taskdag.Node and has no explicit exemption reason", rowName, field.Name)
		}
	}
}

func setStringSliceField(t *testing.T, target any, field string, values []string) {
	t.Helper()
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		t.Fatalf("target = %T, want pointer to struct", target)
	}
	fieldValue := v.Elem().FieldByName(field)
	if !fieldValue.IsValid() {
		t.Fatalf("%T missing %s field", target, field)
	}
	if !fieldValue.CanSet() || fieldValue.Kind() != reflect.Slice || fieldValue.Type().Elem().Kind() != reflect.String {
		t.Fatalf("%T.%s type = %v, want settable []string", target, field, fieldValue.Type())
	}
	fieldValue.Set(reflect.ValueOf(append([]string(nil), values...)))
}

func assertStringSliceField(t *testing.T, node Node, field string, want []string) {
	t.Helper()
	fieldValue := reflect.ValueOf(node).FieldByName(field)
	if !fieldValue.IsValid() {
		t.Fatalf("taskdag.Node missing %s field", field)
	}
	if fieldValue.Kind() != reflect.Slice || fieldValue.Type().Elem().Kind() != reflect.String {
		t.Fatalf("taskdag.Node.%s type = %v, want []string", field, fieldValue.Type())
	}
	got := fieldValue.Interface().([]string)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("taskdag.Node.%s = %#v, want %#v", field, got, want)
	}
}
