package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestLaunchRequestFromExecutableBuildsLaunchRequest(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		AgentID:     " agent-persist-1 ",
		Name:        " agent-1 ",
		Prompt:      " hello ",
		ParentID:    " agent-root ",
		AgentType:   " worker ",
		MemoryScope: " local ",
		CWD:         " /tmp/work ",
		Provider:    " codex ",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	if req.AgentID != "agent-persist-1" || req.Name != "agent-1" {
		t.Fatalf("launch request identity = agent_id %q name %q, want agent-persist-1 / agent-1", req.AgentID, req.Name)
	}
	if req.Prompt != "hello" || req.Cwd != "/tmp/work" {
		t.Fatalf("launch request prompt/cwd = (%q, %q)", req.Prompt, req.Cwd)
	}
	if req.ParentID != "agent-root" || req.AgentType != "worker" || req.MemoryScope != "local" {
		t.Fatalf("launch request metadata = %#v", req)
	}
	if len(req.Command) != 1 || req.Command[0] != "/tmp/agent-terminal" {
		t.Fatalf("launch request command = %#v, want [/tmp/agent-terminal]", req.Command)
	}
	if len(req.Env) != 1 || req.Env[0] != "AGENT_PROVIDER=codex" {
		t.Fatalf("launch request env = %#v, want [AGENT_PROVIDER=codex]", req.Env)
	}
}

func TestNamePolicyLaunchRequestNameAndPromptAreIndependent(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:   " dag-runtime-audit ",
		Prompt: "调研任务：定位 DAG runtime 路径",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	if req.AgentID == "dag-runtime-audit" || !strings.HasPrefix(req.AgentID, "agent_") || req.Name != "dag-runtime-audit" {
		t.Fatalf("launch request identity = agent_id %q name %q, want generated agent_ id plus explicit display name", req.AgentID, req.Name)
	}
	if req.Prompt != "调研任务：定位 DAG runtime 路径" {
		t.Fatalf("launch request prompt = %q, want prompt preserved separately", req.Prompt)
	}
}

func TestLaunchRequestFromExecutableForwardsModel(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     "agent-m",
		Provider: "claude",
		Model:    " claude-opus-4-7[1m] ",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	want := map[string]bool{
		"AGENT_PROVIDER=claude":           true,
		"AGENT_MODEL=claude-opus-4-7[1m]": true,
	}
	if len(req.Env) != len(want) {
		t.Fatalf("launch request env = %#v, want %v", req.Env, want)
	}
	for _, entry := range req.Env {
		if !want[entry] {
			t.Fatalf("unexpected env entry %q; full env = %#v", entry, req.Env)
		}
	}
}

func TestLaunchRequestFromExecutableOmitsEmptyModel(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     "agent-n",
		Provider: "claude",
		Model:    "   ",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	if len(req.Env) != 1 || req.Env[0] != "AGENT_PROVIDER=claude" {
		t.Fatalf("launch request env = %#v, want only [AGENT_PROVIDER=claude]", req.Env)
	}
}

func TestLaunchHandlerAllowsMCPOrchExecutable(t *testing.T) {
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	done := make(chan contract.LaunchRequest, 1)
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		LaunchAgentFunc: func(_ context.Context, req contract.LaunchRequest) error {
			done <- req
			return nil
		},
	})

	input, err := json.Marshal(LaunchAgentInput{
		AgentID:     "agent-persist-1",
		Name:        "agent-1",
		Prompt:      "hello",
		ParentID:    "agent-root",
		AgentType:   "worker",
		MemoryScope: "project",
		CWD:         "/tmp/work",
		Provider:    "codex",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	result, err := handler(context.Background(), input)
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", result)
	}
	if resultMap["success"] != true {
		t.Fatalf("HandleLaunchAgent() result = %#v, want success", resultMap)
	}
	if resultMap["status"] != "launching" {
		t.Fatalf("HandleLaunchAgent() status = %v, want launching", resultMap["status"])
	}

	// Wait for the async goroutine to call LaunchAgent.
	select {
	case got := <-done:
		if got.AgentID != "agent-persist-1" || got.Name != "agent-1" {
			t.Fatalf("launch request identity = agent_id %q name %q, want agent-persist-1 / agent-1", got.AgentID, got.Name)
		}
		if got.Prompt != "hello" || got.Cwd != "/tmp/work" {
			t.Fatalf("launch request prompt/cwd = (%q, %q), want (hello, /tmp/work)", got.Prompt, got.Cwd)
		}
		if got.ParentID != "agent-root" || got.AgentType != "worker" || got.MemoryScope != "project" {
			t.Fatalf("launch request metadata = %#v", got)
		}
		if len(got.Command) != 1 || got.Command[0] != "/tmp/mcp-orch" {
			t.Fatalf("launch request command = %#v, want [/tmp/mcp-orch]", got.Command)
		}
		if len(got.Env) != 1 || got.Env[0] != "AGENT_PROVIDER=codex" {
			t.Fatalf("launch request env = %#v, want [AGENT_PROVIDER=codex]", got.Env)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async LaunchAgent was not called within 5s")
	}
}

func TestLaunchHandlerReassignsDuplicateAgentIDBeforeAsyncLaunch(t *testing.T) {
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	done := make(chan contract.LaunchRequest, 1)
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-dup", AgentID: "agent-dup"}}, nil
		},
		LaunchAgentFunc: func(_ context.Context, req contract.LaunchRequest) error {
			done <- req
			return nil
		},
	})

	input, err := json.Marshal(LaunchAgentInput{
		AgentID:  "agent-dup",
		Name:     "worker",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := handler(context.Background(), input)
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", result)
	}
	returnedID, _ := resultMap["agent_id"].(string)
	if returnedID == "" || returnedID == "agent-dup" || !strings.HasPrefix(returnedID, "agent_") {
		t.Fatalf("returned agent_id = %q, want reassigned generated id", returnedID)
	}
	select {
	case got := <-done:
		if got.AgentID != returnedID {
			t.Fatalf("async launch AgentID = %q, want returned id %q", got.AgentID, returnedID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async LaunchAgent was not called within 5s")
	}
}

func TestLaunchHandlerReturnsFinalPersistedAgentID(t *testing.T) {
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{
				ID:       req.AgentID,
				AgentID:  "agent-final",
				ThreadID: "thread-final",
				State:    "idle",
			}, nil
		},
	})

	input, err := json.Marshal(LaunchAgentInput{
		AgentID:  "agent-requested",
		Name:     "worker",
		Provider: "codex",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	result, err := handler(context.Background(), input)
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", result)
	}
	if resultMap["agent_id"] != "agent-final" {
		t.Fatalf("returned agent_id = %v, want final persisted id", resultMap["agent_id"])
	}
	if resultMap["launch_id"] != "agent-requested" {
		t.Fatalf("returned launch_id = %v, want original reserved runtime id", resultMap["launch_id"])
	}
	if resultMap["thread_id"] != "thread-final" {
		t.Fatalf("returned thread_id = %v, want thread-final", resultMap["thread_id"])
	}
}
