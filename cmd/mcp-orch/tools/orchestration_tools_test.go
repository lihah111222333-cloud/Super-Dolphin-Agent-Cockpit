package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestLaunchRequestFromExecutableRejectsStandaloneMCPOrch(t *testing.T) {
	tests := []string{
		"/tmp/mcp-orch",
		`C:\agent\mcp-orch.exe`,
	}
	for _, exe := range tests {
		t.Run(exe, func(t *testing.T) {
			_, err := launchRequestFromExecutable(LaunchAgentInput{Name: "agent-1"}, exe)
			if err == nil || err.Error() != standaloneMCPOrchLaunchError {
				t.Fatalf("launchRequestFromExecutable() error = %v, want %q", err, standaloneMCPOrchLaunchError)
			}
		})
	}
}

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

func TestLaunchHandlerRejectsStandaloneMCPOrchExecutable(t *testing.T) {
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	called := false
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		LaunchAgentFunc: func(context.Context, contract.LaunchRequest) error {
			called = true
			return nil
		},
	})

	input, err := json.Marshal(LaunchAgentInput{Name: "agent-1"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	_, err = handler(context.Background(), input)
	if err == nil {
		t.Fatal("HandleLaunchAgent() error = nil, want unsupported standalone mcp-orch error")
	}
	if !strings.Contains(err.Error(), "not supported in standalone mcp-orch mode") {
		t.Fatalf("HandleLaunchAgent() error = %q, want unsupported standalone mcp-orch message", err.Error())
	}
	if called {
		t.Fatal("HandleLaunchAgent() called LaunchAgent on the orchestration service despite fail-fast guard")
	}
}
