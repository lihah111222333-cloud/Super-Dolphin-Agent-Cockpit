package tools

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestCreateDAGNodesFromInputRejectsInvalidTopology(t *testing.T) {
	tests := []struct {
		name  string
		nodes []CreateDAGNodeInput
		want  string
	}{
		{
			name: "duplicate node key",
			nodes: []CreateDAGNodeInput{
				{NodeKey: "a", Title: "A"},
				{NodeKey: " a ", Title: "A2"},
			},
			want: "already exists",
		},
		{
			name:  "unknown dependency",
			nodes: []CreateDAGNodeInput{{NodeKey: "a", Title: "A", DependsOn: []string{"missing"}}},
			want:  "unknown node",
		},
		{
			name: "cycle",
			nodes: []CreateDAGNodeInput{
				{NodeKey: "a", Title: "A", DependsOn: []string{"b"}},
				{NodeKey: "b", Title: "B", DependsOn: []string{"a"}},
			},
			want: "cycle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createDAGNodesFromInput(tc.nodes)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("createDAGNodesFromInput() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestCreateDAGNodesFromInputRejectsInvalidTopologyWithStableCode(t *testing.T) {
	_, err := createDAGNodesFromInput([]CreateDAGNodeInput{
		{NodeKey: "a", Title: "A", DependsOn: []string{"missing"}},
	})
	if err == nil {
		t.Fatal("createDAGNodesFromInput() error = nil, want invalid topology")
	}
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "invalid_input" {
		t.Fatalf("coded error = %#v, want invalid_input (err=%v)", coded, err)
	}
}

func TestCreateDAGNodesFromInputPreservesReadsWrites(t *testing.T) {
	reads := []string{"shared://inputs/source.md"}
	writes := []string{"shared://outputs/report.md"}
	node := CreateDAGNodeInput{NodeKey: "rw", Title: "Reads Writes"}
	setStructStringSliceField(t, &node, "Reads", reads)
	setStructStringSliceField(t, &node, "Writes", writes)

	got, err := createDAGNodesFromInput([]CreateDAGNodeInput{node})
	if err != nil {
		t.Fatalf("createDAGNodesFromInput() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("createDAGNodesFromInput() len = %d, want 1", len(got))
	}
	assertStructStringSliceField(t, got[0], "Reads", reads)
	assertStructStringSliceField(t, got[0], "Writes", writes)
}

func TestCreateDAGRequestRejectsUnknownFinalNodeKeyWithStableCode(t *testing.T) {
	_, err := createDAGRequestFromInput(CreateDAGInput{
		AgentID:      "designer-1",
		DagKey:       "dag-final",
		Title:        "Final DAG",
		FinalNodeKey: "missing",
		Nodes:        []CreateDAGNodeInput{{NodeKey: "present", Title: "Present"}},
	}, "")
	assertCreateDAGInvalidInputCode(t, err)
}

func TestCreateDAGRequestRejectsFlatScheduleConflictWithStableCode(t *testing.T) {
	_, err := createDAGRequestFromInput(CreateDAGInput{
		AgentID:  "designer-1",
		DagKey:   "dag-conflict",
		Title:    "Conflict DAG",
		Trigger:  "manual",
		Schedule: DAGScheduleInput{Trigger: "auto"},
	}, "")
	assertCreateDAGInvalidInputCode(t, err)
}

func TestTaskCreateDAGEnvelopeClassifiesDuplicateDagKeyAsInvalidInput(t *testing.T) {
	env := mcpcommon.NewToolErrorEnvelopeWithClassifier("task_create_dag", "", platformdb.ErrConflict, nil, ToolErrorClassifier)
	if env.Code != "invalid_input" {
		t.Fatalf("tool error code = %q, want invalid_input", env.Code)
	}
}

func assertCreateDAGInvalidInputCode(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("createDAGRequestFromInput() error = nil, want invalid_input")
	}
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "invalid_input" {
		t.Fatalf("coded error = %#v, want invalid_input (err=%v)", coded, err)
	}
	env := mcpcommon.NewToolErrorEnvelope("task_create_dag", err)
	if env.Code != "invalid_input" {
		t.Fatalf("tool error code = %q, want invalid_input", env.Code)
	}
}

func TestTaskCreateDAGSchemaExposesFlatShortcuts(t *testing.T) {
	defs := taskToolDefinitions(ToolPorts{})
	createDAG := mustFindToolDefinition(t, defs, "task_create_dag")
	props := createDAG.InputSchema["properties"].(map[string]any)
	for _, want := range []string{"trigger", "default_retry", "max_concurrency"} {
		if _, ok := props[want].(map[string]any); !ok {
			t.Fatalf("task_create_dag properties missing flat %s: %#v", want, props)
		}
	}
	nodes := props["nodes"].(map[string]any)
	items := nodes["items"].(map[string]any)
	nodeProps := items["properties"].(map[string]any)
	for _, want := range []string{"retry", "timeout_sec", "on_failure"} {
		if _, ok := nodeProps[want].(map[string]any); !ok {
			t.Fatalf("task_create_dag node properties missing flat %s: %#v", want, nodeProps)
		}
	}
	required := createDAG.InputSchema["required"].([]string)
	if slices.Contains(required, "schedule") {
		t.Fatalf("task_create_dag required = %#v, want flat schedule path to make schedule optional", required)
	}
	if slices.Contains(required, "agent_id") {
		t.Fatalf("task_create_dag required = %#v, want trusted _agentId to supply creator identity", required)
	}
}

func setStructStringSliceField(t *testing.T, target any, field string, values []string) {
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

func assertStructStringSliceField(t *testing.T, target any, field string, want []string) {
	t.Helper()
	v := reflect.ValueOf(target)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	fieldValue := v.FieldByName(field)
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
