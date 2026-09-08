//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// TestStandaloneAgentContract 验证新编译 binary 的公开 schema、参数拒绝和列表对象；不调用付费模型。
func TestStandaloneAgentContract(t *testing.T) {
	binary := buildServeE2EBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	fixture := writeServeE2EFixture(t, binary)
	daemon := startServeE2E(t, ctx, binary, fixture)
	client := &http.Client{Timeout: 5 * time.Second}
	session, _ := initializeServeE2E(t, client, daemon.ready.Endpoint, fixture.token)
	listed := callServeE2E(t, client, daemon.ready.Endpoint, fixture.token, session, 2, "tools/list", map[string]any{})
	var catalog struct {
		Tools []struct {
			Name         string
			Description  string         `json:"description"`
			OutputSchema map[string]any `json:"outputSchema"`
			InputSchema  map[string]any `json:"inputSchema"`
		}
	}
	if err := json.Unmarshal(listed.Result, &catalog); err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]map[string]any)
	for _, tool := range catalog.Tools {
		assertAgentInterruptDescription(t, tool.Name, tool.Description)
		schemas[tool.Name] = tool.OutputSchema
		if tool.Name == "launch_agent" {
			assertLaunchGuideWireContract(t, tool.InputSchema)
		}
	}
	for _, name := range []string{"launch_agent", "send_message", "list_agents", "stop_agent", "interrupt_agent", "recover_agent", "get_agent_report", "get_agent_reports"} {
		if len(schemas[name]) == 0 {
			t.Fatalf("binary tools/list omitted %s output contract", name)
		}
	}
	cases := []struct {
		name      string
		arguments map[string]any
	}{
		{"launch_agent", map[string]any{"name": "missing-cwd", "model": "test-model"}},
		{"launch_agent", map[string]any{"name": "missing-model", "cwd": fixture.workspace}},
		{"launch_agent", map[string]any{"name": "focused", "cwd": fixture.workspace, "context_mode": "focused"}},
		{"get_agent_report", map[string]any{"agent_id": "missing", "wait": true}},
		{"send_message", map[string]any{"message": "missing-target"}},
		{"list_agents", map[string]any{"envelope": false}},
	}
	for i, tc := range cases {
		reply := callServeE2E(t, client, daemon.ready.Endpoint, fixture.token, session, 10+i, "tools/call", map[string]any{"name": tc.name, "arguments": tc.arguments})
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("%s accepted invalid arguments: %s", tc.name, reply.Result)
		}
	}
	assertLaunchDiscoveryToolsAvailable(t, client, daemon.ready.Endpoint, fixture.token, session)
	raw := callServeToolStructured(t, client, daemon.ready.Endpoint, fixture.token, session, 30, "list_agents", map[string]any{})
	assertAgentListWireContract(t, raw, schemas["list_agents"])
	if err := stopServeProcessE2E(t, daemon, nil); err != nil {
		t.Fatal(err)
	}
	assertServeEndpointClosed(t, client, daemon.ready.Endpoint)
}

// assertAgentInterruptDescription 验证真实工具发现面明确区分中断turn与终止后台资源。
func assertAgentInterruptDescription(t *testing.T, name, description string) {
	t.Helper()
	if name != "interrupt_agent" {
		return
	}
	for _, fragment := range []string{"current turn", "Background commands may keep running", "stop_agent"} {
		if !strings.Contains(description, fragment) {
			t.Fatalf("interrupt description omitted lifecycle boundary %q", fragment)
		}
	}
}

// assertAgentListWireContract 同时验证真实 wire JSON 与公开 outputSchema，避免只验证注册表存在。
func assertAgentListWireContract(t *testing.T, raw json.RawMessage, schema map[string]any) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("agent-list.json", schema); err != nil {
		t.Fatal(err)
	}
	validator, err := compiler.Compile("agent-list.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(value); err != nil {
		t.Fatalf("wire result violates outputSchema: %v", err)
	}
	var result struct {
		Agents         []any
		Total, Showing int
		Truncated      bool
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 || result.Showing != 0 || result.Truncated || result.Agents == nil {
		t.Fatalf("unexpected empty list: %s", raw)
	}
}

// assertLaunchGuideWireContract 校验 binary tools/list 实际发送的标准注解与参数示例。
func assertLaunchGuideWireContract(t *testing.T, schema map[string]any) {
	t.Helper()
	required, ok := schema["required"].([]any)
	if !ok || !slices.Contains(required, any("model")) {
		t.Fatal("standalone wire launch schema omitted required model")
	}
	description, ok := schema["description"].(string)
	if !ok {
		t.Fatal("launch inputSchema omitted discovery description")
	}
	for _, name := range []string{"prompt_list", "prompt_get", "list_models(provider=codex)"} {
		if !strings.Contains(description, name) {
			t.Errorf("wire discovery guide missing %s", name)
		}
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("launch inputSchema omitted properties")
	}
	for name, raw := range properties {
		title, ok := raw.(map[string]any)["title"].(string)
		if !ok || !strings.Contains(title, ": ") {
			t.Errorf("wire launch property %s omitted group title", name)
		}
	}
	assertLaunchWireExamples(t, schema)
}

// assertLaunchWireExamples 用同一公开 schema 验证全部 wire examples，防止示例漂移。
func assertLaunchWireExamples(t *testing.T, schema map[string]any) {
	t.Helper()
	examples, ok := schema["examples"].([]any)
	if !ok || len(examples) != 4 {
		t.Fatalf("wire launch examples=%#v", schema["examples"])
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("launch-input.json", schema); err != nil {
		t.Fatal(err)
	}
	validator, err := compiler.Compile("launch-input.json")
	if err != nil {
		t.Fatal(err)
	}
	for i, example := range examples {
		if err := validator.Validate(example); err != nil {
			t.Errorf("wire launch example %d invalid: %v", i, err)
		}
	}
}

// assertLaunchDiscoveryToolsAvailable 真实调用发现入口，验证 model 与模板依赖确实注入。
func assertLaunchDiscoveryToolsAvailable(t *testing.T, client *http.Client, endpoint, token, session string) {
	t.Helper()
	models := callServeToolStructured(t, client, endpoint, token, session, 40, "list_models", map[string]any{"provider": "codex"})
	assertLaunchDiscoveredModels(t, models)
	raw := callServeToolStructured(t, client, endpoint, token, session, 41, "prompt_list", map[string]any{"envelope": true})
	var listed struct {
		Data []struct {
			PromptKey string `json:"prompt_key"`
			Enabled   bool
		}
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	for _, item := range listed.Data {
		if item.Enabled && item.PromptKey != "" {
			callServeToolStructured(t, client, endpoint, token, session, 42, "prompt_get", map[string]any{"prompt_key": item.PromptKey})
			return
		}
	}
	t.Fatal("prompt_list did not expose an enabled template for prompt_get discovery")
}

// assertLaunchDiscoveredModels 校验文案指定的真实 data[].models 路径与 provider 可用标记。
func assertLaunchDiscoveredModels(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var result struct {
		Data []struct {
			Provider  string
			Models    []string
			Available bool
		}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("codex discovery returned %s", raw)
	}
	provider := result.Data[0]
	if provider.Provider != "codex" || !provider.Available || len(provider.Models) == 0 {
		t.Fatalf("codex discovery has no selectable model: %s", raw)
	}
}
