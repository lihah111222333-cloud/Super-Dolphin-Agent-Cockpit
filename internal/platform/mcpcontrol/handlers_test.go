package mcpcontrol

import (
	"context"
	"encoding/json"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestRegistryContextProvider_UsesRequestedAgentIDHint(t *testing.T) {
	resp, err := (registryContextProvider{}).GetContext(context.Background(), &ToolInstance{
		AgentID:    "shared",
		BinaryName: "go-agent-mcp-orch",
		ClientKind: "orch",
		PeerKind:   dto.PeerKindTool,
		PID:        99,
		Status:     dto.StatusActive,
	}, dto.ContextRequest{
		AgentID: "agent-42",
		Scope:   dto.ScopeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "agent-42" {
		t.Fatalf("payload.agent_id = %#v, want agent-42", got)
	}
}

func TestRegistryContextProvider_UsesLeaseScopedAgentIDWhenHintMissing(t *testing.T) {
	resp, err := (registryContextProvider{}).GetContext(context.Background(), &ToolInstance{
		AgentID:    "lease-agent",
		BinaryName: "go-agent-mcp-orch",
		ClientKind: "orch",
		PeerKind:   dto.PeerKindTool,
		PID:        99,
		Status:     dto.StatusActive,
	}, dto.ContextRequest{
		Scope: dto.ScopeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(resp.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := payload["agent_id"]; got != "lease-agent" {
		t.Fatalf("payload.agent_id = %#v, want lease-agent", got)
	}
}
