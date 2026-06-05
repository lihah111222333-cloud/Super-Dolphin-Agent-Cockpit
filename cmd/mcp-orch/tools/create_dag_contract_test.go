package tools

import (
	"errors"
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
	env := mcpcommon.NewToolErrorEnvelope("task_create_dag", platformdb.ErrConflict)
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
