package thread

import (
	"encoding/json"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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
		"language":"zh",
		"launchIntentId":"launch_018f00e0-39fc-72ac-a47a-2a858c75d111"
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertStartParamsV2Identity(t, params)
	assertStartParamsV2Provider(t, params)
	assertStartParamsV2AgentFields(t, params)
	assertStartParamsV2Instructions(t, params)
	if string(params.Sandbox) != `{"type":"danger-full-access"}` {
		t.Fatalf("startParams sandbox = %s", params.Sandbox)
	}
	assertStartParamsV2Config(t, params)
	if params.Language != "zh" {
		t.Fatalf("startParams language = %q, want zh", params.Language)
	}
	request := buildStartRequestFromParams(params, nil)
	if request.Language != "zh" {
		t.Fatalf("StartRequest.Language = %q, want zh", request.Language)
	}
	if request.LaunchIntentID != "launch_018f00e0-39fc-72ac-a47a-2a858c75d111" {
		t.Fatalf("StartRequest.LaunchIntentID = %q", request.LaunchIntentID)
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
	assertStartParamsLegacyProvider(t, params)
	assertStartParamsLegacyInstructions(t, params)
	assertStartParamsLegacyAgentFields(t, params)
	if params.Name != "" {
		t.Fatalf("Name should be empty when only prompt is set, got %q", params.Name)
	}
	if params.Prompt != "legacy prompt" {
		t.Fatalf("legacy prompt = %#v", params)
	}
}

func assertStartParamsV2Identity(t *testing.T, params startParams) {
	t.Helper()

	if params.CWD != "/tmp/project" {
		t.Fatalf("startParams CWD = %q, want /tmp/project", params.CWD)
	}
	if params.Model != "gpt-5.5" {
		t.Fatalf("startParams Model = %q, want gpt-5.5", params.Model)
	}
}

func assertStartParamsV2Provider(t *testing.T, params startParams) {
	t.Helper()

	if params.ModelProvider != "openai" {
		t.Fatalf("startParams ModelProvider = %q, want openai", params.ModelProvider)
	}
	if params.ApprovalPolicy != "never" {
		t.Fatalf("startParams ApprovalPolicy = %q, want never", params.ApprovalPolicy)
	}
}

func assertStartParamsV2AgentFields(t *testing.T, params startParams) {
	t.Helper()

	if params.ParentAgentID != "agent-root" {
		t.Fatalf("startParams ParentAgentID = %q, want agent-root", params.ParentAgentID)
	}
	if params.AgentType != "worker" || params.AgentMemoryScope != "local" {
		t.Fatalf("startParams agent fields = %#v", params)
	}
}

func assertStartParamsV2Instructions(t *testing.T, params startParams) {
	t.Helper()

	if params.BaseInstructions != "system prompt" {
		t.Fatalf("startParams BaseInstructions = %q, want system prompt", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "dev prompt" {
		t.Fatalf("startParams DeveloperInstructions = %q, want dev prompt", params.DeveloperInstructions)
	}
}

func assertStartParamsV2Config(t *testing.T, params startParams) {
	t.Helper()

	if params.Summary != "concise" {
		t.Fatalf("startParams Summary = %q, want concise", params.Summary)
	}
	if params.Effort != "high" || params.Personality != "pragmatic" {
		t.Fatalf("startParams config = %#v", params)
	}
}

func assertStartParamsLegacyProvider(t *testing.T, params startParams) {
	t.Helper()

	if params.ModelProvider != "azure" {
		t.Fatalf("legacy ModelProvider = %q, want azure", params.ModelProvider)
	}
	if params.ApprovalPolicy != "on-request" {
		t.Fatalf("legacy ApprovalPolicy = %q, want on-request", params.ApprovalPolicy)
	}
}

func assertStartParamsLegacyInstructions(t *testing.T, params startParams) {
	t.Helper()

	if params.BaseInstructions != "legacy base" {
		t.Fatalf("legacy BaseInstructions = %q, want legacy base", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "legacy dev" {
		t.Fatalf("legacy DeveloperInstructions = %q, want legacy dev", params.DeveloperInstructions)
	}
}

func assertStartParamsLegacyAgentFields(t *testing.T, params startParams) {
	t.Helper()

	if params.ParentAgentID != "agent-root" {
		t.Fatalf("legacy ParentAgentID = %q, want agent-root", params.ParentAgentID)
	}
	if params.AgentType != "worker" || params.AgentMemoryScope != "project" {
		t.Fatalf("legacy agent fields = %#v", params)
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

func TestThreadRPCParamsRejectUnknownFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		payload   string
		newTarget func() any
		want      string
	}{
		{name: "thread id", payload: `{"threadId":"thread-1","surprise":true}`, newTarget: func() any { return &threadIDParams{} }, want: `thread id: unknown field "surprise"`},
		{name: "prompt history", payload: `{"cwd":"/repo","limit":10,"surprise":true}`, newTarget: func() any { return &promptHistoryParams{} }, want: `thread/promptHistory: unknown field "surprise"`},
		{
			name:      "resume",
			payload:   `{"threadId":"thread-1","cwd":"/repo","surprise":true}`,
			newTarget: func() any { return &resumeParams{} },
			want:      `thread/resume: unknown field "surprise"`,
		},
		{
			name:      "messages",
			payload:   `{"threadId":"thread-1","limit":20,"surprise":true}`,
			newTarget: func() any { return &messagesParams{} },
			want:      `thread/messages: unknown field "surprise"`,
		},
		{
			name:      "name set",
			payload:   `{"threadId":"thread-1","name":"next","surprise":true}`,
			newTarget: func() any { return &nameSetParams{} },
			want:      `thread/name/set: unknown field "surprise"`,
		},
		{
			name:      "command",
			payload:   `{"threadId":"thread-1","args":"now","surprise":true}`,
			newTarget: func() any { return &commandParams{} },
			want:      `thread command: unknown field "surprise"`,
		},
		{
			name:      "approvals set",
			payload:   `{"threadId":"thread-1","policy":"never","surprise":true}`,
			newTarget: func() any { return &approvalsSetParams{} },
			want:      `thread approvals: unknown field "surprise"`,
		},
		{
			name:      "config get",
			payload:   `{"threadId":"thread-1","surprise":true}`,
			newTarget: func() any { return &configGetParams{} },
			want:      `thread/config/get: unknown field "surprise"`,
		},
		{
			name:      "config set",
			payload:   `{"threadId":"thread-1","model":"gpt-5.5","surprise":true}`,
			newTarget: func() any { return &configSetParams{} },
			want:      `thread/config/set: unknown field "surprise"`,
		},
		{
			name:      "model set",
			payload:   `{"threadId":"thread-1","model":"gpt-5.5","surprise":true}`,
			newTarget: func() any { return &modelSetParams{} },
			want:      `thread/model/set: unknown field "surprise"`,
		},
		{
			name:      "compact start",
			payload:   `{"threadId":"thread-1","args":"compact","surprise":true}`,
			newTarget: func() any { return &compactStartParams{} },
			want:      `thread/compact/start: unknown field "surprise"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := json.Unmarshal([]byte(tt.payload), tt.newTarget())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(%s) error = %v, want %q", tt.name, err, tt.want)
			}
		})
	}
}

func TestNormalizeStartRequestRejectsMissingProvider(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeStartRequest(StartRequest{
		BaseInstructions: "  launch me  ",
		CWD:              wantStartCWD(t),
	})
	if err == nil || !strings.Contains(err.Error(), "provider is required") {
		t.Fatalf("normalizeStartRequest() error = %v, want provider is required", err)
	}
}

func TestNormalizeStartRequestRejectsAgentIDWithLaunchIntentID(t *testing.T) {
	t.Parallel()

	_, _, err := normalizeStartRequest(StartRequest{
		AgentID: "agent_from_client", LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111",
		Provider: "codex", CWD: wantStartCWD(t),
	})
	if err == nil || !strings.Contains(err.Error(), "agent_id cannot be provided with launch_intent_id") {
		t.Fatalf("normalizeStartRequest() error = %v, want agent_id rejection", err)
	}
}

func TestNormalizeStartRequestDoesNotDeriveNameFromPrompt(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{Provider: "codex", Prompt: "  launch me  ", CWD: wantStartCWD(t)})
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
		Provider:      "codex",
		ModelProvider: "[object Object]",
		Model:         "[object Object]",
		Effort:        "undefined",
		CWD:           wantStartCWD(t),
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Model != "" || req.ModelProvider != "" || req.Effort != "" {
		t.Fatalf("normalizeStartRequest artifacts = model %q provider %q effort %q, want empty", req.Model, req.ModelProvider, req.Effort)
	}
	if req.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", req.Provider)
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

func TestStartParamsRejectsConflictingManualSkillSelectionAliases(t *testing.T) {
	t.Parallel()

	var params startParams
	err := json.Unmarshal([]byte(`{"manual_skill_selection":false,"manualSkillSelection":true}`), &params)
	if err == nil || !strings.Contains(err.Error(), `conflicting manual skill selection values`) {
		t.Fatalf("json.Unmarshal() error = %v, want manualSkillSelection conflict", err)
	}
}

func TestStartParamsRejectsInvalidManualSkillSelectionAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "camel null",
			payload: `{"manualSkillSelection":null}`,
			want:    `manualSkillSelection must be a boolean`,
		},
		{
			name:    "snake string",
			payload: `{"manual_skill_selection":"false"}`,
			want:    `manual_skill_selection must be a boolean`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var params startParams
			err := json.Unmarshal([]byte(tt.payload), &params)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("json.Unmarshal(startParams) error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestStartParamsAcceptsSelectedSkillRefsCamelCase(t *testing.T) {
	t.Parallel()

	var params startParams
	input := []byte(`{
		"cwd":"/tmp/project",
		"selectedSkills":["planner"],
		"selectedSkillRefs":[{"key":"project::planner:/tmp/project/.agent/skills/planner","name":"planner","scope":"project","path":"/tmp/project/.agent/skills/planner","source":"manual"}],
		"manualSkillSelection":true
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	assertStartParamsSelectedSkillRefCamelCase(t, params)
	assertStartParamsSelectedSkillCompatibility(t, params)
}

func TestStartParamsPreservesSelectedSkillRefSourcePerRef(t *testing.T) {
	t.Parallel()

	params := startParams{
		SelectedSkillRefs: []skillRefParams{
			{Key: "project::planner:/repo/.agent/skills/planner", Name: "planner", Scope: "project", Path: "/repo/.agent/skills/planner", Source: "manual"},
			{Key: "project::forced:/repo/.agent/skills/forced", Name: "forced", Scope: "project", Path: "/repo/.agent/skills/forced", Source: "force"},
		},
		ManualSkillSelection: true,
	}

	request := buildStartRequestFromParams(params, nil)
	if len(request.LaunchSkillRefs) != 2 {
		t.Fatalf("LaunchSkillRefs = %#v", request.LaunchSkillRefs)
	}
	if request.LaunchSkillRefs[0].Source != dto.SkillSourceManual {
		t.Fatalf("LaunchSkillRefs[0].Source = %q, want manual", request.LaunchSkillRefs[0].Source)
	}
	if request.LaunchSkillRefs[1].Source != dto.SkillSourceForce {
		t.Fatalf("LaunchSkillRefs[1].Source = %q, want force", request.LaunchSkillRefs[1].Source)
	}
}

func assertStartParamsSelectedSkillRefCamelCase(t *testing.T, params startParams) {
	t.Helper()

	if len(params.SelectedSkillRefs) != 1 {
		t.Fatalf("SelectedSkillRefs = %#v", params.SelectedSkillRefs)
	}
	ref := params.SelectedSkillRefs[0]
	if ref.Name != "planner" || ref.Scope != "project" || ref.Key == "" {
		t.Fatalf("SelectedSkillRefs[0] identity = %#v", ref)
	}
	if ref.Path != "/tmp/project/.agent/skills/planner" || ref.Source != "manual" {
		t.Fatalf("SelectedSkillRefs[0] metadata = %#v", ref)
	}
}

func assertStartParamsSelectedSkillCompatibility(t *testing.T, params startParams) {
	t.Helper()

	if len(params.SelectedSkills) != 1 {
		t.Fatalf("SelectedSkills = %#v", params.SelectedSkills)
	}
	if params.SelectedSkills[0] != "planner" || !params.ManualSkillSelection {
		t.Fatalf("selected skill compatibility fields = %#v manual=%v", params.SelectedSkills, params.ManualSkillSelection)
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

// TestNewStartResult_StalePromptKeyPropagated 守护 factory.go::newStartResult
// 里 PromptKeyStale: req.PromptKeyStale 这一行 — stale 信号 "router → wire"
// 链路的关键一跳。pickRoutedTemplate 设 req.PromptKeyStale=true，newStartResult
// 把它复制到 StartResult，buildStartResponse 再 surface 到 wire。没有这条
// 独立守护，mutation 删 factory.go 那一行不会被任何现有测试拦截
// （router_resolve_test 只测到 req 侧，rpc_types_test 只测从 StartResult 起手）。
func TestNewStartResult_StalePromptKeyPropagated(t *testing.T) {
	t.Parallel()
	staleReq := StartRequest{PromptKey: "main/missing", PromptKeyStale: true}
	staleResult := newStartResult(staleReq, "tid", "aid", "puuid", "ptid", "model", "/cwd")
	if !staleResult.PromptKeyStale {
		t.Fatalf("PromptKeyStale must propagate from req to result, got %+v", staleResult)
	}
	if staleResult.PromptKey != "main/missing" {
		t.Fatalf("PromptKey should propagate alongside stale: %q", staleResult.PromptKey)
	}
	happyReq := StartRequest{PromptKey: "main/ok", PromptKeyStale: false}
	happyResult := newStartResult(happyReq, "tid", "aid", "puuid", "ptid", "model", "/cwd")
	if happyResult.PromptKeyStale {
		t.Fatalf("PromptKeyStale must stay false when req has false: %+v", happyResult)
	}
}
