package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

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
		PromptKey:   " main/sql ",
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
	require.Equal(t, "main/sql", req.PromptKey)
	require.Equal(t, "local", req.MemoryScope)
	require.Equal(t, []string{"/tmp/agent-terminal"}, req.Command)
	require.Equal(t, []string{"AGENT_PROVIDER=codex"}, req.Env)
}

func TestLaunchAgentSchemaDocumentsAssembledSections(t *testing.T) {
	defs := orchestrationToolDefinitions(&golden.OrchestrationStub{})
	var launchDef ToolDefinition
	for _, def := range defs {
		if def.Name == "launch_agent" {
			launchDef = def
			break
		}
	}
	if launchDef.Name == "" {
		t.Fatal("launch_agent definition not found")
	}
	props, ok := launchDef.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties schema type = %T, want map[string]any", launchDef.InputSchema["properties"])
	}
	agentKey, ok := props["agent_key"].(map[string]any)
	if !ok {
		t.Fatalf("agent_key schema type = %T, want map[string]any", props["agent_key"])
	}
	description, _ := agentKey["description"].(string)
	if !strings.Contains(description, "assembled sections as base_instructions") {
		t.Fatalf("agent_key description = %q, want assembled sections semantics", description)
	}
	if strings.Contains(description, "prompt_text as base_instructions") {
		t.Fatalf("agent_key description still documents prompt_text injection: %q", description)
	}
	promptKey, ok := props["prompt_key"].(map[string]any)
	if !ok {
		t.Fatalf("prompt_key schema type = %T, want map[string]any", props["prompt_key"])
	}
	promptKeyDescription, _ := promptKey["description"].(string)
	if !strings.Contains(promptKeyDescription, "exact prompt_template.prompt_key") {
		t.Fatalf("prompt_key description = %q, want exact prompt_template.prompt_key semantics", promptKeyDescription)
	}
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
