package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestInterruptAgentSchemaSupportsPosAndAgentID(t *testing.T) {
	def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), "interrupt_agent")
	props := def.InputSchema["properties"].(map[string]any)
	for _, field := range []string{"pos", "agent_id", "source", "timeout_ms"} {
		if _, ok := props[field].(map[string]any); !ok {
			t.Fatalf("interrupt_agent schema properties = %#v, want %s", props, field)
		}
	}
}

func TestInterruptAgentReturnsStateResult(t *testing.T) {
	var gotAgentID, gotSource string
	handler := HandleInterruptAgent(&golden.OrchestrationStub{
		InterruptAgentFunc: func(_ context.Context, agentID string, source string) (contract.AgentStateResult, error) {
			gotAgentID = agentID
			gotSource = source
			return contract.AgentStateResult{AgentID: agentID, State: "idle"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"pos":"agent:agent-1","source":"manual","timeout_ms":100}`))
	if err != nil {
		t.Fatalf("HandleInterruptAgent() error = %v", err)
	}
	out, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleInterruptAgent() result = %T, want map", result)
	}
	if gotAgentID != "agent-1" || gotSource != "manual" || out["interrupted"] != true || out["state"] != "idle" {
		t.Fatalf("got agent=%q source=%q result=%#v", gotAgentID, gotSource, out)
	}
}

func TestInterruptAgentPropagatesServiceError(t *testing.T) {
	wantErr := errors.New("interrupt refused")
	handler := HandleInterruptAgent(&golden.OrchestrationStub{
		InterruptAgentFunc: func(context.Context, string, string) (contract.AgentStateResult, error) {
			return contract.AgentStateResult{}, wantErr
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1"}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleInterruptAgent() error = %v, want %v", err, wantErr)
	}
}
