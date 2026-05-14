package thread

import (
	"encoding/json"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
		"personality":"pragmatic",
		"language":"zh"
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
	if params.Language != "zh" {
		t.Fatalf("startParams language = %q, want zh", params.Language)
	}
	request := buildStartRequestFromParams(params)
	if request.Language != "zh" {
		t.Fatalf("StartRequest.Language = %q, want zh", request.Language)
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

func TestNormalizeStartRequestDropsConfigArtifacts(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{
		ModelProvider: "[object Object]",
		Model:         "[object Object]",
		Effort:        "undefined",
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Model != "" || req.ModelProvider != "" || req.Effort != "" {
		t.Fatalf("normalizeStartRequest artifacts = model %q provider %q effort %q, want empty", req.Model, req.ModelProvider, req.Effort)
	}
	if req.Provider != defaultStartProvider {
		t.Fatalf("Provider = %q, want default %q", req.Provider, defaultStartProvider)
	}
}

func TestNormalizeThreadConfigPatchDropsConfigArtifacts(t *testing.T) {
	t.Parallel()

	model := "[object Object]"
	effort := "undefined"
	patch, err := normalizeThreadConfigPatchOffline("claude", dto.ThreadConfigPatch{Model: &model, Effort: &effort})
	if err != nil {
		t.Fatalf("normalizeThreadConfigPatchOffline() error = %v", err)
	}
	if got := threadConfigPatchValue(patch.Model); got != "" {
		t.Fatalf("patch model = %q, want empty", got)
	}
	if got := threadConfigPatchValue(patch.Effort); got != "" {
		t.Fatalf("patch effort = %q, want empty", got)
	}
}

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

// TestBuildStartResponse_PromptKeyStaleSurfacedToWire verifies the
// buildStartResponse helper echoes the stale flag on both snake_case and
// camelCase keys (matching the rest of this dual-key response shape). The UI
// reads `prompt_key_stale` / `promptKeyStale` to decide whether to clean its
// `settings.activePromptKey` preference; if either key drops on the wire the
// UI loses the signal and the pref never self-clears.
func TestBuildStartResponse_PromptKeyStaleSurfacedToWire(t *testing.T) {
	t.Parallel()

	resp := buildStartResponse(StartResult{
		ThreadID:       "thread-x",
		PromptKey:      "main/missing",
		PromptKeyStale: true,
	})

	if resp.PromptKeyStale == nil || !*resp.PromptKeyStale {
		t.Fatalf("snake-case prompt_key_stale missing or false: %#v", resp.PromptKeyStale)
	}
	if resp.PromptKeyStaleCamel == nil || !*resp.PromptKeyStaleCamel {
		t.Fatalf("camelCase promptKeyStale missing or false: %#v", resp.PromptKeyStaleCamel)
	}

	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wireStr := string(wire)
	if !strings.Contains(wireStr, `"prompt_key_stale":true`) {
		t.Fatalf("wire payload missing snake_case prompt_key_stale=true: %s", wireStr)
	}
	if !strings.Contains(wireStr, `"promptKeyStale":true`) {
		t.Fatalf("wire payload missing camelCase promptKeyStale=true: %s", wireStr)
	}
}

// TestBuildStartResponse_PromptKeyStaleOmittedOnHappyPath ensures the new
// field is fully omitempty on the success path so today's wire shape stays
// byte-identical. A truthy stale field on a successful launch would cause the
// UI to wipe the user's launch-prompt preference on every start.
func TestBuildStartResponse_PromptKeyStaleOmittedOnHappyPath(t *testing.T) {
	t.Parallel()

	resp := buildStartResponse(StartResult{
		ThreadID:       "thread-x",
		PromptKey:      "main/sql",
		PromptKeyStale: false,
	})

	if resp.PromptKeyStale != nil || resp.PromptKeyStaleCamel != nil {
		t.Fatalf("happy path must leave stale pointers nil: snake=%v camel=%v",
			resp.PromptKeyStale, resp.PromptKeyStaleCamel)
	}
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wireStr := string(wire)
	if strings.Contains(wireStr, "prompt_key_stale") || strings.Contains(wireStr, "promptKeyStale") {
		t.Fatalf("happy path response leaked stale key: %s", wireStr)
	}
}
