package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
	"github.com/stretchr/testify/require"
)

func TestLaunchRequestFromExecutableBuildsLaunchRequest(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		AgentID:     " agent-persist-1 ",
		Name:        " agent-1 ",
		Prompt:      " hello ",
		ParentID:    " agent-root ",
		AgentType:   " worker ",
		MemoryScope: " local ",
		CWD:         "/tmp/work",
		Provider:    " codex ",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, "agent-persist-1", req.AgentID)
	require.Equal(t, "agent-1", req.Name)
	require.Equal(t, "hello", req.Prompt)
	require.Equal(t, "/tmp/work", req.Cwd)
	require.Equal(t, "agent-root", req.ParentID)
	require.Equal(t, "worker", req.AgentType)
	require.Equal(t, "local", req.MemoryScope)
	require.Equal(t, []string{"/tmp/agent-terminal"}, req.Command)
	require.Equal(t, []string{"AGENT_PROVIDER=codex"}, req.Env)
}

func TestLaunchRequestFromExecutablePreservesCWDForContractValidation(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     "agent-cwd-whitespace",
		CWD:      " /tmp/work ",
		Provider: "codex",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	require.Equal(t, " /tmp/work ", req.Cwd)
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

func TestLaunchRequestFromExecutableDefaultsProviderToCodex(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name: "agent-default-provider",
		CWD:  "/tmp/work",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	require.Equal(t, []string{"AGENT_PROVIDER=codex"}, req.Env)
}

func TestLaunchRequestFromExecutableForwardsModel(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     "agent-m",
		Provider: "claude",
		Model:    " claude-opus-4-7[1m] ",
		Effort:   " max ",
	}, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	want := map[string]bool{
		"AGENT_PROVIDER=claude":           true,
		"AGENT_MODEL=claude-opus-4-7[1m]": true,
		"AGENT_EFFORT=max":                true,
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

func TestListAgentsHandlerDefaultsToActiveCompactSnapshots(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "agent-stopped", AgentID: "agent-stopped", State: "stopped", LastReport: "old report"},
				{ID: "agent-idle", AgentID: "agent-idle", State: "idle", LastReport: "huge report"},
				{ID: "agent-running", AgentID: "agent-running", State: "turn_running", LastReport: "running report"},
			}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 2 {
		t.Fatalf("HandleListAgents() len = %d, want 2: %#v", len(got), got)
	}
	for _, snapshot := range got {
		if snapshot.State == "stopped" {
			t.Fatalf("HandleListAgents() included inactive snapshot: %#v", snapshot)
		}
		if snapshot.LastReport != "" {
			t.Fatalf("HandleListAgents() LastReport = %q, want omitted", snapshot.LastReport)
		}
	}
}

func TestListAgentsHandlerCanIncludeInactiveReportsAndLimit(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "agent-stopped", AgentID: "agent-stopped", State: "stopped", LastReport: "old report"},
				{ID: "agent-idle", AgentID: "agent-idle", State: "idle", LastReport: "idle report"},
			}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"include_inactive":true,"include_reports":true,"limit":1}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 1 {
		t.Fatalf("HandleListAgents() len = %d, want 1: %#v", len(got), got)
	}
	if got[0].AgentID != "agent-stopped" || got[0].LastReport != "old report" {
		t.Fatalf("HandleListAgents() first snapshot = %#v, want stopped with report", got[0])
	}
}

func TestListAgentsHandlerFiltersCommaSeparatedState(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "agent-stopped", AgentID: "agent-stopped", State: "stopped"},
				{ID: "agent-idle", AgentID: "agent-idle", State: "idle"},
				{ID: "agent-running", AgentID: "agent-running", State: "turn_running"},
			}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"state":"idle, turn_running"}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 2 || got[0].AgentID != "agent-idle" || got[1].AgentID != "agent-running" {
		t.Fatalf("HandleListAgents() = %#v, want idle and running", got)
	}
}

func TestListAgentsHandlerDefaultsToTrustedScopeCWD(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "other-agent", AgentID: "other-agent", State: "idle", Cwd: "/repo/other"},
				{ID: "current-agent", AgentID: "current-agent", State: "idle", Cwd: "/repo/current"},
			}, nil
		},
	})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: "/repo/current"})

	result, err := handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 1 || got[0].AgentID != "current-agent" {
		t.Fatalf("HandleListAgents() = %#v, want only current cwd agent", got)
	}
}

func TestListAgentsHandlerExcludesLegacyAgentsWithoutCWDWhenFiltering(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "other-agent", AgentID: "other-agent", State: "idle", Cwd: "/repo/other"},
				{ID: "legacy-agent", AgentID: "legacy-agent", State: "idle"},
				{ID: "current-agent", AgentID: "current-agent", State: "idle", Cwd: "/repo/current"},
			}, nil
		},
	})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: "/repo/current"})

	result, err := handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	gotIDs := []string{}
	for _, agent := range got {
		gotIDs = append(gotIDs, agent.AgentID)
	}
	if !reflect.DeepEqual(gotIDs, []string{"current-agent"}) {
		t.Fatalf("HandleListAgents() agent IDs = %#v, want only current cwd agents", gotIDs)
	}
}

func TestListAgentsHandlerCWDFilterStillDefaultsToActiveAgents(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "stopped-agent", AgentID: "stopped-agent", State: "stopped", Cwd: "/repo/current"},
				{ID: "current-agent", AgentID: "current-agent", State: "idle", Cwd: "/repo/current"},
			}, nil
		},
	})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: "/repo/current"})

	result, err := handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 1 || got[0].AgentID != "current-agent" {
		t.Fatalf("HandleListAgents() = %#v, want only active current cwd agent", got)
	}
}

func TestListAgentsHandlerFiltersExplicitCWDWithoutTrustedScope(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "other-agent", AgentID: "other-agent", State: "idle", Cwd: "/repo/other"},
				{ID: "current-agent", AgentID: "current-agent", State: "idle", Cwd: "/repo/current"},
			}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"cwd":"/repo/current"}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 1 || got[0].AgentID != "current-agent" {
		t.Fatalf("HandleListAgents() = %#v, want explicit cwd agent", got)
	}
}

func TestListAgentsHandlerTrustedScopeCWDOverridesArgumentCWD(t *testing.T) {
	handler := HandleListAgents(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{
				{ID: "evil-agent", AgentID: "evil-agent", State: "idle", Cwd: "/repo/evil"},
				{ID: "trusted-agent", AgentID: "trusted-agent", State: "idle", Cwd: "/repo/trusted"},
			}, nil
		},
	})
	ctx := mcpcommon.WithToolScope(context.Background(), mcpcommon.ToolScope{CWD: "/repo/trusted"})

	result, err := handler(ctx, json.RawMessage(`{"cwd":"/repo/evil"}`))
	if err != nil {
		t.Fatalf("HandleListAgents() error = %v", err)
	}
	got, ok := result.([]contract.AgentSnapshot)
	if !ok {
		t.Fatalf("HandleListAgents() result type = %T, want []contract.AgentSnapshot", result)
	}
	if len(got) != 1 || got[0].AgentID != "trusted-agent" {
		t.Fatalf("HandleListAgents() = %#v, want trusted cwd agent", got)
	}
}

func TestStopAgentHandlerArchivesWhenSupported(t *testing.T) {
	svc := &archiveCapableOrchestrationStub{}
	handler := HandleStopAgent(svc)

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":" agent-1 "}`))
	if err != nil {
		t.Fatalf("HandleStopAgent() error = %v", err)
	}
	if svc.archivedAgentID != "agent-1" {
		t.Fatalf("archived agent = %q, want agent-1", svc.archivedAgentID)
	}
	if svc.stoppedAgentID != "" {
		t.Fatalf("stopped agent = %q, want ArchiveAgent path only", svc.stoppedAgentID)
	}
	got, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleStopAgent() result type = %T, want map[string]any", result)
	}
	if got["success"] != true || got["agent_id"] != "agent-1" || got["archived"] != true {
		t.Fatalf("HandleStopAgent() result = %#v, want success archived agent-1", got)
	}
}

type archiveCapableOrchestrationStub struct {
	golden.OrchestrationStub
	archivedAgentID string
	stoppedAgentID  string
}

func (s *archiveCapableOrchestrationStub) ArchiveAgent(_ context.Context, agentID string) error {
	s.archivedAgentID = agentID
	return nil
}

func (s *archiveCapableOrchestrationStub) StopAgent(_ context.Context, agentID string) error {
	s.stoppedAgentID = agentID
	return nil
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
	require.NoError(t, err)

	result, err := handler(context.Background(), input)
	require.NoError(t, err)
	resultMap, ok := result.(map[string]any)
	require.Truef(t, ok, "HandleLaunchAgent() result type = %T, want map[string]any", result)
	require.Equal(t, true, resultMap["success"])
	require.Equal(t, "launching", resultMap["status"])

	// Wait for the async goroutine to call LaunchAgent.
	select {
	case got := <-done:
		require.Equal(t, "agent-persist-1", got.AgentID)
		require.Equal(t, "agent-1", got.Name)
		require.Equal(t, "hello", got.Prompt)
		require.Equal(t, "/tmp/work", got.Cwd)
		require.Equal(t, "agent-root", got.ParentID)
		require.Equal(t, "worker", got.AgentType)
		require.Equal(t, "project", got.MemoryScope)
		require.Equal(t, []string{"/tmp/mcp-orch"}, got.Command)
		require.Equal(t, []string{"AGENT_PROVIDER=codex"}, got.Env)
	case <-time.After(5 * time.Second):
		t.Fatal("async LaunchAgent was not called within 5s")
	}
}

func TestLaunchHandlerReturnsExistingDuplicateAgentID(t *testing.T) {
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	launchCalls := 0
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		ListAgentsFunc: func(context.Context) ([]contract.AgentSnapshot, error) {
			return []contract.AgentSnapshot{{ID: "agent-dup", AgentID: "agent-dup", ThreadID: "thread-dup", State: "turn_running"}}, nil
		},
		LaunchAgentFunc: func(_ context.Context, req contract.LaunchRequest) error {
			launchCalls++
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
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	var captured contract.LaunchRequest
	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			captured = req
			return contract.AgentSnapshot{ID: req.AgentID, AgentID: req.AgentID, State: "launching"}, nil
		},
	})

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

func TestLaunchAgentCWDDescriptionDocumentsConditionalRequirement(t *testing.T) {
	var found ToolDefinition
	for _, tool := range orchestrationToolDefinitions(&golden.OrchestrationStub{}) {
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
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return "/tmp/mcp-orch", nil }
	defer func() { osExecutable = originalExecutable }()

	handler := HandleLaunchAgent(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{}, fmt.Errorf("%w: launch_agent cwd is required", contract.ErrLaunchCWDRequired)
		},
	})
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
