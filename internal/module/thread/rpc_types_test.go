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
	if params.Prompt != "legacy prompt" {
		t.Fatalf("legacy prompt = %q, want %q", params.Prompt, "legacy prompt")
	}
}

func TestStartParamsMarshalUsesSnakeCase(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(startParams{
		ModelProvider:         "openai",
		ApprovalPolicy:        "never",
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

func TestNormalizeStartRequestDefaultsProvider(t *testing.T) {
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
	if req.Prompt != "launch me" {
		t.Fatalf("prompt = %q, want %q", req.Prompt, "launch me")
	}
	if agentID == "" || req.AgentID == "" {
		t.Fatalf("agent id = %q, want generated id", agentID)
	}
}
