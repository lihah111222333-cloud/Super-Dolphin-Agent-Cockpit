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

	caller := &recordingDAGToolCaller{result: `{"RunKey":"dag-1#run-ui","Version":3}`}
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
