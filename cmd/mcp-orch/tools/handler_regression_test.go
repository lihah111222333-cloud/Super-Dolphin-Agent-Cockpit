package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestHandleListDAGsReturnsWrappedDAGs(t *testing.T) {
	var gotFilter contract.ListDAGsFilter
	handler := HandleListDAGs(&golden.OrchestrationStub{
		ListDAGsFunc: func(_ context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
			gotFilter = filter
			return []contract.DAGSummary{{DagKey: "dag-1", Title: "Daily", Status: "running"}}, nil
		},
	})

	resp, err := handler(context.Background(), json.RawMessage(`{"status":" running ","keyword":" daily ","limit":7}`))
	if err != nil {
		t.Fatalf("HandleListDAGs() error = %v", err)
	}
	out, ok := resp.(ListDAGsOutput)
	if !ok {
		t.Fatalf("HandleListDAGs() response = %T, want ListDAGsOutput", resp)
	}
	if len(out.DAGs) != 1 || out.DAGs[0].DagKey != "dag-1" {
		t.Fatalf("HandleListDAGs() = %#v", out)
	}
	assertEnvelopeCounts(t, "HandleListDAGs()", len(out.Data), out.Total, out.Showing, out.Truncated, out.Hint)
	if out.Data[0].DagKey != "dag-1" {
		t.Fatalf("HandleListDAGs() data = %#v", out.Data)
	}
	if gotFilter != (contract.ListDAGsFilter{Status: "running", Keyword: "daily", Limit: 7}) {
		t.Fatalf("ListDAGs filter = %#v", gotFilter)
	}
}

func TestHandleListAgentsEnvelopeKeepsLegacyArrayDefault(t *testing.T) {
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{AgentID: "agent-1", State: "idle"}}, nil
		},
	})

	legacy, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("HandleListAgents() legacy error = %v", err)
	}
	if _, ok := legacy.([]contract.AgentSnapshot); !ok {
		t.Fatalf("HandleListAgents() legacy response = %T, want []contract.AgentSnapshot", legacy)
	}

	result, err := handler(context.Background(), json.RawMessage(`{"envelope":true,"limit":5}`))
	if err != nil {
		t.Fatalf("HandleListAgents() envelope error = %v", err)
	}
	out, ok := result.(ListAgentsOutput)
	if !ok {
		t.Fatalf("HandleListAgents() envelope response = %T, want ListAgentsOutput", result)
	}
	if len(out.Agents) != 1 {
		t.Fatalf("HandleListAgents() agents = %#v", out.Agents)
	}
	assertEnvelopeCounts(t, "HandleListAgents()", len(out.Data), out.Total, out.Showing, out.Truncated, out.Hint)
}

func TestHandleCreateDAGPreservesFinalNodeKey(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-final",
		"title":"Daily final output",
		"schedule":{"trigger":"manual"},
		"final_node_key":" delivery_writer ",
		"nodes":[
			{"node_key":"source_monitor","title":"Source monitor"},
			{"node_key":"delivery_writer","title":"Delivery writer","depends_on":["source_monitor"]}
		]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(got.Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v raw=%s", err, string(got.Metadata))
	}
	if metadata["final_node_key"] != "delivery_writer" {
		t.Fatalf("metadata.final_node_key = %v, want delivery_writer (metadata=%v)", metadata["final_node_key"], metadata)
	}
}

func TestHandleCreateDAGRejectsUnknownFinalNodeKey(t *testing.T) {
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			t.Fatal("CreateDAG should not be called for unknown final_node_key")
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-final",
		"title":"Daily final output",
		"schedule":{"trigger":"manual"},
		"final_node_key":"missing_final",
		"nodes":[{"node_key":"source_monitor","title":"Source monitor"}]
	}`))
	if err == nil || err.Error() != "final_node_key missing_final does not match any node_key" {
		t.Fatalf("HandleCreateDAG() error = %v, want final_node_key validation", err)
	}
}

func TestHandleCreateDAGPreservesNodeConfig(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-config",
		"title":"Runnable DAG",
		"schedule":{"trigger":"manual"},
		"nodes":[{
			"node_key":"smoke",
			"title":"Smoke",
			"node_type":"automation",
			"config":{"exec":{"kind":"command_card","command_ref":"build","cwd":"/repo","workspace_roots":["/repo"]},"outputs":{"to_node_result":true}}
		}]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	assertJSONEqual(t, got.Nodes[0].Config, `{"exec":{"kind":"command_card","command_ref":"build","cwd":"/repo","workspace_roots":["/repo"]},"outputs":{"to_node_result":true}}`)
}

func TestHandleCreateDAGSynthesizesAutomationConfigFromCommandRef(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-command-ref",
		"title":"Runnable DAG",
		"schedule":{"trigger":"manual"},
		"nodes":[{
			"node_key":"smoke",
			"title":"Smoke",
			"node_type":"automation",
			"command_ref":" build ",
			"config":{"exec":{"cwd":"/repo","workspace_roots":["/repo"]}}
		}]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	assertJSONEqual(t, got.Nodes[0].Config, `{"exec":{"kind":"command_card","command_ref":"build","cwd":"/repo","workspace_roots":["/repo"]}}`)
}

func TestHandleCreateDAGInfersAutomationNodeTypeFromCommandRef(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-command-ref",
		"title":"Runnable DAG",
		"schedule":{"trigger":"manual"},
		"nodes":[{
			"node_key":"smoke",
			"title":"Smoke",
			"command_ref":" build ",
			"config":{"exec":{"cwd":"/repo","workspace_roots":["/repo"]}}
		}]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	if got.Nodes[0].NodeType != "automation" {
		t.Fatalf("NodeType = %q, want automation", got.Nodes[0].NodeType)
	}
	assertJSONEqual(t, got.Nodes[0].Config, `{"exec":{"kind":"command_card","command_ref":"build","cwd":"/repo","workspace_roots":["/repo"]}}`)
}

func TestHandleCreateDAGMergesCommandRefIntoAutomationConfig(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-config-output",
		"title":"Runnable DAG",
		"schedule":{"trigger":"manual"},
		"nodes":[{
			"node_key":"smoke",
			"title":"Smoke",
			"node_type":"automation",
			"command_ref":" build ",
			"config":{"exec":{"cwd":"/repo","workspace_roots":["/repo"]},"outputs":{"to_node_result":true}}
		}]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
	assertJSONEqual(t, got.Nodes[0].Config, `{"exec":{"kind":"command_card","command_ref":"build","cwd":"/repo","workspace_roots":["/repo"]},"outputs":{"to_node_result":true}}`)
}

func TestHandleCreateDAGAcceptsFlatScheduleAndNodeExecution(t *testing.T) {
	var got contract.CreateDAGRequest
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
			got = req
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-flat",
		"title":"Flat DAG",
		"trigger":"manual",
		"default_retry":2,
		"max_concurrency":3,
		"nodes":[{
			"node_key":"score",
			"title":"Score",
			"node_type":"agent",
			"retry":1,
			"timeout_sec":30
		}]
	}`))
	if err != nil {
		t.Fatalf("HandleCreateDAG() error = %v", err)
	}
	assertJSONEqual(t, got.Metadata, `{"schedule":{"trigger":"manual","default_retry":2,"max_concurrency":3}}`)
	assertJSONEqual(t, got.Nodes[0].Config, `{"execution":{"retry":1,"timeout_sec":30}}`)
}

func TestHandleCreateDAGRejectsUnsupportedExecutionFields(t *testing.T) {
	handler := HandleCreateDAG(&golden.OrchestrationStub{})
	tests := []struct {
		name  string
		field string
		node  string
	}{
		{name: "flat on_failure", field: "on_failure", node: `"on_failure":"fail_fast"`},
		{name: "flat pool", field: "pool", node: `"pool":"default"`},
		{name: "flat priority", field: "priority", node: `"priority":1`},
		{name: "nested on_failure", field: "on_failure", node: `"execution":{"on_failure":"fail_fast"}`},
		{name: "nested pool", field: "pool", node: `"execution":{"pool":"default"}`},
		{name: "nested priority", field: "priority", node: `"execution":{"priority":1}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := `{"agent_id":"designer-1","dag_key":"dag-unsupported","title":"Unsupported","nodes":[{"node_key":"n","title":"N",` + test.node + `}]}`
			_, err := handler(context.Background(), json.RawMessage(input))
			if err == nil || !strings.Contains(err.Error(), `unknown field "`+test.field+`"`) {
				t.Fatalf("HandleCreateDAG() error = %v, want unknown field %q", err, test.field)
			}
		})
	}
}

func TestHandleCreateDAGRejectsFlatScheduleConflict(t *testing.T) {
	handler := HandleCreateDAG(&golden.OrchestrationStub{
		CreateDAGFunc: func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
			t.Fatal("CreateDAG should not be called when flat schedule conflicts with nested schedule")
			return contract.DAGDetail{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id":"designer-1",
		"dag_key":"dag-conflict",
		"title":"Conflict",
		"trigger":"manual",
		"schedule":{"trigger":"scheduled"}
	}`))
	if err == nil || err.Error() != "trigger conflicts with schedule.trigger" {
		t.Fatalf("HandleCreateDAG() error = %v, want trigger conflict", err)
	}
}

func TestTaskCreateDAGSchemaExposesNodeConfig(t *testing.T) {
	defs := taskToolDefinitions(ToolPorts{})
	var createDAG ToolDefinition
	for _, def := range defs {
		if def.Name == "task_create_dag" {
			createDAG = def
			break
		}
	}
	if createDAG.Name == "" {
		t.Fatal("task_create_dag tool definition not found")
	}
	props := createDAG.InputSchema["properties"].(map[string]any)
	nodes := props["nodes"].(map[string]any)
	items := nodes["items"].(map[string]any)
	nodeProps := items["properties"].(map[string]any)
	if _, ok := nodeProps["config"].(map[string]any); !ok {
		t.Fatalf("task_create_dag node properties = %#v, want config schema", nodeProps)
	}
}

func TestTaskDAGApplyOpsSchemaExposesOpDiscriminator(t *testing.T) {
	defs := taskToolDefinitions(ToolPorts{})
	var applyOps ToolDefinition
	for _, def := range defs {
		if def.Name == "task_dag_apply_ops" {
			applyOps = def
			break
		}
	}
	if applyOps.Name == "" {
		t.Fatal("task_dag_apply_ops tool definition not found")
	}
	props := applyOps.InputSchema["properties"].(map[string]any)
	ops := props["ops"].(map[string]any)
	items := ops["items"].(map[string]any)
	itemProps := items["properties"].(map[string]any)
	opSchema, ok := itemProps["op"].(map[string]any)
	if !ok {
		t.Fatalf("task_dag_apply_ops ops.items.properties = %#v, want op discriminator schema", itemProps)
	}
	nodeSchema := itemProps["node"].(map[string]any)
	nodeProps := nodeSchema["properties"].(map[string]any)
	if _, ok := nodeProps["assigned_to"]; !ok {
		t.Fatalf("task_dag_apply_ops add_node schema properties = %#v, want assigned_to", nodeProps)
	}
	for _, want := range []string{"update_dag", "add_node", "update_node", "remove_node"} {
		if !slices.Contains(EnumValues(Schema(opSchema)), want) {
			t.Fatalf("op enum = %#v, want %s", opSchema["enum"], want)
		}
	}
	required := items["required"].([]string)
	if !slices.Contains(required, "op") {
		t.Fatalf("ops.items.required = %#v, want op", required)
	}
}

func TestTaskDAGApplyOpsSchemaExposesFlatAction(t *testing.T) {
	defs := taskToolDefinitions(ToolPorts{})
	applyOps := mustFindToolDefinition(t, defs, "task_dag_apply_ops")
	props := applyOps.InputSchema["properties"].(map[string]any)
	action, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("task_dag_apply_ops properties = %#v, want action", props)
	}
	for _, want := range []string{"update_dag", "add_node", "update_node", "remove_node", "apply_ops_raw"} {
		if !slices.Contains(EnumValues(Schema(action)), want) {
			t.Fatalf("action enum = %#v, want %s", action["enum"], want)
		}
	}
	required := applyOps.InputSchema["required"].([]string)
	if slices.Contains(required, "dag_key") || slices.Contains(required, "ops") {
		t.Fatalf("task_dag_apply_ops required = %#v, want pos/flat action support", required)
	}
}

func TestHandleApplyOpsBuildsFlatAddNode(t *testing.T) {
	var got contract.ApplyOpsRequest
	handler := HandleApplyOps(&golden.OrchestrationStub{
		ApplyOpsFunc: func(_ context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
			got = req
			return contract.ApplyOpsResponse{NewVersion: 6}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"pos":"dag:dag-flat",
		"base_version":5,
		"action":"add_node",
		"node_key":"score",
		"title":"Score",
		"node_type":"automation",
		"assigned_to":"agent-score",
		"depends_on":["plan"],
		"config":{"exec":{"kind":"command_card","command_ref":"score","cwd":"/repo","workspace_roots":["/repo"]}}
	}`))
	if err != nil {
		t.Fatalf("HandleApplyOps() error = %v", err)
	}
	if got.DagKey != "dag-flat" || got.BaseVersion != 5 {
		t.Fatalf("ApplyOps request = %#v", got)
	}
	assertJSONEqual(t, got.Ops, `[{"op":"add_node","node":{"node_key":"score","title":"Score","node_type":"automation","assigned_to":"agent-score","depends_on":["plan"],"config":{"exec":{"kind":"command_card","command_ref":"score","cwd":"/repo","workspace_roots":["/repo"]}}}}]`)
}

func TestHandleApplyOpsBuildsFlatAddNodeReadsWrites(t *testing.T) {
	var got contract.ApplyOpsRequest
	handler := HandleApplyOps(&golden.OrchestrationStub{
		ApplyOpsFunc: func(_ context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
			got = req
			return contract.ApplyOpsResponse{NewVersion: 6}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"dag_key":"dag-flat",
		"base_version":5,
		"action":"add_node",
		"node_key":"materialize",
		"title":"Materialize",
		"node_type":"agent",
		"assigned_to":"agent-materialize",
		"reads":["shared://inputs/source.md"],
		"writes":["shared://outputs/report.md"]
	}`))
	if err != nil {
		t.Fatalf("HandleApplyOps() error = %v", err)
	}
	assertJSONEqual(t, got.Ops, `[{"op":"add_node","node":{"node_key":"materialize","title":"Materialize","node_type":"agent","assigned_to":"agent-materialize","reads":["shared://inputs/source.md"],"writes":["shared://outputs/report.md"]}}]`)
}

func TestHandleApplyOpsBuildsFlatUpdateNode(t *testing.T) {
	var got contract.ApplyOpsRequest
	handler := HandleApplyOps(&golden.OrchestrationStub{
		ApplyOpsFunc: func(_ context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
			got = req
			return contract.ApplyOpsResponse{NewVersion: 6}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"dag_key":"dag-flat",
		"base_version":5,
		"action":"update_node",
		"node_key":"score",
		"title":"Score v2",
		"depends_on":[]
	}`))
	if err != nil {
		t.Fatalf("HandleApplyOps() error = %v", err)
	}
	assertJSONEqual(t, got.Ops, `[{"op":"update_node","node_key":"score","patch":{"title":"Score v2","depends_on":[]}}]`)
}

func TestHandleApplyOpsBuildsFlatUpdateNodeReadsWrites(t *testing.T) {
	var got contract.ApplyOpsRequest
	handler := HandleApplyOps(&golden.OrchestrationStub{
		ApplyOpsFunc: func(_ context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
			got = req
			return contract.ApplyOpsResponse{NewVersion: 6}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"dag_key":"dag-flat",
		"base_version":5,
		"action":"update_node",
		"node_key":"materialize",
		"reads":["shared://inputs/new-source.md"],
		"writes":[]
	}`))
	if err != nil {
		t.Fatalf("HandleApplyOps() error = %v", err)
	}
	assertJSONEqual(t, got.Ops, `[{"op":"update_node","node_key":"materialize","patch":{"reads":["shared://inputs/new-source.md"],"writes":[]}}]`)
}

func TestHandleApplyOpsRejectsPatchFlatConflict(t *testing.T) {
	handler := HandleApplyOps(&golden.OrchestrationStub{
		ApplyOpsFunc: func(context.Context, contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
			t.Fatal("ApplyOps should not be called when patch conflicts with flat fields")
			return contract.ApplyOpsResponse{}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"dag_key":"dag-flat",
		"base_version":5,
		"action":"update_node",
		"node_key":"score",
		"title":"Score v2",
		"patch":{"title":"Score raw"}
	}`))
	if err == nil || err.Error() != "patch cannot be combined with flat update_node fields" {
		t.Fatalf("HandleApplyOps() error = %v, want patch conflict", err)
	}
}

func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("unmarshal got JSON: %v raw=%s", err, string(got))
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal want JSON: %v raw=%s", err, want)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", string(got), want)
	}
}

func TestOrchestrationNilGuardsUseConsistentMessage(t *testing.T) {
	handlers := []struct {
		name    string
		handler ToolHandler
		input   string
	}{
		{name: "launch", handler: HandleLaunchAgent(nil), input: `{}`},
		{name: "send", handler: HandleSendMessage(SendMessagePorts{}), input: `{}`},
		{name: "stop", handler: HandleStopAgent(nil), input: `{}`},
		{name: "recover", handler: HandleRecoverAgent(nil), input: `{}`},
		{name: "interrupt", handler: HandleInterruptAgent(nil), input: `{}`},
		{name: "list", handler: HandleListAgents(AgentListPorts{}), input: `{}`},
		{name: "report", handler: HandleGetAgentReport(nil), input: `{}`},
		{name: "create_dag", handler: HandleCreateDAG(nil), input: `{}`},
		{name: "list_dags", handler: HandleListDAGs(nil), input: `{}`},
		{name: "get_dag", handler: HandleGetDAG(nil), input: `{}`},
		{name: "update_node", handler: HandleUpdateNode(nil), input: `{}`},
		{name: "start_dag", handler: HandleStartDAG(nil), input: `{}`},
		{name: "delete_dag", handler: HandleDeleteDAG(nil), input: `{}`},
		{name: "get_run", handler: HandleGetRun(nil), input: `{}`},
		{name: "apply_ops", handler: HandleApplyOps(nil), input: `{}`},
		{name: "list_runs", handler: HandleListRuns(nil), input: `{}`},
	}
	for _, tc := range handlers {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.handler(context.Background(), json.RawMessage(tc.input))
			wantErr := "orchestration service is not configured"
			if tc.name == "report" {
				wantErr = "agent report port is not configured"
			}
			if err == nil || err.Error() != wantErr {
				t.Fatalf("handler error = %v", err)
			}
		})
	}
}
