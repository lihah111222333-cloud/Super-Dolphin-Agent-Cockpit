package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
)

func TestMCPOrchDAGRuntimeListDAGsCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{
		result: `{"dags":[{"dag_key":"dag-1","title":"Daily","status":"running"}]}`,
	}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	dags, err := runtime.ListDAGs(context.Background(), contract.ListDAGsFilter{
		Status:  " running ",
		Keyword: " daily ",
		Limit:   7,
	})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	if len(dags) != 1 || dags[0].DagKey != "dag-1" {
		t.Fatalf("ListDAGs() = %#v", dags)
	}
	assertDAGToolCall(t, caller, "task_list_dags", map[string]any{
		"status":  "running",
		"keyword": "daily",
		"limit":   float64(7),
	})
}

func TestMCPOrchDAGRuntimeStartDAGCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{result: `{"run_key":"dag-1#run-ui","version":3}`}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	resp, err := runtime.StartDAG(context.Background(), contract.StartDAGRequest{
		DagKey:         " dag-1 ",
		TriggerSource:  " manual ",
		IdempotencyKey: " ui-1 ",
	})
	if err != nil {
		t.Fatalf("StartDAG() error = %v", err)
	}
	if resp.RunKey != "dag-1#run-ui" || resp.Version != 3 {
		t.Fatalf("StartDAG() = %#v", resp)
	}
	assertDAGToolCall(t, caller, "task_start_dag", map[string]any{
		"dag_key":         "dag-1",
		"trigger_source":  "manual",
		"idempotency_key": "ui-1",
	})
}

func TestMCPOrchDAGRuntimeTerminateDAGCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{result: `{}`}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	err := runtime.TerminateDAG(context.Background(), contract.TerminateDAGRequest{
		DagKey: " dag-1 ",
		RunKey: " run-1 ",
		Reason: " user_requested ",
	})
	if err != nil {
		t.Fatalf("TerminateDAG() error = %v", err)
	}
	assertDAGToolCall(t, caller, "task_terminate_dag", map[string]any{
		"dag_key": "dag-1",
		"run_key": "run-1",
		"reason":  "user_requested",
	})
}

func TestMCPOrchDAGRuntimeApplyOpsCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{result: `{"new_version":12}`}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	resp, err := runtime.ApplyOps(context.Background(), contract.ApplyOpsRequest{
		DagKey:      " dag-1 ",
		BaseVersion: 11,
		Ops:         json.RawMessage(`[{"op":"update_node","node_key":"draft","patch":{"title":"Draft v2"}}]`),
	})
	if err != nil {
		t.Fatalf("ApplyOps() error = %v", err)
	}
	if resp.NewVersion != 12 {
		t.Fatalf("ApplyOps() = %#v", resp)
	}
	assertDAGToolCall(t, caller, "task_dag_apply_ops", map[string]any{
		"dag_key":      "dag-1",
		"base_version": float64(11),
	})
	ops, ok := caller.argument["ops"].([]any)
	if !ok || len(ops) != 1 {
		t.Fatalf("argument[ops] = %#v, want one op", caller.argument["ops"])
	}
	op, ok := ops[0].(map[string]any)
	if !ok {
		t.Fatalf("argument[ops][0] = %#v, want object", ops[0])
	}
	patch, ok := op["patch"].(map[string]any)
	if !ok {
		t.Fatalf("argument[ops][0].patch = %#v, want object", op["patch"])
	}
	if op["op"] != "update_node" || op["node_key"] != "draft" || patch["title"] != "Draft v2" {
		t.Fatalf("argument[ops][0] = %#v, want update_node draft title patch", op)
	}
}

func TestMCPOrchDAGRuntimeGetRunCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{
		result: `{"run":{"run_key":"dag-1#run-1","dag_key":"dag-1","status":"running"},"nodes":[{"node_key":"n1","status":"running","spawning_thread_id":"thread-child"}]}`,
	}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	resp, err := runtime.GetRun(context.Background(), contract.GetRunRequest{RunKey: " dag-1#run-1 "})
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if resp.Run.RunKey != "dag-1#run-1" {
		t.Fatalf("GetRun().Run = %#v", resp.Run)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].NodeKey != "n1" || resp.Nodes[0].SpawningThreadID == nil || *resp.Nodes[0].SpawningThreadID != "thread-child" {
		t.Fatalf("GetRun().Nodes = %#v", resp.Nodes)
	}
	assertDAGToolCall(t, caller, "task_get_run", map[string]any{
		"run_key": "dag-1#run-1",
	})
}

func TestMCPOrchDAGRuntimePropagatesPeerFailure(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{
		success:    false,
		successSet: true,
		result:     `{"success":false,"error":"no active peer"}`,
		text:       "no active peer",
	}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	_, err := runtime.GetDAG(context.Background(), "dag-1")
	if err == nil || !strings.Contains(err.Error(), "no active peer") {
		t.Fatalf("GetDAG() error = %v, want no active peer", err)
	}
}

type recordingDAGToolCaller struct {
	name       string
	argument   map[string]any
	result     string
	text       string
	success    bool
	successSet bool
}

func (c *recordingDAGToolCaller) HandleToolCall(_ context.Context, msg contract.ToolCallRawMessage) (any, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, err
	}
	c.name = params.Name
	if err := json.Unmarshal(params.Arguments, &c.argument); err != nil {
		return nil, err
	}
	success := true
	if c.successSet {
		success = c.success
	}
	text := c.text
	if text == "" {
		text = c.result
	}
	return &toolbridge.ToolCallResult{
		Success:           success,
		StructuredContent: json.RawMessage(c.result),
		ContentItems:      []toolbridge.ToolCallContentItem{{Type: "inputText", Text: text}},
	}, nil
}

func assertDAGToolCall(t *testing.T, caller *recordingDAGToolCaller, wantName string, wantArgs map[string]any) {
	t.Helper()
	if caller.name != wantName {
		t.Fatalf("tool name = %q, want %q", caller.name, wantName)
	}
	for key, want := range wantArgs {
		if caller.argument[key] != want {
			t.Fatalf("argument[%s] = %#v, want %#v; all args=%#v", key, caller.argument[key], want, caller.argument)
		}
	}
}
