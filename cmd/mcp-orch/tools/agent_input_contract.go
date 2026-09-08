package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const agentToolContractVersion = "orchestration.agent.v1"

// defineAgentTool 发布可执行的参数约束；注册表在业务副作用之前校验同一份 schema。
func defineAgentTool(name, description string, schema Schema, handler ToolHandler) ToolDefinition {
	applyAgentInputContract(name, schema)
	def := defineTool(name, description, schema, handler)
	def.Metadata.Version = agentToolContractVersion
	def.Metadata.OutputSchema = coreAgentOutputSchema(name)
	return def
}

// applyAgentInputContract 将跨参数规则放入公开 schema，避免调用者只靠长描述推断。
func applyAgentInputContract(name string, schema Schema) {
	properties := schema["properties"].(map[string]any)
	constraints := []Schema{}
	if _, ok := properties["pos"]; ok {
		constraints = append(constraints, Schema{"anyOf": []Schema{requiredAgentFields("agent_id"), requiredAgentFields("pos")}})
		properties["agent_id"].(map[string]any)["description"] = "Target persisted agent_id returned by launch_agent/list_agents. Canonical identity; never use display name or thread_id."
		properties["pos"].(map[string]any)["description"] = "Optional resource locator agent:<agent_id>; use agent_id for ordinary agent calls. If both are supplied they must match."
	}
	switch name {
	case "launch_agent":
		constraints = append(constraints, launchInputConstraints()...)
		applyLaunchInputGuide(schema, properties)
	case "list_agents":
		properties["envelope"].(map[string]any)["const"] = true
		properties["limit"].(map[string]any)["minimum"] = 0
	case "get_agent_report":
		constraints = append(constraints, requireWhen("wait", true, "after_report_seq"))
		properties["after_report_seq"].(map[string]any)["minimum"] = 0
	case "get_agent_reports":
		constraints = append(constraints, requireWhen("wait", true, "after_report_seq_by_agent"))
		properties["agent_ids"].(map[string]any)["minItems"] = 1
		properties["agent_ids"].(map[string]any)["uniqueItems"] = true
		properties["after_report_seq_by_agent"].(map[string]any)["additionalProperties"] = Schema{"type": "integer", "minimum": 0}
	}
	if field, ok := properties["timeout_ms"].(map[string]any); ok {
		field["minimum"] = 0
	}
	if len(constraints) > 0 {
		schema["allOf"] = constraints
	}
}

// launchInputConstraints 定义目录、上下文和 provider 身份联动，不替代 service 对真实父任务的校验。
func launchInputConstraints() []Schema {
	identity := []string{"codex_home", "codex_instance_key", "codex_model_provider"}
	constraints := []Schema{
		{"anyOf": []Schema{requiredAgentFields("cwd"), requiredAgentFields("parent_id")}},
		requireWhen("context_mode", "focused", "context"),
		requireWhen("context_mode", "forked", "parent_id"),
		{"if": requiredAgentFields("context"), "then": Schema{"properties": Schema{"context_mode": Schema{"const": "focused"}}, "required": []string{"context_mode"}}},
		{"if": requiredAgentFields("parent_id"), "then": Schema{"properties": Schema{"provider": Schema{"const": "codex"}}}},
	}
	for _, field := range identity {
		constraints = append(constraints, Schema{"if": Schema{"required": []string{field}}, "then": requiredAgentFields(identity...)})
	}
	return constraints
}

// requiredAgentFields 同时约束字段存在及非空，空白身份不能满足条件必填。
func requiredAgentFields(fields ...string) Schema {
	properties := Schema{}
	for _, field := range fields {
		properties[field] = Schema{"type": "string", "pattern": `\S`}
	}
	return Schema{"required": fields, "properties": properties}
}

func requireWhen(field string, value any, required string) Schema {
	return Schema{"if": Schema{"properties": Schema{field: Schema{"const": value}}, "required": []string{field}}, "then": Schema{"required": []string{required}}}
}

// withAgentInputSchema 让 stdio、HTTP 与 bootstrap 共用注册期编译的输入约束。
func withAgentInputSchema(def ToolDefinition) (ToolDefinition, error) {
	if def.Metadata.Version != agentToolContractVersion {
		return def, nil
	}
	if len(def.InputSchema) == 0 {
		return def, fmt.Errorf("agent tool %s requires input schema", def.Name)
	}
	validator, err := compileToolOutputSchema(def.Name, def.InputSchema)
	if err != nil {
		return def, fmt.Errorf("input contract: %w", err)
	}
	next := def.Handler
	def.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		var value any
		if err := json.Unmarshal(input, &value); err != nil {
			return nil, err
		}
		if err := validator.Validate(value); err != nil {
			return nil, mcpcommon.NewCodedToolError("invalid_arguments", err, false, "next: correct arguments using this tool's inputSchema; no action was submitted")
		}
		if next == nil {
			return nil, fmt.Errorf("tool %s handler is not configured", def.Name)
		}
		return next(ctx, input)
	}
	return def, nil
}

// applyLaunchInputGuide 使用标准 JSON Schema 注解，在 tools/list 中公开常用调用与发现路径。
func applyLaunchInputGuide(schema Schema, properties map[string]any) {
	schema["title"] = "Launch a managed agent"
	schema["description"] = "Start with Task: name + prompt + absolute cwd; standalone launches also require an explicit configured model. Context: omit for a fresh prompt, use focused + context for selected facts, or forked + parent_id for an existing parent thread. Expert: discover enabled templates with prompt_list, inspect one with prompt_get, then copy its exact prompt_key; name is only a display label. Runtime: call list_models(provider=codex) and copy a name from data[].models. Standalone requires that explicit model; peer launches may omit it for provider-side configuration/resolution. Provider identity overrides are advanced and require the complete codex_home/codex_instance_key/codex_model_provider triple. Examples below are ordinary, focused read-only, parent fork, and template launch; replace example paths, model names, parent IDs and template keys with real discovered values."
	schema["examples"] = []any{
		map[string]any{"model": "discovered-model-name", "name": "review", "prompt": "Review the changed files and report findings.", "cwd": "/absolute/project"},
		map[string]any{"model": "discovered-model-name", "name": "verify", "prompt": "Verify the fix.", "cwd": "/absolute/project", "context_mode": "focused", "context": "Inspect the current diff; do not edit files.", "read_only": true},
		map[string]any{"model": "discovered-model-name", "name": "follow-up", "prompt": "Verify the remaining acceptance criteria.", "parent_id": "parent-agent-id", "context_mode": "forked"},
		map[string]any{"model": "discovered-model-name", "name": "expert", "prompt": "Review this change using the selected expert instructions.", "cwd": "/absolute/project", "prompt_key": "discovered-prompt-key"},
	}
	groups := map[string][]string{
		"Task":                         {"name", "prompt", "cwd", "agent_id"},
		"Context":                      {"context_mode", "context", "parent_id"},
		"Expert":                       {"prompt_key", "agent_key", "agent_type"},
		"Runtime":                      {"provider", "model", "effort"},
		"Provider identity":            {"codex_home", "codex_instance_key", "codex_model_provider"},
		"Restrictions and preferences": {"read_only", "disabled_tools", "memory_scope", "language"},
	}
	for group, fields := range groups {
		for _, field := range fields {
			properties[field].(map[string]any)["title"] = group + ": " + strings.ReplaceAll(field, "_", " ")
		}
	}
	properties["prompt_key"].(map[string]any)["description"] = "Select the exact prompt_template.prompt_key: call prompt_list, choose an enabled template, optionally inspect it with prompt_get(prompt_key=...), and copy that prompt_key here. Prefer this over agent_key when selecting an expert; multiple templates may share an agent_key."
	properties["agent_key"].(map[string]any)["description"] = "Advanced template routing identity, distinct from agent_id and display name. Omit when selecting a specific template by prompt_key; use an agent_key discovered from prompt_list only when router selection is intended. The selected template injects assembled sections as base_instructions."
	properties["model"].(map[string]any)["description"] = "Model identity. Required by standalone execution: call list_models(provider=codex), check data[].available, and copy a string from data[].models. Peer execution may omit model for provider-side configuration/resolution. Do not infer a model from name, agent_key, or prompt_key."
	properties["effort"].(map[string]any)["description"] = "Optional reasoning effort supported by the selected model/provider. Omit unless a specific effort is needed and known to be supported; list_models lists model names, not effort capabilities."
}
