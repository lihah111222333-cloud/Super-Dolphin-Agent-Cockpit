package tools

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAgentInputSchemaRejectsAmbiguousCalls(t *testing.T) {
	cases := []struct{ tool, input string }{
		{"launch_agent", `{"name":"review"}`},
		{"launch_agent", `{"name":"review","cwd":"/tmp","context_mode":"focused"}`},
		{"launch_agent", `{"name":"review","cwd":"/tmp","context_mode":"forked"}`},
		{"launch_agent", `{"name":"review","cwd":"/tmp","context":"unscoped history"}`},
		{"launch_agent", `{"name":"review","cwd":"/tmp","codex_home":"/tmp/codex"}`},
		{"send_message", `{"message":"continue"}`},
		{"get_agent_report", `{"agent_id":"a","wait":true}`},
		{"get_agent_reports", `{"agent_ids":["a"],"wait":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool+tc.input, func(t *testing.T) {
			def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), tc.tool)
			validator, err := compileToolOutputSchema(tc.tool, def.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatal(err)
			}
			if err := validator.Validate(value); err == nil {
				t.Fatalf("schema accepted ambiguous call: %s", tc.input)
			}
		})
	}
}

func TestLaunchInputFieldsMatchPublishedSchema(t *testing.T) {
	def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), "launch_agent")
	properties := def.InputSchema["properties"].(map[string]any)
	producer := reflect.TypeFor[LaunchAgentInput]()
	seen := make(map[string]bool)
	for i := 0; i < producer.NumField(); i++ {
		name := strings.Split(producer.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			t.Fatalf("unmapped input field %s", producer.Field(i).Name)
		}
		if _, ok := properties[name]; !ok {
			t.Errorf("missing schema field %s", name)
		}
		seen[name] = true
	}
	for name := range properties {
		if !seen[name] {
			t.Errorf("stale schema field %s", name)
		}
	}
}

func TestAgentRegistryValidatesBeforeSideEffects(t *testing.T) {
	def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), "launch_agent")
	called := false
	def.Handler = func(context.Context, json.RawMessage) (any, error) { called = true; return nil, nil }
	registry := newRegistryFromToolDefinitions(newToolRuntimeState(), []ToolDefinition{def})
	tool, ok := registry.Lookup("launch_agent")
	if !ok {
		t.Fatal("missing launch_agent")
	}
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"name":"review"}`))
	if err == nil || called {
		t.Fatalf("invalid call reached handler: called=%v err=%v", called, err)
	}
}

func TestAgentInputSchemaAcceptsCommonWorkflows(t *testing.T) {
	cases := []struct{ tool, input string }{
		{"launch_agent", `{"name":"review","prompt":"Review the diff","cwd":"/tmp"}`},
		{"launch_agent", `{"name":"review","parent_id":"parent","context_mode":"forked"}`},
		{"launch_agent", `{"name":"review","cwd":"/tmp","context_mode":"focused","context":"Only inspect the diff","read_only":true}`},
		{"launch_agent", `{"name":"review","cwd":"/tmp","codex_home":"/tmp/codex","codex_instance_key":"instance","codex_model_provider":"openai"}`},
		{"send_message", `{"agent_id":"a","message":"continue","wait_report":true}`},
		{"get_agent_report", `{"agent_id":"a","wait":true,"after_report_seq":0}`},
		{"get_agent_reports", `{"agent_ids":["a"],"wait":true,"after_report_seq_by_agent":{"a":3}}`},
		{"list_agents", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.tool+tc.input, func(t *testing.T) {
			def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), tc.tool)
			validator, err := compileToolOutputSchema(tc.tool, def.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal([]byte(tc.input), &value); err != nil {
				t.Fatal(err)
			}
			if err := validator.Validate(value); err != nil {
				t.Fatalf("valid workflow rejected: %v", err)
			}
		})
	}
}

// TestLaunchInputGuideExamples 验证可发现字段分组和标准 examples 与生产参数契约一致。
func TestLaunchInputGuideExamples(t *testing.T) {
	def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), "launch_agent")
	validator, err := compileToolOutputSchema(def.Name, def.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	examples, ok := def.InputSchema["examples"].([]any)
	if !ok || len(examples) != 4 {
		t.Fatalf("launch examples=%#v", def.InputSchema["examples"])
	}
	for i, example := range examples {
		if err := validator.Validate(example); err != nil {
			t.Fatalf("example %d invalid: %v", i, err)
		}
	}
	assertLaunchGuideFieldTitles(t, def.InputSchema)
	for _, marker := range []string{"prompt_list", "prompt_get", "list_models(provider=codex)", "name is only a display label"} {
		if !strings.Contains(def.InputSchema["description"].(string), marker) {
			t.Errorf("launch discovery missing %q", marker)
		}
	}
}

// assertLaunchGuideFieldTitles 动态枚举输入字段并验证每项具有可见分组标题。
func assertLaunchGuideFieldTitles(t *testing.T, schema Schema) {
	t.Helper()
	fields := schema["properties"].(map[string]any)
	producer := reflect.TypeFor[LaunchAgentInput]()
	for i := 0; i < producer.NumField(); i++ {
		name := strings.Split(producer.Field(i).Tag.Get("json"), ",")[0]
		field, ok := fields[name].(map[string]any)
		if !ok {
			t.Fatalf("missing launch property %s", name)
		}
		title, ok := field["title"].(string)
		if !ok || !strings.Contains(title, ": ") {
			t.Errorf("ungrouped launch field %s", name)
		}
	}
}
