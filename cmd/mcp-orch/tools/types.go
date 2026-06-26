package tools

import (
	"context"
	"encoding/json"
)

// ToolHandler 是 MCP 工具的处理函数签名，接收原始 JSON 输入并返回结果。
type ToolHandler func(ctx context.Context, input json.RawMessage) (any, error)

// Schema 是 MCP 工具参数或输出的 JSON schema 表示。
type Schema map[string]any

// ToolRiskClass 标记工具调用的治理风险等级，供 registry 和 policy 审计使用。
type ToolRiskClass string

// 工具风险等级常量，用于治理元数据和审计策略。
const (
	// ToolRiskLow 表示只读或低影响操作。
	ToolRiskLow ToolRiskClass = "low"
	// ToolRiskMedium 表示会改变局部状态但不触发高危边界的操作。
	ToolRiskMedium ToolRiskClass = "medium"
	// ToolRiskHigh 表示会写工作流、执行命令或写共享文件等高影响操作。
	ToolRiskHigh ToolRiskClass = "high"
)

// ToolPermission 描述工具调用需要的最小权限。
type ToolPermission string

// 工具权限常量，用于声明调用方需要的最小权限。
const (
	// ToolPermissionWorkflowRead 允许读取工作流定义和运行态。
	ToolPermissionWorkflowRead ToolPermission = "workflow.read"
	// ToolPermissionWorkflowWrite 允许修改工作流定义、运行态或调度状态。
	ToolPermissionWorkflowWrite ToolPermission = "workflow.write"
	// ToolPermissionSharedFileWrite 允许写入 workflow shared-file 根目录。
	ToolPermissionSharedFileWrite ToolPermission = "shared_file.write"
	// ToolPermissionCommandExecute 允许执行受策略约束的命令卡。
	ToolPermissionCommandExecute ToolPermission = "command.execute"
)

// ToolWorkspaceScope 描述工具能触达的工作区范围。
type ToolWorkspaceScope string

// 工具工作区范围常量，用于约束工具可触达的状态或文件边界。
const (
	// ToolWorkspaceScopeNone 表示工具不直接访问本地工作区。
	ToolWorkspaceScopeNone ToolWorkspaceScope = "none"
	// ToolWorkspaceScopeWorkflow 表示工具访问 orchestration 工作流状态。
	ToolWorkspaceScopeWorkflow ToolWorkspaceScope = "workflow"
	// ToolWorkspaceScopeSharedFile 表示工具访问 workflow shared-file 根目录。
	ToolWorkspaceScopeSharedFile ToolWorkspaceScope = "shared_file"
	// ToolWorkspaceScopeAllowedRoots 表示工具必须限制在显式允许的工作区根目录内。
	ToolWorkspaceScopeAllowedRoots ToolWorkspaceScope = "allowed_roots"
)

// ToolIdempotencyRequirement 描述调用方是否必须提供幂等键或等价保护。
type ToolIdempotencyRequirement string

// 工具幂等性要求常量，用于提示调用方重试时是否必须提供保护。
const (
	// ToolIdempotencyNone 表示该工具不要求额外幂等保护。
	ToolIdempotencyNone ToolIdempotencyRequirement = "none"
	// ToolIdempotencyRecommended 表示重试时建议提供幂等键。
	ToolIdempotencyRecommended ToolIdempotencyRequirement = "recommended"
	// ToolIdempotencyRequired 表示调用方必须提供幂等保护。
	ToolIdempotencyRequired ToolIdempotencyRequirement = "required"
)

// ToolRedactionPolicy 描述审计事件对入参和结果的脱敏策略。
type ToolRedactionPolicy string

// 工具脱敏策略常量，用于控制审计事件记录入参和结果的粒度。
const (
	// ToolRedactionNone 表示审计层不做额外脱敏。
	ToolRedactionNone ToolRedactionPolicy = "none"
	// ToolRedactionMetadataOnly 表示审计只记录摘要元数据，不记录完整入参或结果。
	ToolRedactionMetadataOnly ToolRedactionPolicy = "metadata_only"
	// ToolRedactionSensitiveFields 表示审计需要按敏感字段名脱敏。
	ToolRedactionSensitiveFields ToolRedactionPolicy = "sensitive_fields"
)

// ToolMetadata 是工具注册表里的治理元数据；审批 MVP 未实现前 ApprovalRequired 必须为 false。
type ToolMetadata struct {
	Version                string                     `json:"version"`
	OutputSchema           Schema                     `json:"output_schema"`
	Capabilities           []string                   `json:"capabilities"`
	RiskClass              ToolRiskClass              `json:"risk_class"`
	Permission             ToolPermission             `json:"permission"`
	WorkspaceScope         ToolWorkspaceScope         `json:"workspace_scope"`
	TimeoutSeconds         int                        `json:"timeout_seconds,omitempty"`
	IdempotencyRequirement ToolIdempotencyRequirement `json:"idempotency_requirement"`
	ApprovalRequired       bool                       `json:"approval_required"`
	AuditEventType         string                     `json:"audit_event_type"`
	RedactionPolicy        ToolRedactionPolicy        `json:"redaction_policy"`
}

// ToolDefinition 是注册到 Registry 的工具完整定义，含 schema、handler 和治理元数据。
type ToolDefinition struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	InputSchema Schema       `json:"input_schema"`
	Metadata    ToolMetadata `json:"metadata,omitempty"`
	Handler     ToolHandler  `json:"-"`
}

// listEnvelope 是列表工具返回的通用分页包装对象。
type listEnvelope[T any] struct {
	Data      []T    `json:"data"`
	Total     int    `json:"total"`
	Showing   int    `json:"showing"`
	Truncated bool   `json:"truncated"`
	Hint      string `json:"hint,omitempty"`
}

// newListEnvelope 构造分页包装对象，limit > 0 且达到上限时标记 Truncated。
func newListEnvelope[T any](items []T, limit int, hint string) listEnvelope[T] {
	return listEnvelope[T]{
		Data:      items,
		Total:     len(items),
		Showing:   len(items),
		Truncated: limit > 0 && len(items) >= limit,
		Hint:      hint,
	}
}

// successResult 构造带 success:true 标志的结果 map。
func successResult(fields map[string]any) map[string]any {
	result := map[string]any{"success": true}
	for key, value := range fields {
		if value != nil {
			result[key] = value
		}
	}
	return result
}

// StringSchema 构建字符串参数 schema。
func StringSchema(description string) Schema {
	return scalarSchema("string", description)
}

// IntegerSchema 构建整数参数 schema。
func IntegerSchema(description string) Schema {
	return scalarSchema("integer", description)
}

// BooleanSchema 构建布尔参数 schema。
func BooleanSchema(description string) Schema {
	return scalarSchema("boolean", description)
}

// EnumStringSchema 构建枚举字符串参数 schema。
func EnumStringSchema(description string, values ...string) Schema {
	schema := StringSchema(description)
	schema["enum"] = append([]string(nil), values...)
	return schema
}

// EnumValues 从 Schema 反取 "enum" 字段，供 handler 层 requireEnum 复用 schema 枚举。
// schema 和 handler 共用同一份值域，避免工具描述允许但运行时拒绝的漂移。
//
// 仅识别 []string 与 []any（元素为 string）两种形状；其他类型直接返 nil，
// 调用方应保证 schema 用 EnumStringSchema 构造（已在单测覆盖）。
func EnumValues(s Schema) []string {
	if s == nil {
		return nil
	}
	raw, ok := s["enum"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, str)
		}
		return out
	default:
		return nil
	}
}

// ArraySchema 构建数组参数 schema。
func ArraySchema(items Schema, description string) Schema {
	schema := Schema{"type": "array", "items": map[string]any(items)}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

// ObjectSchema 构建对象参数 schema。
func ObjectSchema(properties map[string]Schema, required ...string) Schema {
	schema := Schema{
		"type":                 "object",
		"properties":           schemaProperties(properties),
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

// RawObjectSchema 构建允许透传任意字段的对象 schema。
// 仅用于 metadata/config/patch 这类跨层 raw JSON，普通工具入参应优先使用 ObjectSchema 的封闭字段集。
func RawObjectSchema(description string) Schema {
	schema := Schema{"type": "object", "additionalProperties": true}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

// scalarSchema 构建指定类型的 scalar schema，含可选描述。
func scalarSchema(kind, description string) Schema {
	schema := Schema{"type": kind}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

// schemaProperties 把 map[string]Schema 转换为 map[string]any 以便 JSON 序列化。
func schemaProperties(properties map[string]Schema) map[string]any {
	mapped := make(map[string]any, len(properties))
	for key, value := range properties {
		mapped[key] = map[string]any(value)
	}
	return mapped
}
