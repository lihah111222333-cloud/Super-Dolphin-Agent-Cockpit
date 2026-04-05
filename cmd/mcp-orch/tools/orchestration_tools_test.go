package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestLaunchRequestFromExecutableBuildsLaunchRequest(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     " agent-1 ",
		Prompt:   " hello ",
		CWD:      " /tmp/work ",
		Provider: " codex ",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	if req.AgentID != "agent-1" || req.Name != "agent-1" {
		t.Fatalf("launch request IDs = (%q, %q), want agent-1", req.AgentID, req.Name)
	}
	if req.Prompt != "hello" || req.Cwd != "/tmp/work" {
		t.Fatalf("launch request prompt/cwd = (%q, %q)", req.Prompt, req.Cwd)
	}
	if len(req.Command) != 1 || req.Command[0] != "/tmp/agent-terminal" {
		t.Fatalf("launch request command = %#v, want [/tmp/agent-terminal]", req.Command)
	}
	if len(req.Env) != 1 || req.Env[0] != "AGENT_PROVIDER=codex" {
		t.Fatalf("launch request env = %#v, want [AGENT_PROVIDER=codex]", req.Env)
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
		Name:     "agent-1",
		Prompt:   "hello",
		CWD:      "/tmp/work",
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
	if resultMap["success"] != true {
		t.Fatalf("HandleLaunchAgent() result = %#v, want success", resultMap)
	}
	if resultMap["status"] != "launching" {
		t.Fatalf("HandleLaunchAgent() status = %v, want launching", resultMap["status"])
	}

	// Wait for the async goroutine to call LaunchAgent.
	select {
	case got := <-done:
		if got.AgentID != "agent-1" || got.Name != "agent-1" {
			t.Fatalf("launch request IDs = (%q, %q), want agent-1", got.AgentID, got.Name)
		}
		if got.Prompt != "hello" || got.Cwd != "/tmp/work" {
			t.Fatalf("launch request prompt/cwd = (%q, %q), want (hello, /tmp/work)", got.Prompt, got.Cwd)
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
