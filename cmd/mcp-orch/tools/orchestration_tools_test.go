package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	orch "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
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
	require.Equal(t, "codex", launchEnvValue(req.Env, "AGENT_PROVIDER"))
	require.Equal(t, expectedLaunchAgentDefaultDisabledTools(t), launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	require.Equal(t, "spawn_agent", launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
}

func TestLaunchRequestFromExecutableComposesFocusedContext(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:        "agent-focused",
		Prompt:      " inspect launch flow ",
		ContextMode: " FoCuSeD ",
		Context:     " background: use Codex only\nconstraint: do not fork history ",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, "【相关上下文】\nbackground: use Codex only\nconstraint: do not fork history\n\n【任务】\ninspect launch flow", req.Prompt)
}

func TestLaunchRequestFromExecutableContextModeValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   LaunchAgentInput
		wantErr string
	}{
		{
			name: "focused requires context",
			input: LaunchAgentInput{
				Name:        "agent-focused",
				Prompt:      "inspect",
				ContextMode: "focused",
				Context:     " \t\n ",
			},
			wantErr: "context_mode=focused requires non-empty context field",
		},
		{
			name: "minimal rejects context",
			input: LaunchAgentInput{
				Name:        "agent-minimal",
				Prompt:      "inspect",
				ContextMode: "minimal",
				Context:     "background",
			},
			wantErr: "context_mode=minimal does not accept context field",
		},
		{
			name: "default minimal rejects context",
			input: LaunchAgentInput{
				Name:    "agent-minimal-default",
				Prompt:  "inspect",
				Context: "background",
			},
			wantErr: "context_mode=minimal does not accept context field",
		},
		{
			name: "forked requires parent",
			input: LaunchAgentInput{
				Name:        "agent-forked",
				Prompt:      "inspect",
				ContextMode: "forked",
			},
			wantErr: "context_mode=forked requires non-empty parent_id",
		},
		{
			name: "forked rejects context",
			input: LaunchAgentInput{
				Name:        "agent-forked",
				Prompt:      "inspect",
				ContextMode: "forked",
				ParentID:    "agent-parent",
				Context:     "background",
			},
			wantErr: "context_mode=forked does not accept context field",
		},
		{
			name: "unsupported mode",
			input: LaunchAgentInput{
				Name:        "agent-unknown",
				Prompt:      "inspect",
				ContextMode: "full-history",
			},
			wantErr: "unsupported context_mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := launchRequestFromExecutable(tt.input, "/tmp/agent-terminal")
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLaunchHandlerRejectsPayloadParentThreadID(t *testing.T) {
	called := false
	handler := handleLaunchAgentWithExeFn(&golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			called = true
			return contract.AgentSnapshot{ID: req.AgentID, AgentID: req.AgentID, State: "launching"}, nil
		},
	}, func() (string, error) { return "/tmp/agent-terminal", nil })

	_, err := handler(context.Background(), json.RawMessage(`{
		"name":"agent-forked",
		"context_mode":"forked",
		"parent_id":"agent-parent",
		"parent_thread_id":"thread-parent-payload",
		"cwd":"/tmp/work",
		"provider":"codex"
	}`))

	require.Error(t, err)
	require.Contains(t, err.Error(), "parent_thread_id")
	require.False(t, called, "LaunchAgentSnapshot must not run after rejected parent_thread_id payload")
}

func TestLaunchAgentSchemaOmitsParentThreadID(t *testing.T) {
	props := launchAgentSchemaProperties(t)

	require.Contains(t, props, "parent_id")
	require.NotContains(t, props, "parent_thread_id")
}

func TestLaunchAgentSchemaDocumentsAssembledSections(t *testing.T) {
	props := launchAgentSchemaProperties(t)
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

func TestLaunchAgentSchemaDocumentsContextMode(t *testing.T) {
	props := launchAgentSchemaProperties(t)
	contextMode, ok := props["context_mode"].(map[string]any)
	if !ok {
		t.Fatalf("context_mode schema type = %T, want map[string]any", props["context_mode"])
	}
	require.ElementsMatch(t, []string{"minimal", "focused", "forked"}, EnumValues(Schema(contextMode)))
	context, ok := props["context"].(map[string]any)
	if !ok {
		t.Fatalf("context schema type = %T, want map[string]any", props["context"])
	}
	contextDescription, _ := context["description"].(string)
	contextModeDescription, _ := contextMode["description"].(string)
	for _, want := range []string{
		"minimal",
		"focused",
		"forked",
		"Do not copy the parent conversation history",
	} {
		require.Contains(t, contextModeDescription, want)
	}
	for _, want := range []string{
		"focused",
		"background",
		"confirmed decisions",
		"relevant file paths",
		"forbidden actions",
		"return format",
		"known risks",
		"file paths, function names, line numbers, and constraints",
		"Do not paste large code blocks",
		"fixed Markdown report template",
		"must not delegate again",
	} {
		require.Contains(t, contextDescription, want)
	}
	for _, field := range []string{"files", "constraints", "return_format"} {
		require.NotContains(t, props, field)
		require.NotContains(t, contextDescription, "`"+field+"`")
		require.NotContains(t, contextModeDescription, "`"+field+"`")
	}
}

func TestLaunchAgentSchemaDocumentsReadOnly(t *testing.T) {
	props := launchAgentSchemaProperties(t)
	readOnly, ok := props["read_only"].(map[string]any)
	require.Truef(t, ok, "read_only schema type = %T, want map[string]any", props["read_only"])

	description, _ := readOnly["description"].(string)
	require.Equal(t, "boolean", readOnly["type"])
	require.Contains(t, description, "read-only")
	require.Contains(t, description, "review")
	require.Contains(t, description, "planning")
	require.Contains(t, description, "does not change agent_type")
}

func launchAgentSchemaProperties(t *testing.T) map[string]any {
	t.Helper()
	defs := orchestrationToolDefinitions(ToolPorts{})
	for _, def := range defs {
		if def.Name != "launch_agent" {
			continue
		}
		props, ok := def.InputSchema["properties"].(map[string]any)
		require.Truef(t, ok, "properties schema type = %T, want map[string]any", def.InputSchema["properties"])
		return props
	}
	t.Fatal("launch_agent definition not found")
	return nil
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
	require.Equal(t, "codex", launchEnvValue(req.Env, "AGENT_PROVIDER"))
	require.Equal(t, expectedLaunchAgentDefaultDisabledTools(t), launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	require.Equal(t, "spawn_agent", launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
}

func TestLaunchRequestFromExecutableMergesDefaultDisabledTools(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:          "agent-default-deny",
		CWD:           "/tmp/work",
		DisabledTools: " shell , spawn_agent, browser , launch_agent ",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, expectedLaunchAgentDefaultDisabledTools(t, "shell", "browser"), launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	require.Equal(t, "spawn_agent", launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
}

func TestLaunchRequestFromExecutableAppliesReviewerDisabledToolsForReadOnlyAgentTypes(t *testing.T) {
	tests := []struct {
		name      string
		agentType contract.AgentType
	}{
		{name: "plan", agentType: contract.AgentTypePlan},
		{name: "explore", agentType: contract.AgentTypeExplore},
	}

	reviewerDenied := contract.ReadOnlyAgentDeniedTools()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := launchRequestFromExecutable(LaunchAgentInput{
				Name:          "agent-" + tt.name,
				AgentType:     string(tt.agentType),
				DisabledTools: " custom_tool , spawn_agent ",
			}, "/tmp/agent-terminal")
			require.NoError(t, err)

			disabled := disabledToolCounts(launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
			defaults, err := defaultLaunchAgentDisabledTools()
			require.NoError(t, err)
			for _, tool := range defaults {
				require.Equalf(t, 1, disabled[tool], "%s disabled count", tool)
			}
			for _, tool := range reviewerDenied {
				require.Equalf(t, 1, disabled[tool], "%s disabled count", tool)
			}
			for _, tool := range []string{"patch_edit", "task_start_dag"} {
				require.Equalf(t, 1, disabled[tool], "%s disabled count", tool)
			}
			for _, legacy := range []string{"edit", "lsp_edit"} {
				require.NotContains(t, disabled, legacy)
			}
			require.Equal(t, 1, disabled["custom_tool"])
		})
	}
}

func TestLaunchRequestFromExecutableAppliesReadOnlyToolsWhenReadOnlyIsExplicit(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:      "agent-reviewer",
		AgentType: "reviewer",
		ReadOnly:  true,
		Provider:  "codex",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, "reviewer", req.AgentType)

	disabled := disabledToolCounts(launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	for _, tool := range contract.ReadOnlyAgentDeniedTools() {
		require.Equalf(t, 1, disabled[tool], "%s disabled count", tool)
	}

	nativeDisabled := disabledToolCounts(launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
	for _, tool := range []string{
		contract.CodexNativeToolShell,
		contract.CodexNativeToolApplyPatch,
		contract.CodexNativeToolWriteNewFile,
		contract.CodexNativeToolSpawnAgent,
		contract.CodexNativeToolMultiAgent,
		contract.CodexNativeToolMultiToolParallel,
		contract.CodexNativeToolUpdatePlan,
	} {
		require.Equalf(t, 1, nativeDisabled[tool], "%s native disabled count", tool)
	}
}

func TestLaunchRequestFromExecutableAppliesReadOnlyCodexNativeToolsForReadOnlyAgentTypes(t *testing.T) {
	tests := []struct {
		name      string
		agentType contract.AgentType
	}{
		{name: "plan", agentType: contract.AgentTypePlan},
		{name: "explore", agentType: contract.AgentTypeExplore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := launchRequestFromExecutable(LaunchAgentInput{
				Name:      "agent-native-" + tt.name,
				AgentType: string(tt.agentType),
				Provider:  "codex",
			}, "/tmp/agent-terminal")
			require.NoError(t, err)

			disabled := disabledToolCounts(launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
			for _, tool := range []string{
				contract.CodexNativeToolShell,
				contract.CodexNativeToolApplyPatch,
				contract.CodexNativeToolWriteNewFile,
				contract.CodexNativeToolSpawnAgent,
				contract.CodexNativeToolMultiAgent,
				contract.CodexNativeToolMultiToolParallel,
				contract.CodexNativeToolUpdatePlan,
			} {
				require.Equalf(t, 1, disabled[tool], "%s native disabled count", tool)
			}
		})
	}
}

func TestLaunchRequestFromExecutableDoesNotApplyReviewerDisabledToolsToWorker(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:      "agent-worker-deny",
		AgentType: "worker",
		ReadOnly:  false,
	}, "/tmp/agent-terminal")
	require.NoError(t, err)

	disabled := disabledToolCounts(launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	defaults, err := defaultLaunchAgentDisabledTools()
	require.NoError(t, err)
	require.Len(t, disabled, len(defaults))
	for _, tool := range defaults {
		require.Equalf(t, 1, disabled[tool], "%s disabled count", tool)
	}
	for _, tool := range []string{"patch_edit", "task_start_dag"} {
		require.NotContains(t, disabled, tool)
	}
	for _, legacy := range []string{"edit", "lsp_edit"} {
		require.NotContains(t, disabled, legacy)
	}

	nativeDisabled := disabledToolCounts(launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
	require.Equal(t, 1, nativeDisabled[contract.CodexNativeToolSpawnAgent])
	for _, tool := range []string{
		contract.CodexNativeToolShell,
		contract.CodexNativeToolApplyPatch,
		contract.CodexNativeToolWriteNewFile,
		contract.CodexNativeToolMultiAgent,
		contract.CodexNativeToolUpdatePlan,
	} {
		require.NotContains(t, nativeDisabled, tool)
	}
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
		"AGENT_DISABLED_TOOLS=" + expectedLaunchAgentDefaultDisabledTools(t): true,
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

func TestLaunchRequestFromExecutableAllowsClaudeRootAgent(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     "agent-claude-root",
		Provider: "claude",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, "claude", launchEnvValue(req.Env, "AGENT_PROVIDER"))
	require.Equal(t, expectedLaunchAgentDefaultDisabledTools(t), launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	require.Empty(t, launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
}

func TestLaunchRequestFromExecutableRejectsClaudeChildAgent(t *testing.T) {
	_, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:     "agent-claude-child",
		ParentID: "agent-parent",
		Provider: "claude",
	}, "/tmp/agent-terminal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Claude sub-agent orchestration is not supported")
	require.Contains(t, err.Error(), "provider=codex")
}

func TestLaunchRequestFromExecutableForwardsCodexIdentity(t *testing.T) {
	var in LaunchAgentInput
	err := json.Unmarshal([]byte(`{
		"name": "agent-codex-provider",
		"provider": "codex",
		"codex_home": " /Users/mac/.codex ",
		"codex_instance_key": " default ",
		"codex_model_provider": " openai "
	}`), &in)
	if err != nil {
		t.Fatalf("unmarshal launch input: %v", err)
	}
	req, err := launchRequestFromExecutable(in, "/tmp/agent-terminal")
	if err != nil {
		t.Fatalf("launchRequestFromExecutable() error = %v", err)
	}
	if got := launchEnvValue(req.Env, "AGENT_CODEX_HOME"); got != "/Users/mac/.codex" {
		t.Fatalf("AGENT_CODEX_HOME = %q, want /Users/mac/.codex; env=%#v", got, req.Env)
	}
	if got := launchEnvValue(req.Env, "AGENT_CODEX_INSTANCE_KEY"); got != "default" {
		t.Fatalf("AGENT_CODEX_INSTANCE_KEY = %q, want default; env=%#v", got, req.Env)
	}
	if got := launchEnvValue(req.Env, "AGENT_CODEX_MODEL_PROVIDER"); got != "openai" {
		t.Fatalf("AGENT_CODEX_MODEL_PROVIDER = %q, want openai; env=%#v", got, req.Env)
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
	if got := launchEnvValue(req.Env, "AGENT_PROVIDER"); got != "claude" {
		t.Fatalf("AGENT_PROVIDER = %q, want claude; env=%#v", got, req.Env)
	}
	if got := launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"); got != expectedLaunchAgentDefaultDisabledTools(t) {
		t.Fatalf("AGENT_DISABLED_TOOLS = %q, want default child delegation deny list; env=%#v", got, req.Env)
	}
	if got := launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"); got != "" {
		t.Fatalf("AGENT_CODEX_DISABLED_NATIVE_TOOLS = %q, want empty for non-codex provider; env=%#v", got, req.Env)
	}
}

func launchEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if value, ok := strings.CutPrefix(item, prefix); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func expectedLaunchAgentDefaultDisabledTools(t testing.TB, extra ...string) string {
	t.Helper()

	defaults, err := defaultLaunchAgentDisabledTools()
	require.NoError(t, err)
	return joinUniqueCSV(defaults, strings.Join(extra, ","))
}

func disabledToolCounts(csv string) map[string]int {
	counts := map[string]int{}
	for item := range strings.SplitSeq(csv, ",") {
		tool := strings.TrimSpace(item)
		if tool == "" {
			continue
		}
		counts[tool]++
	}
	return counts
}

func TestListAgentsHandlerDefaultsToActiveCompactSnapshots(t *testing.T) {
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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

func TestListAgentsHandlerFiltersCommaSeparatedState(t *testing.T) {
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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
	handler := handleListAgentsWithStub(&golden.OrchestrationStub{
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

// TestHandleStopAgentDoesNotReportArchivedForNoop 验证无实际归档结果时不会上报 archived=true。
func TestHandleStopAgentDoesNotReportArchivedForNoop(t *testing.T) {
	svc := &archiveCapableOrchestrationStub{archiveNoop: true}
	handler := HandleStopAgent(svc)
	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-1"}`))
	if !errors.Is(err, contract.ErrAgentNotFound) {
		t.Fatalf("HandleStopAgent() error = %v, want ErrAgentNotFound for noop archive", err)
	}
}

type archiveCapableOrchestrationStub struct {
	golden.OrchestrationStub
	archivedAgentID string
	stoppedAgentID  string
	archiveNoop     bool
}

func (s *archiveCapableOrchestrationStub) ArchiveAgent(_ context.Context, agentID string) (orch.ArchiveOutcome, error) {
	if s.archiveNoop {
		return orch.ArchiveOutcome{}, nil
	}
	s.archivedAgentID = agentID
	return orch.ArchiveOutcome{BindingArchived: true}, nil
}

func (s *archiveCapableOrchestrationStub) StopAgent(_ context.Context, agentID string) error {
	s.stoppedAgentID = agentID
	return nil
}
