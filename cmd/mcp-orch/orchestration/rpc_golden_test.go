package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	rpcpkg "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	goldentest "github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestOrchestrationGoldenTurnAgentSamples(t *testing.T) {
	t.Parallel()

	var launchReq contract.LaunchRequest
	var submitReq contract.TurnSubmission
	svc := &goldentest.OrchestrationStub{
		LaunchAgentFunc: func(_ context.Context, req contract.LaunchRequest) error {
			launchReq = req
			return nil
		},
		SubmitTurnFunc: func(_ context.Context, req contract.TurnSubmission) error {
			submitReq = req
			return nil
		},
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{ID: agentID, ThreadID: "thread-submit-1"}, nil
		},
	}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &config.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(ProvideRPCFacade(testRPCFacadeParams(svc)).Handlers)

	launchRequest := map[string]any{
		"id":          "agent-launch-1",
		"name":        "guard-launch-agent",
		"prompt":      "launch golden agent",
		"cwd":         "/tmp/agent-launch",
		"command":     []string{"codex", "serve"},
		"env":         map[string]string{"PROVIDER": "codex", "SANDBOX": "workspace-write"},
		"parentId":    "parent-launch-1",
		"agentType":   "worker",
		"memoryScope": "project",
	}
	launchResponse := dispatchJSON(t, server, "agent/launch", launchRequest)
	assertLaunchRequest(t, launchReq)
	assertLaunchResponse(t, launchResponse)

	submitRequest := map[string]any{
		"agent_id": "agent-submit-1",
		"prompt":   "trace submit prompt",
		"images":   []string{"diagram.png"},
		"files":    []string{"app://workspace/docs/notes.txt"},
	}
	submitResponse := dispatchJSON(t, server, "agent/submit", submitRequest)
	assertSubmitRequest(t, submitReq)

	goldentest.AssertJSON(t, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainTurnAgent,
		Name:    "rpc_samples",
	}, map[string]any{
		"agent/launch": map[string]any{
			"method":   "agent/launch",
			"request":  launchRequest,
			"response": launchResponse,
			"v2_reference": map[string]any{
				"path": "internal/guards/golden/rpc_response/agent_launch.golden.json",
				"result": map[string]any{
					"agent_id": "agent-launch-1",
					"name":     "guard-launch-agent",
					"status":   "running",
				},
			},
		},
		"agent/submit": map[string]any{
			"method":   "agent/submit",
			"request":  submitRequest,
			"response": submitResponse,
			"v2_reference": map[string]any{
				"path": "internal/guards/golden/rpc_response/agent_submit.golden.json",
				"result": map[string]any{
					"success": true,
				},
			},
		},
	})
}

func assertLaunchResponse(t *testing.T, response any) {
	t.Helper()

	got, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("launch response = %T, want object", response)
	}
	if got["success"] != true || got["agent_id"] != "agent-launch-1" || got["status"] != "running" {
		t.Fatalf("launch response = %#v, want success agent-launch-1 running", got)
	}
}

func dispatchJSON(t *testing.T, server *rpcpkg.Server, method string, request any) any {
	t.Helper()

	params, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal(%s request) error = %v", method, err)
	}
	raw, err := server.Dispatch(context.Background(), method, params)
	if err != nil {
		t.Fatalf("Dispatch(%s) error = %v", method, err)
	}
	return decodeJSONValue(t, raw)
}

func decodeJSONValue(t *testing.T, raw []byte) any {
	t.Helper()

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("json.Unmarshal(response) error = %v", err)
	}
	return value
}

func assertLaunchRequest(t *testing.T, req contract.LaunchRequest) {
	t.Helper()

	assertLaunchRequestIdentity(t, req)
	assertLaunchRequestRouting(t, req)
	assertLaunchRequestCommand(t, req)
	assertLaunchRequestEnv(t, req)
}

func assertLaunchRequestIdentity(t *testing.T, req contract.LaunchRequest) {
	t.Helper()
	if req.AgentID != "agent-launch-1" || req.ParentID != "parent-launch-1" {
		t.Fatalf("launch request ids = %#v", req)
	}
	if req.AgentType != "worker" || req.MemoryScope != "project" {
		t.Fatalf("launch request metadata = %#v", req)
	}
}

func assertLaunchRequestRouting(t *testing.T, req contract.LaunchRequest) {
	t.Helper()
	if req.Name != "guard-launch-agent" || req.Cwd != "/tmp/agent-launch" {
		t.Fatalf("launch request routing = %#v", req)
	}
}

func assertLaunchRequestCommand(t *testing.T, req contract.LaunchRequest) {
	t.Helper()
	if len(req.Command) != 2 || req.Command[0] != "codex" {
		t.Fatalf("launch command = %#v", req.Command)
	}
}

func assertLaunchRequestEnv(t *testing.T, req contract.LaunchRequest) {
	t.Helper()
	if len(req.Env) != 2 || req.Env[0] != "PROVIDER=codex" || req.Env[1] != "SANDBOX=workspace-write" {
		t.Fatalf("launch env = %#v", req.Env)
	}
}

func assertSubmitRequest(t *testing.T, req contract.TurnSubmission) {
	t.Helper()

	if req.AgentID != "agent-submit-1" || req.ThreadID != "thread-submit-1" {
		t.Fatalf("submit routing = %#v", req)
	}
	if len(req.Inputs) != 3 {
		t.Fatalf("submit inputs = %#v", req.Inputs)
	}
	if req.Inputs[0].Type != "text" || req.Inputs[1].Type != "image" || req.Inputs[2].Type != "mention" {
		t.Fatalf("submit input types = %#v", req.Inputs)
	}
}
