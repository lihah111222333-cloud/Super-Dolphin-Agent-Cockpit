package tools

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestParseOrchPosCanonicalSelectors(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want orchPos
	}{
		{name: "agent", raw: "agent:agent-1", want: orchPos{Raw: "agent:agent-1", AgentID: "agent-1"}},
		{name: "dag", raw: "dag:review-loop", want: orchPos{Raw: "dag:review-loop", DagKey: "review-loop"}},
		{name: "dag run node", raw: "dag:review-loop/run:run-1/node:score", want: orchPos{Raw: "dag:review-loop/run:run-1/node:score", DagKey: "review-loop", RunKey: "run-1", NodeKey: "score"}},
		{name: "dag run id node", raw: "dag:review-loop/run_id:42/node:score", want: orchPos{Raw: "dag:review-loop/run_id:42/node:score", DagKey: "review-loop", RunID: 42, NodeKey: "score"}},
		{name: "run only", raw: "run:run-1", want: orchPos{Raw: "run:run-1", RunKey: "run-1"}},
		{name: "workspace", raw: "workspace:ws-1", want: orchPos{Raw: "workspace:ws-1", WorkspaceRunKey: "ws-1"}},
		{name: "shared path", raw: "shared:handoff/task-1/settings.json", want: orchPos{Raw: "shared:handoff/task-1/settings.json", SharedPath: "handoff/task-1/settings.json"}},
		{name: "prompt path", raw: "prompt:main/dag_designer_zh", want: orchPos{Raw: "prompt:main/dag_designer_zh", PromptKey: "main/dag_designer_zh"}},
		{name: "command path", raw: "command:build/smoke", want: orchPos{Raw: "command:build/smoke", CommandKey: "build/smoke"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOrchPos(tc.raw)
			if err != nil {
				t.Fatalf("parseOrchPos() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("parseOrchPos() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseOrchPosRejectsInvalidSelectors(t *testing.T) {
	tests := []string{
		"agent:agent-1/run:run-1",
		"node:score",
		"dag:dag-1/dag:dag-2",
		"dag:dag-1/run_id:x/node:score",
		"dag:dag-1/run_id:42",
		"dag:dag-1/run:run-1/run_id:42/node:score",
		"bogus:x",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			_, err := parseOrchPos(raw)
			if err == nil {
				t.Fatal("parseOrchPos() error = nil, want invalid_pos")
			}
			var coded *mcpcommon.CodedToolError
			if !errors.As(err, &coded) || coded.Code != "invalid_pos" {
				t.Fatalf("parseOrchPos() error = %T %[1]v, want invalid_pos", err)
			}
		})
	}
}

func TestReadToolSchemasExposePosWithoutLegacyRequired(t *testing.T) {
	cases := []struct {
		defs        []ToolDefinition
		toolName    string
		legacyField string
	}{
		{defs: orchestrationToolDefinitions(ToolPorts{}), toolName: "get_agent_report", legacyField: "agent_id"},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_get_dag", legacyField: "dag_key"},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_get_run", legacyField: "run_key"},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_list_runs", legacyField: "dag_key"},
		{defs: workspaceToolDefinitions(nil), toolName: "workspace_get_run", legacyField: "run_key"},
		{defs: workspaceToolDefinitions(nil), toolName: "workspace_list_runs", legacyField: "dag_key"},
		{defs: sharedFileToolDefinitions(nil), toolName: "shared_file_read", legacyField: "path"},
		{defs: promptToolDefinitions(nil, nil), toolName: "prompt_get", legacyField: "prompt_key"},
		{defs: commandToolDefinitions(nil), toolName: "command_get", legacyField: "card_key"},
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			def := mustFindToolDefinition(t, tc.defs, tc.toolName)
			props := def.InputSchema["properties"].(map[string]any)
			if _, ok := props["pos"].(map[string]any); !ok {
				t.Fatalf("%s schema properties = %#v, want pos", tc.toolName, props)
			}
			required, _ := def.InputSchema["required"].([]string)
			if slices.Contains(required, tc.legacyField) {
				t.Fatalf("%s required = %#v, want %s optional when pos is provided", tc.toolName, required, tc.legacyField)
			}
		})
	}
}

func TestReadHandlersAcceptPosSelectors(t *testing.T) {
	t.Run("agent report", assertAgentReportPosSelector)
	t.Run("dag", assertDAGPosSelector)
	t.Run("run", assertRunPosSelector)
	t.Run("list runs", assertListRunsPosSelector)
	t.Run("workspace run", assertWorkspaceRunPosSelector)
	t.Run("shared file", assertSharedFilePosSelector)
	t.Run("prompt", assertPromptPosSelector)
	t.Run("command", assertCommandPosSelector)
}

func assertAgentReportPosSelector(t *testing.T) {
	var gotAgentID string
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			gotAgentID = agentID
			return contract.AgentReportResult{AgentID: agentID, Report: "done"}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"agent:agent-1"}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	if gotAgentID != "agent-1" || result.(contract.AgentReportResult).AgentID != "agent-1" {
		t.Fatalf("agent report pos got agent_id=%q result=%#v", gotAgentID, result)
	}
}

func assertDAGPosSelector(t *testing.T) {
	var gotDagKey string
	handler := HandleGetDAG(&golden.OrchestrationStub{
		GetDAGFunc: func(_ context.Context, dagKey string) (contract.DAGDetail, error) {
			gotDagKey = dagKey
			return contract.DAGDetail{DAG: contract.DAGSummary{DagKey: dagKey}}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop"}`))
	if err != nil {
		t.Fatalf("HandleGetDAG() error = %v", err)
	}
	if gotDagKey != "review-loop" || result.(contract.DAGDetail).DAG.DagKey != "review-loop" {
		t.Fatalf("dag pos got dag_key=%q result=%#v", gotDagKey, result)
	}
}

func assertRunPosSelector(t *testing.T) {
	var got contract.GetRunRequest
	handler := HandleGetRun(&golden.OrchestrationStub{
		GetRunFunc: func(_ context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
			got = req
			return contract.GetRunResponse{Run: contract.Run{RunKey: req.RunKey}}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop/run:run-1"}`))
	if err != nil {
		t.Fatalf("HandleGetRun() error = %v", err)
	}
	if got.RunKey != "run-1" || result.(contract.GetRunResponse).Run.RunKey != "run-1" {
		t.Fatalf("run pos got req=%#v result=%#v", got, result)
	}
}

func assertListRunsPosSelector(t *testing.T) {
	var got contract.ListRunsRequest
	handler := HandleListRuns(&golden.OrchestrationStub{
		ListRunsFunc: func(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
			got = req
			return contract.ListRunsResponse{Runs: []contract.Run{{DagKey: req.DagKey}}}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop","status":"running","limit":3}`))
	if err != nil {
		t.Fatalf("HandleListRuns() error = %v", err)
	}
	if got.DagKey != "review-loop" || got.Status != "running" || got.Limit != 3 {
		t.Fatalf("list runs pos req = %#v", got)
	}
	assertListRunsEnvelope(t, result)
}

func assertWorkspaceRunPosSelector(t *testing.T) {
	var gotRunKey string
	handler := HandleWorkspaceGetRun(stubWorkspaceService{
		getRun: func(_ context.Context, runKey string) (*workspace.Run, error) {
			gotRunKey = runKey
			return &workspace.Run{RunKey: runKey, Status: "created"}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"workspace:ws-1"}`))
	if err != nil {
		t.Fatalf("HandleWorkspaceGetRun() error = %v", err)
	}
	if gotRunKey != "ws-1" || result.(*workspaceRunDTO).RunKey != "ws-1" {
		t.Fatalf("workspace pos got run_key=%q result=%#v", gotRunKey, result)
	}
}

func assertSharedFilePosSelector(t *testing.T) {
	var gotPath string
	handler := HandleSharedFileRead(stubSharedFileStore{
		get: func(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
			gotPath = path
			return &sharedfilestore.SharedFile{Path: path, Content: "ok"}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"shared:handoff/task-1/settings.json"}`))
	if err != nil {
		t.Fatalf("HandleSharedFileRead() error = %v", err)
	}
	if gotPath != "handoff/task-1/settings.json" || result.(sharedFileDTO).Path != "handoff/task-1/settings.json" {
		t.Fatalf("shared pos got path=%q result=%#v", gotPath, result)
	}
}

func assertPromptPosSelector(t *testing.T) {
	var gotKey string
	handler := HandlePromptGet(stubPromptStore{
		get: func(_ context.Context, key string) (*promptstore.PromptTemplate, error) {
			gotKey = key
			return &promptstore.PromptTemplate{
				ID:        42,
				PromptKey: key,
				Title:     "Prompt",
				Tags:      json.RawMessage(`["scope.global"]`),
				Enabled:   true,
			}, nil
		},
	}, nil)
	result, err := handler(promptToolTestContext(), json.RawMessage(`{"pos":"prompt:main/dag_designer_zh"}`))
	if err != nil {
		t.Fatalf("HandlePromptGet() error = %v", err)
	}
	if gotKey != "main/dag_designer_zh" || result.(promptTemplateDTO).PromptKey != "main/dag_designer_zh" {
		t.Fatalf("prompt pos got key=%q result=%#v", gotKey, result)
	}
}

func assertCommandPosSelector(t *testing.T) {
	var gotKey string
	handler := HandleCommandGet(stubCommandStore{
		get: func(_ context.Context, key string) (*commandcardstore.CommandCard, error) {
			gotKey = key
			return &commandcardstore.CommandCard{CardKey: key, Title: "Build"}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"pos":"command:build/smoke"}`))
	if err != nil {
		t.Fatalf("HandleCommandGet() error = %v", err)
	}
	if gotKey != "build/smoke" || result.(commandCardDTO).CardKey != "build/smoke" {
		t.Fatalf("command pos got key=%q result=%#v", gotKey, result)
	}
}

func assertListRunsEnvelope(t *testing.T, result any) {
	out, ok := result.(ListRunsOutput)
	if !ok {
		t.Fatalf("HandleListRuns() response = %T, want ListRunsOutput", result)
	}
	if len(out.Runs) != 1 || len(out.Data) != 1 || out.Total != 1 || out.Showing != 1 || out.Truncated || out.Hint == "" {
		t.Fatalf("HandleListRuns() envelope = %#v", out)
	}
}

func TestMutationToolSchemasExposePosWithoutSelectorRequired(t *testing.T) {
	cases := []struct {
		defs           []ToolDefinition
		toolName       string
		legacyFields   []string
		requiredFields []string
	}{
		{defs: orchestrationToolDefinitions(ToolPorts{}), toolName: "send_message", legacyFields: []string{"agent_id"}, requiredFields: []string{"message"}},
		{defs: orchestrationToolDefinitions(ToolPorts{}), toolName: "stop_agent", legacyFields: []string{"agent_id"}},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_start_dag", legacyFields: []string{"dag_key"}},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_terminate_dag", legacyFields: []string{"dag_key", "run_key"}},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_delete_dag", legacyFields: []string{"dag_key"}},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_update_node", legacyFields: []string{"dag_key", "node_key", "run_id"}, requiredFields: []string{"status"}},
		{defs: taskToolDefinitions(ToolPorts{}), toolName: "task_dispatch_node", legacyFields: []string{"dag_key", "node_key", "run_id"}, requiredFields: []string{"assigned_to"}},
	}

	for _, tc := range cases {
		t.Run(tc.toolName, func(t *testing.T) {
			def := mustFindToolDefinition(t, tc.defs, tc.toolName)
			props := def.InputSchema["properties"].(map[string]any)
			if _, ok := props["pos"].(map[string]any); !ok {
				t.Fatalf("%s schema properties = %#v, want pos", tc.toolName, props)
			}
			required, _ := def.InputSchema["required"].([]string)
			for _, legacy := range tc.legacyFields {
				if slices.Contains(required, legacy) {
					t.Fatalf("%s required = %#v, want legacy selector %s optional when pos is provided", tc.toolName, required, legacy)
				}
			}
			for _, field := range tc.requiredFields {
				if !slices.Contains(required, field) {
					t.Fatalf("%s required = %#v, want business field %s required", tc.toolName, required, field)
				}
			}
		})
	}
}

func TestMutationHandlersAcceptPosSelectors(t *testing.T) {
	t.Run("send message", assertSendMessagePosSelector)
	t.Run("stop agent", assertStopAgentPosSelector)
	t.Run("start dag", assertStartDAGPosSelector)
	t.Run("terminate dag", assertTerminateDAGPosSelector)
	t.Run("delete dag", assertDeleteDAGPosSelector)
	t.Run("update runtime node", assertUpdateNodePosSelector)
	t.Run("dispatch runtime node", assertDispatchNodePosSelector)
}

func assertSendMessagePosSelector(t *testing.T) {
	var got contract.TurnSubmission
	handler := handleSendMessageWithStub(&golden.OrchestrationStub{
		SubmitTurnFunc: func(_ context.Context, req contract.TurnSubmission) error {
			got = req
			return nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"agent:agent-1","message":"hello"}`)); err != nil {
		t.Fatalf("HandleSendMessage() error = %v", err)
	}
	if got.AgentID != "agent-1" || got.ThreadID != "agent-1" || len(got.Inputs) != 1 {
		t.Fatalf("send_message pos submission = %#v", got)
	}
}

func assertStopAgentPosSelector(t *testing.T) {
	var gotAgentID string
	handler := HandleStopAgent(&golden.OrchestrationStub{
		StopAgentFunc: func(_ context.Context, agentID string) error {
			gotAgentID = agentID
			return nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"agent:agent-1"}`)); err != nil {
		t.Fatalf("HandleStopAgent() error = %v", err)
	}
	if gotAgentID != "agent-1" {
		t.Fatalf("stop_agent pos agent_id = %q", gotAgentID)
	}
}

func assertStartDAGPosSelector(t *testing.T) {
	var got contract.StartDAGRequest
	handler := HandleStartDAG(&golden.OrchestrationStub{
		StartDAGFunc: func(_ context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
			got = req
			return contract.StartDAGResponse{RunKey: "run-1"}, nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop","trigger_source":"manual"}`)); err != nil {
		t.Fatalf("HandleStartDAG() error = %v", err)
	}
	if got.DagKey != "review-loop" || got.TriggerSource != "manual" {
		t.Fatalf("start_dag pos req = %#v", got)
	}
}

func assertTerminateDAGPosSelector(t *testing.T) {
	var got contract.TerminateDAGRequest
	handler := HandleTerminateDAG(&golden.OrchestrationStub{
		TerminateDAGFunc: func(_ context.Context, req contract.TerminateDAGRequest) error {
			got = req
			return nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop/run:run-1","reason":"stop"}`)); err != nil {
		t.Fatalf("HandleTerminateDAG() error = %v", err)
	}
	if got.DagKey != "review-loop" || got.RunKey != "run-1" || got.Reason != "stop" {
		t.Fatalf("terminate_dag pos req = %#v", got)
	}
}

func assertDeleteDAGPosSelector(t *testing.T) {
	var got contract.DeleteDAGRequest
	handler := HandleDeleteDAG(&golden.OrchestrationStub{
		DeleteDAGFunc: func(_ context.Context, req contract.DeleteDAGRequest) error {
			got = req
			return nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop"}`)); err != nil {
		t.Fatalf("HandleDeleteDAG() error = %v", err)
	}
	if got.DagKey != "review-loop" {
		t.Fatalf("delete_dag pos req = %#v", got)
	}
}

func assertUpdateNodePosSelector(t *testing.T) {
	var got contract.UpdateNodeStatusRequest
	handler := HandleUpdateNode(&golden.OrchestrationStub{
		UpdateNodeStatusFunc: func(_ context.Context, req contract.UpdateNodeStatusRequest) (contract.DAGNode, error) {
			got = req
			return contract.DAGNode{DagKey: req.DagKey, NodeKey: req.NodeKey, Status: req.Status}, nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop/run_id:42/node:score","status":"done","result":"ok"}`)); err != nil {
		t.Fatalf("HandleUpdateNode() error = %v", err)
	}
	if got.DagKey != "review-loop" || got.NodeKey != "score" || got.RunID != 42 || got.Status != "done" {
		t.Fatalf("update_node pos req = %#v", got)
	}
}

func assertDispatchNodePosSelector(t *testing.T) {
	var got contract.DispatchNodeRequest
	handler := HandleDispatchNode(&golden.OrchestrationStub{
		DispatchNodeFunc: func(_ context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
			got = req
			return contract.DispatchNodeResponse{Enqueued: true}, nil
		},
	})
	if _, err := handler(context.Background(), json.RawMessage(`{"pos":"dag:review-loop/run_id:42/node:score","assigned_to":"agent-1"}`)); err != nil {
		t.Fatalf("HandleDispatchNode() error = %v", err)
	}
	if got.DagKey != "review-loop" || got.NodeKey != "score" || got.RunID != 42 || got.AssignedTo != "agent-1" {
		t.Fatalf("dispatch_node pos req = %#v", got)
	}
}

func TestPosSelectorRejectsLegacyConflict(t *testing.T) {
	handler := HandleGetDAG(&golden.OrchestrationStub{
		GetDAGFunc: func(context.Context, string) (contract.DAGDetail, error) {
			t.Fatal("GetDAG must not be called on conflicting selectors")
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"dag_key":"dag-a","pos":"dag:dag-b"}`))
	if err == nil {
		t.Fatal("HandleGetDAG() error = nil, want pos_conflict")
	}
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "pos_conflict" {
		t.Fatalf("HandleGetDAG() error = %T %[1]v, want pos_conflict", err)
	}
}

func TestMutationPosSelectorRejectsLegacyConflict(t *testing.T) {
	handler := HandleStartDAG(&golden.OrchestrationStub{
		StartDAGFunc: func(context.Context, contract.StartDAGRequest) (contract.StartDAGResponse, error) {
			t.Fatal("StartDAG must not be called on conflicting selectors")
			return contract.StartDAGResponse{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"dag_key":"dag-a","pos":"dag:dag-b"}`))
	if err == nil {
		t.Fatal("HandleStartDAG() error = nil, want pos_conflict")
	}
	var coded *mcpcommon.CodedToolError
	if !errors.As(err, &coded) || coded.Code != "pos_conflict" {
		t.Fatalf("HandleStartDAG() error = %T %[1]v, want pos_conflict", err)
	}
}

func mustFindToolDefinition(t *testing.T, defs []ToolDefinition, name string) ToolDefinition {
	t.Helper()
	for _, def := range defs {
		if def.Name == name {
			return def
		}
	}
	t.Fatalf("tool %q not found", name)
	return ToolDefinition{}
}
