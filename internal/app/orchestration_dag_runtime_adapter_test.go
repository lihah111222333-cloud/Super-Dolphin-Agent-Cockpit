package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
)

func TestMCPOrchDAGRuntimeListDAGsUsesActiveSharedOrchPeer(t *testing.T) {
	t.Parallel()

	peer := &recordingSharedOrchPeer{
		result: `{"dags":[{"dag_key":"dag-1","title":"Daily","status":"running"}]}`,
	}
	registry := &recordingSharedOrchRegistry{
		peer: &mcpcontrol.ToolInstance{
			ClientKind: mcpdto.ClientKindOrch,
			PeerKind:   mcpdto.PeerKindSharedService,
			Shared:     true,
			Status:     mcpdto.StatusActive,
			Peer:       peer,
		},
	}
	runtime := newMCPOrchDAGRuntime(toolbridge.NewHandlerForTesting(registry, nil))

	dags, err := runtime.ListDAGs(context.Background(), contract.ListDAGsFilter{Limit: 3})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	if len(dags) != 1 || dags[0].DagKey != "dag-1" {
		t.Fatalf("ListDAGs() = %#v", dags)
	}
	if len(registry.scopes) != 1 || registry.scopes[0].Family != mcpdto.ClientKindOrch {
		t.Fatalf("FindActiveForScope() scopes = %#v, want one orch scope", registry.scopes)
	}
	if peer.method != toolbridge.ProxyMethodToolsCall || peer.name != "task_list_dags" {
		t.Fatalf("peer callback = %s/%s, want tools/call task_list_dags", peer.method, peer.name)
	}
	if peer.arguments["limit"] != float64(3) {
		t.Fatalf("peer arguments = %#v, want limit 3", peer.arguments)
	}
}

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

func TestMCPOrchDAGRuntimeCreateDAGCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{result: `{"dag":{"dag_key":"dag-1","title":"Daily"},"nodes":[{"node_key":"draft","title":"Draft"}]}`}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	detail, err := runtime.CreateDAG(context.Background(), contract.CreateDAGRequest{
		DagKey:      " dag-1 ",
		Title:       " Daily ",
		Description: " Daily report ",
		CreatedBy:   " dashboard-ui ",
		Metadata:    json.RawMessage(`{"schedule":{"trigger":"manual"},"final_node_key":"final"}`),
		Nodes: []contract.CreateDAGNodeRequest{{
			NodeKey:    " draft ",
			Title:      " Draft ",
			NodeType:   " agent ",
			AssignedTo: " codex-runner ",
			DependsOn:  []string{"intake"},
			CommandRef: " prompt_list ",
			Config:     json.RawMessage(`{"prompt":"draft"}`),
		}},
	})
	if err != nil {
		t.Fatalf("CreateDAG() error = %v", err)
	}
	assertCreateDAGToolResult(t, detail)
	assertDAGToolCall(t, caller, "task_create_dag", map[string]any{
		"agent_id":       "dashboard-ui",
		"dag_key":        "dag-1",
		"title":          "Daily",
		"description":    "Daily report",
		"final_node_key": "final",
	})
	assertCreateDAGToolSchedule(t, caller)
	assertCreateDAGToolNodeArgs(t, caller)
}

func assertCreateDAGToolResult(t *testing.T, detail contract.DAGDetail) {
	t.Helper()
	if detail.DAG.DagKey != "dag-1" || len(detail.Nodes) != 1 {
		t.Fatalf("CreateDAG() = %#v", detail)
	}
}

func assertCreateDAGToolSchedule(t *testing.T, caller *recordingDAGToolCaller) {
	t.Helper()
	schedule, ok := caller.argument["schedule"].(map[string]any)
	if !ok || schedule["trigger"] != "manual" {
		t.Fatalf("argument[schedule] = %#v, want manual schedule", caller.argument["schedule"])
	}
}

func assertCreateDAGToolNodeArgs(t *testing.T, caller *recordingDAGToolCaller) {
	t.Helper()
	nodes, ok := caller.argument["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("argument[nodes] = %#v, want one node", caller.argument["nodes"])
	}
	node, ok := nodes[0].(map[string]any)
	if !ok {
		t.Fatalf("argument[nodes][0] = %#v, want object", nodes[0])
	}
	config, ok := node["config"].(map[string]any)
	if !ok || config["prompt"] != "draft" {
		t.Fatalf("argument[nodes][0].config = %#v, want prompt config", node["config"])
	}
	if node["node_key"] != "draft" || node["assigned_to"] != "codex-runner" || node["command_ref"] != "prompt_list" {
		t.Fatalf("argument[nodes][0] = %#v, want trimmed node fields", node)
	}
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

func TestMCPOrchDAGRuntimeDeleteDAGCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{result: `{}`}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	err := runtime.DeleteDAG(context.Background(), contract.DeleteDAGRequest{DagKey: " dag-1 "})
	if err != nil {
		t.Fatalf("DeleteDAG() error = %v", err)
	}
	assertDAGToolCall(t, caller, "task_delete_dag", map[string]any{
		"dag_key": "dag-1",
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

func TestMCPOrchDAGRuntimeDispatchNodeCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{result: `{"wakeup_id":99,"enqueued":true,"node":{"dag_key":"dag-1","node_key":"draft","assigned_to":"codex-runner"}}`}
	runtime := &mcpOrchDAGRuntime{tools: caller}
	dispatcher, ok := any(runtime).(interface {
		DispatchNode(context.Context, contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
	})
	if !ok {
		t.Fatal("mcpOrchDAGRuntime does not expose DispatchNode")
	}

	resp, err := dispatcher.DispatchNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     " dag-1 ",
		RunID:      88,
		NodeKey:    " draft ",
		AssignedTo: " codex-runner ",
	})
	if err != nil {
		t.Fatalf("DispatchNode() error = %v", err)
	}
	if !resp.Enqueued || resp.WakeupID != 99 || resp.Node.AssignedTo != "codex-runner" {
		t.Fatalf("DispatchNode() = %#v", resp)
	}
	assertDAGToolCall(t, caller, "task_dispatch_node", map[string]any{
		"dag_key":     "dag-1",
		"run_id":      float64(88),
		"node_key":    "draft",
		"assigned_to": "codex-runner",
	})
}

func TestMCPOrchDAGRuntimeGetRunCallsPeerTool(t *testing.T) {
	t.Parallel()

	caller := &recordingDAGToolCaller{
		result: `{"run":{"run_key":"dag-1#run-1","dag_key":"dag-1","status":"running","derived_state":"active","next_action":"monitor","artifact_count":1},"nodes":[{"node_key":"n1","status":"running","spawning_thread_id":"thread-child","executor":"codex-runner","artifact_links":[{"kind":"sharedfile","path":"reports/out.md"}]}]}`,
	}
	runtime := &mcpOrchDAGRuntime{tools: caller}

	resp, err := runtime.GetRun(context.Background(), contract.GetRunRequest{RunKey: " dag-1#run-1 "})
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	assertGetRunWorkbenchSummary(t, resp)
	assertDAGToolCall(t, caller, "task_get_run", map[string]any{
		"run_key": "dag-1#run-1",
	})
}

func assertGetRunWorkbenchSummary(t *testing.T, resp contract.GetRunResponse) {
	t.Helper()
	assertGetRunDerivedSummary(t, resp.Run)
	assertGetRunNodeDiagnostics(t, resp.Nodes)
}

func assertGetRunDerivedSummary(t *testing.T, run contract.Run) {
	t.Helper()
	if run.RunKey != "dag-1#run-1" {
		t.Fatalf("GetRun().Run = %#v", run)
	}
	if run.DerivedState != "active" || run.NextAction != "monitor" || run.ArtifactCount != 1 {
		t.Fatalf("GetRun().Run derived summary = %#v", run)
	}
}

func assertGetRunNodeDiagnostics(t *testing.T, nodes []contract.DAGNode) {
	t.Helper()
	if len(nodes) != 1 || nodes[0].NodeKey != "n1" || nodes[0].SpawningThreadID == nil || *nodes[0].SpawningThreadID != "thread-child" {
		t.Fatalf("GetRun().Nodes = %#v", nodes)
	}
	if nodes[0].Executor != "codex-runner" || len(nodes[0].ArtifactLinks) != 1 || nodes[0].ArtifactLinks[0].Path != "reports/out.md" {
		t.Fatalf("GetRun().Nodes diagnostics = %#v", nodes)
	}
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

func TestMCPOrchDAGRuntimeWaitsForPeerReady(t *testing.T) {
	caller := &recordingDAGToolCaller{
		result:     `{"dags":[{"dag_key":"dag-1","title":"Daily","status":"running"}]}`,
		callErrors: []error{toolbridge.ErrNoPeerAvailable, toolbridge.ErrNoPeerAvailable},
	}
	runtime := newDAGRuntimeWithPeerWaitForTest(caller, 200*time.Millisecond, time.Millisecond)

	dags, err := runtime.ListDAGs(context.Background(), contract.ListDAGsFilter{Limit: 7})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	if len(dags) != 1 || dags[0].DagKey != "dag-1" {
		t.Fatalf("ListDAGs() = %#v", dags)
	}
	if caller.calls != 3 {
		t.Fatalf("HandleToolCall calls = %d, want 3", caller.calls)
	}
	assertDAGToolCall(t, caller, "task_list_dags", map[string]any{"limit": float64(7)})
}

func TestMCPOrchDAGRuntimePeerUnavailableErrorIsActionable(t *testing.T) {
	caller := &recordingDAGToolCaller{err: toolbridge.ErrNoPeerAvailable}
	runtime := newDAGRuntimeWithPeerWaitForTest(caller, 3*time.Millisecond, time.Millisecond)

	_, err := runtime.ListDAGs(context.Background(), contract.ListDAGsFilter{Limit: 7})
	if !errors.Is(err, toolbridge.ErrNoPeerAvailable) {
		t.Fatalf("ListDAGs() error = %v, want ErrNoPeerAvailable", err)
	}
	for _, want := range []string{"mcp-orch peer not ready", "task_list_dags"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ListDAGs() error = %q, want substring %q", err.Error(), want)
		}
	}
}

type recordingSharedOrchRegistry struct {
	peer   *mcpcontrol.ToolInstance
	scopes []mcpcontrol.ToolScope
}

func (r *recordingSharedOrchRegistry) FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance {
	if r.peer != nil && r.peer.ClientKind == clientKind && r.peer.Status == mcpdto.StatusActive {
		return []*mcpcontrol.ToolInstance{r.peer}
	}
	return nil
}

func (r *recordingSharedOrchRegistry) FindActiveForScope(scope mcpcontrol.ToolScope) []*mcpcontrol.ToolInstance {
	r.scopes = append(r.scopes, scope)
	if r.peer == nil ||
		r.peer.ClientKind != scope.Family ||
		r.peer.Status != mcpdto.StatusActive ||
		!r.peer.Shared ||
		r.peer.PeerKind != mcpdto.PeerKindSharedService {
		return nil
	}
	return []*mcpcontrol.ToolInstance{r.peer}
}

type recordingSharedOrchPeer struct {
	method    string
	name      string
	arguments map[string]any
	result    string
}

func (p *recordingSharedOrchPeer) Notify(context.Context, string, any) error { return nil }

func (p *recordingSharedOrchPeer) Callback(_ context.Context, method string, params any, result any) error {
	p.method = method
	payload, ok := params.(map[string]any)
	if !ok {
		return fmt.Errorf("params type = %T, want map[string]any", params)
	}
	p.name, _ = payload["name"].(string)
	rawArgs, ok := payload["arguments"].(json.RawMessage)
	if !ok {
		return fmt.Errorf("arguments type = %T, want json.RawMessage", payload["arguments"])
	}
	if err := json.Unmarshal(rawArgs, &p.arguments); err != nil {
		return err
	}
	raw, err := json.Marshal(map[string]any{
		"content":           []map[string]string{{"type": "text", "text": p.result}},
		"structuredContent": json.RawMessage(p.result),
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

func (p *recordingSharedOrchPeer) Close() error { return nil }

type recordingDAGToolCaller struct {
	name       string
	argument   map[string]any
	result     string
	text       string
	success    bool
	successSet bool
	err        error
	callErrors []error
	calls      int
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
	c.calls++
	if len(c.callErrors) > 0 {
		err := c.callErrors[0]
		c.callErrors = c.callErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	if c.err != nil {
		return nil, c.err
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

func newDAGRuntimeWithPeerWaitForTest(caller dagToolCaller, timeout, interval time.Duration) *mcpOrchDAGRuntime {
	return &mcpOrchDAGRuntime{
		tools:             caller,
		peerReadyTimeout:  timeout,
		peerReadyInterval: interval,
	}
}
