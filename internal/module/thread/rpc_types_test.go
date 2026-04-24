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
		"model":"gpt-5.5",
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
	if params.CWD != "/tmp/project" || params.Model != "gpt-5.5" {
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
	if params.Name != "" {
		t.Fatalf("Name should be empty when only prompt is set, got %q", params.Name)
	}
	if params.Prompt != "legacy prompt" {
		t.Fatalf("legacy prompt = %#v", params)
	}
}

func TestStartParamsPromptDoesNotPopulateName(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"prompt":"legacy prompt"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.Name != "" {
		t.Fatalf("Name should be empty when only prompt is set, got %q", params.Name)
	}
	if params.Prompt != "legacy prompt" {
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
		"model":"gpt-5.5"
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.ThreadID != "thread-1" || params.Path != "/tmp/history" || params.CWD != "/tmp/repo" || params.Model != "gpt-5.5" {
		t.Fatalf("resumeParams = %#v", params)
	}
}

func TestNormalizeStartRequestDefaultsProviderWithoutPromptPollution(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{
		BaseInstructions: "  launch me  ",
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", req.Provider)
	}
}

func TestNormalizeStartRequestDoesNotDeriveNameFromPrompt(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{Provider: "codex", Prompt: "  launch me  "})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Name != "" {
		t.Fatalf("normalizeStartRequest().Name = %q, want empty (prompt should not become name)", req.Name)
	}
	if req.Prompt != "launch me" {
		t.Fatalf("normalizeStartRequest().Prompt = %q, want 'launch me'", req.Prompt)
	}
}

// TestStartParamsAcceptsSelectedSkillsCamelCase p20.3 §4.3：前端 send path 用
// camelCase；launch payload 对齐后 startParams 必须同时接受 camelCase 别名。
func TestStartParamsAcceptsSelectedSkillsCamelCase(t *testing.T) {
	t.Parallel()

	var params startParams
	input := []byte(`{
		"cwd":"/tmp/project",
		"selectedSkills":["planner"," reviewer "],
		"manualSkillSelection":true
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !params.ManualSkillSelection {
		t.Fatalf("ManualSkillSelection should parse from camelCase alias")
	}
	if len(params.SelectedSkills) != 2 || params.SelectedSkills[0] != "planner" || strings.TrimSpace(params.SelectedSkills[1]) != "reviewer" {
		t.Fatalf("SelectedSkills = %#v", params.SelectedSkills)
	}
}

// TestStartParamsAcceptsSelectedSkillsSnakeCase p20.3 §4.3：主 tag 仍是
// snake_case；camelCase 别名只作兼容读取，主路径必须保留。
func TestStartParamsAcceptsSelectedSkillsSnakeCase(t *testing.T) {
	t.Parallel()

	var params startParams
	input := []byte(`{
		"cwd":"/tmp/project",
		"selected_skills":["debug"],
		"manual_skill_selection":true
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !params.ManualSkillSelection {
		t.Fatalf("ManualSkillSelection snake_case should parse")
	}
	if len(params.SelectedSkills) != 1 || params.SelectedSkills[0] != "debug" {
		t.Fatalf("SelectedSkills = %#v", params.SelectedSkills)
	}
}

// TestStartParamsOmitsLaunchSkillsWhenAbsent p20.3 §4.3：旧 payload 不写新字段时
// 行为不变；null / 缺失 / 空数组均导致 SelectedSkills==nil、ManualSkillSelection==false。
func TestStartParamsOmitsLaunchSkillsWhenAbsent(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"cwd":"/tmp"}`), &params); err != nil {
		t.Fatalf("empty payload error = %v", err)
	}
	if params.SelectedSkills != nil {
		t.Fatalf("SelectedSkills = %#v, want nil", params.SelectedSkills)
	}
	if params.ManualSkillSelection {
		t.Fatalf("ManualSkillSelection should default false")
	}
}

// TestStartParamsRejectsInvalidSelectedSkillsType p20.3 §4.3：非字符串数组的
// camelCase payload 要报错，防止客户端把对象 / 数字塞进来。
func TestStartParamsRejectsInvalidSelectedSkillsType(t *testing.T) {
	t.Parallel()

	var params startParams
	err := json.Unmarshal([]byte(`{"selectedSkills":"planner"}`), &params)
	if err == nil || !strings.Contains(err.Error(), "selectedSkills") {
		t.Fatalf("expected selectedSkills type error, got %v", err)
	}
}

