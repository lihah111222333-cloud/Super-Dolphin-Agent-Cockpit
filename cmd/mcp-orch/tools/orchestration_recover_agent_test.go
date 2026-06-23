package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestRecoverAgentSchemaSupportsPosAndAgentID(t *testing.T) {
	def := mustFindToolDefinition(t, orchestrationToolDefinitions(nil), "recover_agent")
	props := def.InputSchema["properties"].(map[string]any)
	if _, ok := props["pos"].(map[string]any); !ok {
		t.Fatalf("recover_agent schema properties = %#v, want pos", props)
	}
	if _, ok := props["agent_id"].(map[string]any); !ok {
		t.Fatalf("recover_agent schema properties = %#v, want agent_id", props)
	}
	required, _ := def.InputSchema["required"].([]string)
	for _, field := range required {
		if field == "pos" || field == "agent_id" {
			t.Fatalf("recover_agent required = %#v, want pos/agent_id optional pair", required)
		}
	}
}

func TestRecoverAgentReturnsSnapshotAfterRecover(t *testing.T) {
	for _, state := range []string{"stopped", "failed"} {
		t.Run(state, func(t *testing.T) {
			assertRecoverAgentReturnsSnapshotAfterRecover(t, state)
		})
	}
}

func assertRecoverAgentReturnsSnapshotAfterRecover(t *testing.T, state string) {
	t.Helper()
	recovered := false
	handler := HandleRecoverAgent(recoverSnapshotStub(t, state, &recovered))

	result, err := handler(context.Background(), json.RawMessage(`{"pos":"agent:agent-1"}`))
	if err != nil {
		t.Fatalf("HandleRecoverAgent() error = %v", err)
	}
	snapshot := requireAgentSnapshotResult(t, result)
	if snapshot.AgentID != "agent-1" || snapshot.State != "idle" || snapshot.ThreadID != "thread-1" || snapshot.Cwd != "/repo" {
		t.Fatalf("HandleRecoverAgent() snapshot = %#v", snapshot)
	}
	if !recovered {
		t.Fatal("Recover was not called")
	}
}

func recoverSnapshotStub(t *testing.T, state string, recovered *bool) *golden.OrchestrationStub {
	t.Helper()
	return &golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			if agentID != "agent-1" {
				t.Fatalf("Snapshot agentID = %q, want agent-1", agentID)
			}
			if *recovered {
				return contract.AgentSnapshot{AgentID: "agent-1", State: "idle", ThreadID: "thread-1", Cwd: "/repo"}, nil
			}
			return contract.AgentSnapshot{AgentID: "agent-1", State: state, ThreadID: "thread-1", Cwd: "/repo"}, nil
		},
		RecoverFunc: func(_ context.Context, agentID string) error {
			if agentID != "agent-1" {
				t.Fatalf("Recover agentID = %q, want agent-1", agentID)
			}
			*recovered = true
			return nil
		},
	}
}

func requireAgentSnapshotResult(t *testing.T, result any) contract.AgentSnapshot {
	t.Helper()
	snapshot, ok := result.(contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleRecoverAgent() result = %T, want AgentSnapshot", result)
	}
	return snapshot
}

func TestRecoverAgentRejectsActiveAgent(t *testing.T) {
	activeStates := []string{"idle", "turn_running", "turn_starting", "turn_queued", "awaiting_user_input", "recovering"}
	for _, state := range activeStates {
		t.Run(state, func(t *testing.T) {
			recoverCalled := false
			handler := HandleRecoverAgent(&golden.OrchestrationStub{
				SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
					return contract.AgentSnapshot{AgentID: agentID, State: state}, nil
				},
				RecoverFunc: func(context.Context, string) error {
					recoverCalled = true
					return nil
				},
			})

			_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1"}`))
			if err == nil {
				t.Fatal("HandleRecoverAgent() error = nil, want active-agent rejection")
			}
			if !strings.Contains(err.Error(), state) {
				t.Fatalf("HandleRecoverAgent() error = %v, want state %q", err, state)
			}
			if recoverCalled {
				t.Fatal("Recover must not be called for active agent")
			}
		})
	}
}

func TestRecoverAgentPropagatesRecoverError(t *testing.T) {
	wantErr := errors.New("recover backend down")
	handler := HandleRecoverAgent(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: agentID, State: "failed"}, nil
		},
		RecoverFunc: func(context.Context, string) error {
			return wantErr
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1"}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("HandleRecoverAgent() error = %v, want %v", err, wantErr)
	}
}
