package tools

import (
	"context"
	"encoding/json"
	"fmt"

	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// compileToolOutputSchema 编译工具声明的 output_schema；空 schema 表示生产者未声明输出契约。
// schema 只在 registry 构造时编译，畸形声明在 handler 可能产生副作用前阻断启动/注册。
func compileToolOutputSchema(toolName string, schema Schema) (*jsonschema.Schema, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(map[string]any(schema))
	if err != nil {
		return nil, fmt.Errorf("tool %q output schema marshal failed: %w", toolName, err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("tool %q output schema normalization failed: %w", toolName, err)
	}
	if _, ok := document.(map[string]any); !ok {
		return nil, fmt.Errorf("tool %q output schema must be a JSON object", toolName)
	}

	compiler := jsonschema.NewCompiler()
	const resource = "mcp-orch-tool-output-schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, fmt.Errorf("tool %q output schema registration failed: %w", toolName, err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("tool %q output schema compilation failed: %w", toolName, err)
	}
	return compiled, nil
}

// withToolOutputSchema 把 output_schema 校验放在 registry handler 边界，覆盖所有 runtime 查表调用者。
// 结果先按 MCP structuredContent 规则规范化，再交给同一份声明校验；数组/标量因此保持既有 wire 兼容。
func withToolOutputSchema(def ToolDefinition, validator *jsonschema.Schema) ToolDefinition {
	if validator == nil || def.Handler == nil {
		return def
	}
	next := def.Handler
	def.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
		value, err := next(ctx, input)
		if err != nil {
			// 保留现有 handler 错误分类与 envelope 生成；common server 会负责错误结果包装。
			return value, err
		}
		if err := validateToolOutputValue(def.Name, validator, value); err != nil {
			return nil, err
		}
		return value, nil
	}
	return def
}

// validateToolOutputValue 校验工具最终进入 MCP structuredContent 的 JSON 值。
// 仅由显式 output_schema 的 wrapper 调用；无声明时不猜测任何结果形状。
func validateToolOutputValue(toolName string, validator *jsonschema.Schema, value any) error {
	if validator == nil {
		return nil
	}
	raw, err := mcpcommon.StructuredContentForToolResult(value)
	if err != nil {
		return fmt.Errorf("tool %q output encoding failed: %w", toolName, err)
	}
	var structured any
	if err := json.Unmarshal(raw, &structured); err != nil {
		return fmt.Errorf("tool %q structured output normalization failed: %w", toolName, err)
	}
	if err := validator.Validate(structured); err != nil {
		return fmt.Errorf("tool %q output does not match output schema: %w", toolName, err)
	}
	return nil
}
