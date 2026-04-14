package thread

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStartParamsAcceptV2WireFields(t *testing.T) {
	t.Parallel()

	var params startParams
	input := []byte(`{
		"cwd":"/tmp/project",
		"model":"gpt-5.4",
		"modelProvider":"openai",
		"approvalPolicy":"never",
		"parentAgentId":"agent-root",
		"agentType":"worker",
		"agentMemoryScope":"local",
		"baseInstructions":"system prompt",
		"developerInstructions":"dev prompt",
		"sandbox":{"type":"danger-full-access"},
		"summary":"concise",
		"effort":"high",
		"personality":"pragmatic"
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.CWD != "/tmp/project" || params.Model != "gpt-5.4" {
		t.Fatalf("startParams basic fields = %#v", params)
	}
	if params.ModelProvider != "openai" || params.ApprovalPolicy != "never" {
		t.Fatalf("startParams provider fields = %#v", params)
	}
	if params.ParentAgentID != "agent-root" || params.AgentType != "worker" || params.AgentMemoryScope != "local" {
		t.Fatalf("startParams agent fields = %#v", params)
	}
	if params.BaseInstructions != "system prompt" || params.DeveloperInstructions != "dev prompt" {
		t.Fatalf("startParams instructions = %#v", params)
	}
	if string(params.Sandbox) != `{"type":"danger-full-access"}` {
		t.Fatalf("startParams sandbox = %s", params.Sandbox)
	}
	if params.Summary != "concise" || params.Effort != "high" || params.Personality != "pragmatic" {
		t.Fatalf("startParams config = %#v", params)
	}
}

func TestStartParamsKeepLegacyAliases(t *testing.T) {
	t.Parallel()

	var params startParams
	input := []byte(`{
		"model_provider":"azure",
		"approval_policy":"on-request",
		"parentId":"agent-root",
		"agent_type":"worker",
		"memory_scope":"project",
		"base_instructions":"legacy base",
		"developer_instructions":"legacy dev",
		"prompt":"legacy prompt"
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.ModelProvider != "azure" || params.ApprovalPolicy != "on-request" {
		t.Fatalf("legacy aliases = %#v", params)
	}
	if params.BaseInstructions != "legacy base" || params.DeveloperInstructions != "legacy dev" {
		t.Fatalf("legacy instructions = %#v", params)
	}
	if params.ParentAgentID != "agent-root" || params.AgentType != "worker" || params.AgentMemoryScope != "project" {
		t.Fatalf("legacy agent fields = %#v", params)
	}
	if params.Name != "legacy prompt" || params.Prompt != "legacy prompt" {
		t.Fatalf("legacy display name = %#v", params)
	}
}

func TestStartParamsPromptOnlyPopulatesName(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"prompt":"legacy prompt"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.Name != "legacy prompt" || params.Prompt != "legacy prompt" {
		t.Fatalf("legacy prompt = %#v", params)
	}
	if params.BaseInstructions != "" {
		t.Fatalf("baseInstructions = %q, want empty", params.BaseInstructions)
	}
}

func TestStartParamsRejectsConflictingInstructionAliases(t *testing.T) {
	t.Parallel()

	var params startParams
	err := json.Unmarshal([]byte(`{"baseInstructions":"explicit","instructions":"legacy"}`), &params)
	if err == nil || !strings.Contains(err.Error(), "conflicting base instructions") {
		t.Fatalf("json.Unmarshal() error = %v, want conflicting base instructions", err)
	}
}

func TestStartParamsMarshalUsesSnakeCase(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(startParams{
		ModelProvider:         "openai",
		ApprovalPolicy:        "never",
		ParentAgentID:         "agent-root",
		AgentType:             "worker",
		AgentMemoryScope:      "user",
		BaseInstructions:      "base",
		DeveloperInstructions: "dev",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"model_provider":"openai"`,
		`"approval_policy":"never"`,
		`"parent_agent_id":"agent-root"`,
		`"agent_type":"worker"`,
		`"agent_memory_scope":"user"`,
		`"base_instructions":"base"`,
		`"developer_instructions":"dev"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("marshal output = %s, want %s", text, want)
		}
	}
}

func TestResumeParamsAcceptThreadBodyFields(t *testing.T) {
	t.Parallel()

	var params resumeParams
	input := []byte(`{
		"threadId":"thread-1",
		"path":"/tmp/history",
		"cwd":"/tmp/repo",
		"model":"gpt-5.4"
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.ThreadID != "thread-1" || params.Path != "/tmp/history" || params.CWD != "/tmp/repo" || params.Model != "gpt-5.4" {
		t.Fatalf("resumeParams = %#v", params)
	}
}

func TestNormalizeStartRequestDefaultsProviderWithoutPromptPollution(t *testing.T) {
	t.Parallel()

	req, agentID, err := normalizeStartRequest(StartRequest{
		BaseInstructions: "  launch me  ",
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Provider != defaultStartProvider {
		t.Fatalf("provider = %q, want %q", req.Provider, defaultStartProvider)
	}
	if req.Name != "" || req.Prompt != "" {
		t.Fatalf("display fields = %#v, want empty name/prompt", req)
	}
	if agentID == "" || req.AgentID == "" {
		t.Fatalf("agent id = %q, want generated id", agentID)
	}
}

func TestNormalizeStartRequestDerivesNameFromDeprecatedPrompt(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{Prompt: "  launch me  "})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Name != "launch me" || req.Prompt != "launch me" {
		t.Fatalf("normalizeStartRequest() = %#v, want name/prompt launch me", req)
	}
}
