package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
	"github.com/stretchr/testify/require"
)

func mockExe() func() (string, error) {
	return func() (string, error) { return "/tmp/mcp-orch", nil }
}

func TestLaunchHandlerAllowsMCPOrchExecutable(t *testing.T) {
	done := make(chan contract.LaunchRequest, 1)
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		LaunchAgentFunc: func(_ context.Context, req contract.LaunchRequest) error {
			done <- req
			return nil
		},
	}, mockExe())

	input, err := json.Marshal(LaunchAgentInput{
		AgentID:     "agent-persist-1",
		Name:        "agent-1",
		Prompt:      "hello",
		ParentID:    "agent-root",
		AgentType:   "worker",
		PromptKey:   "main/sql",
		MemoryScope: "project",
		CWD:         "/tmp/work",
		Provider:    "codex",
	})
	require.NoError(t, err)

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.Truef(t, ok, "HandleLaunchAgent() result type = %T, want map[string]any", result)
	require.Equal(t, true, resultMap["success"])
	require.Equal(t, "launching", resultMap["status"])

	select {
	case got := <-done:
		require.Equal(t, "agent-persist-1", got.AgentID)
		require.Equal(t, "agent-1", got.Name)
		require.Equal(t, "hello", got.Prompt)
		require.Equal(t, "/tmp/work", got.Cwd)
		require.Equal(t, "agent-root", got.ParentID)
		require.Equal(t, "worker", got.AgentType)
		require.Equal(t, "main/sql", got.PromptKey)
		require.Equal(t, "project", got.MemoryScope)
		require.Equal(t, []string{"/tmp/mcp-orch"}, got.Command)
		require.Equal(t, "codex", launchEnvValue(got.Env, "AGENT_PROVIDER"))
		require.Equal(t, expectedLaunchAgentDefaultDisabledTools(t), launchEnvValue(got.Env, "AGENT_DISABLED_TOOLS"))
		require.Equal(t, "spawn_agent", launchEnvValue(got.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
	case <-time.After(5 * time.Second):
		t.Fatal("async LaunchAgent was not called within 5s")
	}
}

func TestLaunchHandlerAllowsRootAgentDelegation(t *testing.T) {
	launched := false
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			require.Equal(t, "agent-root", agentID)
			return contract.AgentSnapshot{ID: "agent-root", AgentID: "agent-root"}, nil
		},
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			launched = true
			return contract.AgentSnapshot{ID: req.AgentID, AgentID: req.AgentID, State: "idle"}, nil
		},
	}, mockExe())

	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "agent-root"})
	_, err := handler(ctx, json.RawMessage(`{"name":"child","cwd":"/tmp/work","provider":"codex"}`))
	require.NoError(t, err)
	require.True(t, launched)
}

func TestLaunchHandlerAllowsPersistedRootWhenDirectSnapshotMisses(t *testing.T) {
	launched := false
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			require.Equal(t, "agent-root", agentID)
			return contract.AgentSnapshot{}, fmt.Errorf("lookup root: %w", contract.ErrAgentNotFound)
		},
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-root", AgentID: "agent-root"}}, nil
		},
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			launched = true
			return contract.AgentSnapshot{ID: req.AgentID, AgentID: req.AgentID, State: "idle"}, nil
		},
	}, mockExe())

	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "agent-root"})
	_, err := handler(ctx, json.RawMessage(`{"name":"child","cwd":"/tmp/work","provider":"codex"}`))
	require.NoError(t, err)
	require.True(t, launched)
}

func TestLaunchHandlerRejectsChildAgentDelegation(t *testing.T) {
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			require.Equal(t, "agent-child", agentID)
			return contract.AgentSnapshot{ID: "agent-child", AgentID: "agent-child", ParentID: "agent-root"}, nil
		},
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			t.Fatal("ListAgents should not be called after child delegation is rejected")
			return nil, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called after child delegation is rejected")
			return contract.AgentSnapshot{}, nil
		},
	}, mockExe())

	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{AgentID: "agent-child"})
	_, err := handler(ctx, json.RawMessage(`{"name":"grandchild","cwd":"/tmp/work","provider":"codex"}`))
	require.ErrorContains(t, err, "Sub-agents are not allowed to spawn further agents")
}

func TestRegistryLegacyLaunchAliasIsNotRegistered(t *testing.T) {
	registry := NewRegistry(Dependencies{})

	_, ok := registry.Lookup("orchestration_launch_agent")
	require.False(t, ok)
}

func TestLaunchHandlerRejectsClaudeChildAgent(t *testing.T) {
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called for unsupported Claude child agent")
			return contract.AgentSnapshot{}, nil
		},
	}, mockExe())

	_, err := handler(context.Background(), json.RawMessage(`{"name":"child","parent_id":"agent-parent","provider":"claude"}`))
	require.ErrorContains(t, err, "Claude sub-agent orchestration is not supported")
	require.ErrorContains(t, err, "provider=codex")
}

func TestLaunchHandlerReturnsExistingDuplicateAgentID(t *testing.T) {
	launchCalls := 0
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-dup", AgentID: "agent-dup", ThreadID: "thread-dup", State: "turn_running"}}, nil
		},
		LaunchAgentFunc: func(_ context.Context, req contract.LaunchRequest) error {
			launchCalls++
			return nil
		},
	}, mockExe())

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
	if returnedID != "agent-dup" || resultMap["status"] != "existing" || resultMap["thread_id"] != "thread-dup" {
		t.Fatalf("result = %#v, want existing agent-dup", resultMap)
	}
	if launchCalls != 0 {
		t.Fatalf("LaunchAgent calls = %d, want 0 for duplicate explicit agent_id", launchCalls)
	}
}

func TestLaunchHandlerReturnsExistingIdleExplicitAgentID(t *testing.T) {
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-idle", AgentID: "agent-idle", ThreadID: "thread-idle", State: "idle"}}, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			t.Fatal("LaunchAgentSnapshot should not be called for existing idle explicit agent_id")
			return contract.AgentSnapshot{}, nil
		},
	})
	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-idle","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", result)
	}
	if got["agent_id"] != "agent-idle" || got["status"] != "existing" || got["thread_id"] != "thread-idle" {
		t.Fatalf("HandleLaunchAgent() result = %#v, want existing idle agent", got)
	}
}

func TestLaunchHandlerRetriesInactiveExplicitAgentIDWithoutReassigning(t *testing.T) {
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-retry", AgentID: "agent-retry", State: "failed"}}, nil
		},
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{ID: "agent-retry", AgentID: "agent-retry", State: "turn_running"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-retry","name":"worker","cwd":"/tmp/work","prompt":"retry"}`))
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", result)
	}
	if got["agent_id"] != "agent-retry" || got["status"] != "launching" {
		t.Fatalf("HandleLaunchAgent() result = %#v, want retry with explicit agent id", got)
	}
}

func TestLaunchHandlerLeavesCwdEmptyWhenOnlyParentIDProvided(t *testing.T) {
	var captured contract.LaunchRequest
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			captured = req
			return contract.AgentSnapshot{ID: req.AgentID, AgentID: req.AgentID, State: "launching"}, nil
		},
	}, mockExe())

	_, err := handler(context.Background(), json.RawMessage(`{"name":"child","parent_id":"parent-1","provider":"codex"}`))
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	if captured.ParentID != "parent-1" {
		t.Fatalf("captured ParentID = %q, want parent-1", captured.ParentID)
	}
	if captured.Cwd != "" {
		t.Fatalf("captured Cwd = %q, want empty so service resolves parent cwd", captured.Cwd)
	}
}

func TestLaunchHandlerReturnsFinalPersistedAgentID(t *testing.T) {
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{
				ID:       req.AgentID,
				AgentID:  "agent-final",
				ThreadID: "thread-final",
				State:    "idle",
			}, nil
		},
	}, mockExe())

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

func TestLaunchAgentCWDDescriptionDocumentsConditionalRequirement(t *testing.T) {
	var found ToolDefinition
	for _, tool := range orchestrationToolDefinitions(ToolPorts{}) {
		if tool.Name == "launch_agent" {
			found = tool
			break
		}
	}
	if found.Name == "" {
		t.Fatal("launch_agent tool definition not found")
	}
	properties, ok := found.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties schema = %T, want map[string]any", found.InputSchema["properties"])
	}
	cwd, ok := properties["cwd"].(map[string]any)
	if !ok {
		t.Fatalf("cwd schema = %T, want map[string]any", properties["cwd"])
	}
	description, _ := cwd["description"].(string)
	lower := strings.ToLower(description)
	if !strings.Contains(lower, "parent_id") || !strings.Contains(lower, "otherwise required") || !strings.Contains(lower, "absolute") {
		t.Fatalf("cwd description = %q, want parent_id, otherwise required, and absolute", description)
	}
}

func TestLaunchHandlerMissingCWDEnvelopeIsNotLSPUnavailable(t *testing.T) {
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{}, fmt.Errorf("%w: launch_agent cwd is required", contract.ErrLaunchCWDRequired)
		},
	}, mockExe())
	_, err := handler(context.Background(), json.RawMessage(`{"name":"child"}`))
	if err == nil {
		t.Fatal("HandleLaunchAgent() error = nil, want cwd error")
	}
	env := mcpcommon.NewToolErrorEnvelope("launch_agent", err)
	if env.Code != "cwd_required" {
		t.Fatalf("Code = %q, want cwd_required", env.Code)
	}
	if env.Retryable {
		t.Fatal("Retryable = true, want false")
	}
	if strings.Contains(strings.ToLower(env.Hint), "lsp") {
		t.Fatalf("Hint = %q, must not mention LSP", env.Hint)
	}
}
