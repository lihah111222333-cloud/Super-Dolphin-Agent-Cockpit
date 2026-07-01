package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// requiredField 是字段校验的名称/值对。
type requiredField struct {
	Name  string
	Value string
}

// rawJSONOptions 控制 marshalRawJSON 的序列化行为。
type rawJSONOptions struct {
	EmptyObject     bool
	OmitEmptyString bool
}

// resourceToolSpec 描述用于生成 list/get 工具对的配置规格。
type resourceToolSpec struct {
	ListName        string
	ListDescription string
	GetName         string
	GetDescription  string
	KeyField        string
	KeyDescription  string
	ListHandler     ToolHandler
	GetHandler      ToolHandler
}

// makeHandler 构造一个 ToolHandler：先校验依赖、再解码 JSON 输入、最后调用 exec。
func makeHandler[T any, R any](
	dependency any,
	dependencyName string,
	exec func(context.Context, T) (R, error),
) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		if err := requireDependency(dependency, dependencyName); err != nil {
			return nil, err
		}
		var in T
		if err := shared.DecodeInput(input, &in); err != nil {
			return nil, err
		}
		return exec(ctx, in)
	}
}

// defineTool 创建基础工具定义（无治理元数据）。
func defineTool(name, description string, schema Schema, handler ToolHandler) ToolDefinition {
	return ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema,
		Handler:     handler,
	}
}

// defineGovernedTool 创建含治理元数据的工具定义。
func defineGovernedTool(name, description string, schema Schema, handler ToolHandler, metadata ToolMetadata) ToolDefinition {
	def := defineTool(name, description, schema, handler)
	def.Metadata = metadata
	return def
}

var toolsRequiringPathPolicy = map[string]bool{
	"shared_file_write": true,
	"tts_generate":      true,
	"av_merge":          true,
	"video_with_audio":  true,
}

// validateRegistryPathPolicies 在 registry 构造期拒绝缺少路径策略的本地写入/媒体工具。
// 这层是启动期护栏，防止新增本地文件写工具时忘记声明 handler 前置校验字段。
func validateRegistryPathPolicies(defs []ToolDefinition) error {
	for _, def := range defs {
		if !toolsRequiringPathPolicy[def.Name] {
			continue
		}
		if def.Metadata.PathPolicy.PathAuthority == ToolPathAuthorityNone {
			return fmt.Errorf("tool %s requires ToolPathPolicy", def.Name)
		}
		if len(def.Metadata.PathPolicy.ReadFields)+len(def.Metadata.PathPolicy.WriteFields) == 0 {
			return fmt.Errorf("tool %s path policy requires read_fields or write_fields", def.Name)
		}
	}
	return nil
}

// withToolPathPolicy 包装工具 handler，在业务逻辑运行前先按 metadata 校验路径字段。
// handler 仍负责把受控 ref 解析为实际路径；这里先拦住 absolute/traversal/home 等明显越界输入。
func withToolPathPolicy(def ToolDefinition) ToolDefinition {
	policy := def.Metadata.PathPolicy
	if policy.PathAuthority == ToolPathAuthorityNone || def.Handler == nil {
		return def
	}
	next := def.Handler
	def.Handler = func(ctx context.Context, input json.RawMessage) (any, error) {
		if err := validateToolPathPolicyInput(policy, input); err != nil {
			return nil, err
		}
		return next(ctx, input)
	}
	return def
}

// validateToolPathPolicyInput 根据工具 metadata 校验输入中的路径字段。
// 它只做权限边界检查，不把 ref 解析为真实路径，避免 registry 层承担业务副作用。
func validateToolPathPolicyInput(policy ToolPathPolicy, input json.RawMessage) error {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		return fmt.Errorf("invalid input: %w", err)
	}
	for _, field := range policy.ReadFields {
		if err := validateToolPathPolicyField(policy, field, fields[field], false); err != nil {
			return err
		}
	}
	for _, field := range policy.WriteFields {
		if err := validateToolPathPolicyField(policy, field, fields[field], true); err != nil {
			return err
		}
	}
	return nil
}

// validateToolPathPolicyField 校验单个路径字段是否符合声明的读写权限。
// 读写方向由 ToolPathPolicy 指定，缺失或空字符串由具体工具决定是否必填。
func validateToolPathPolicyField(policy ToolPathPolicy, field string, raw any, write bool) error {
	if raw == nil {
		return nil
	}
	value, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%s must be a string path", field)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	switch policy.PathAuthority {
	case ToolPathAuthoritySharedFile:
		return validateSharedFilePolicyPath(field, value, write)
	case ToolPathAuthorityWorkspaceRelative:
		_, err := cleanWorkspaceRelativePath(value)
		if err != nil {
			return fmt.Errorf("%s violates workspace_relative path policy: %w", field, err)
		}
		return nil
	case ToolPathAuthoritySharedOrWorkspace:
		if sharedPath, ok := strings.CutPrefix(value, "shared:"); ok {
			return validateSharedFilePolicyPath(field, sharedPath, write)
		}
		_, err := cleanWorkspaceRelativePath(value)
		if err != nil {
			return fmt.Errorf("%s violates sharedfile_or_workspace_relative path policy: %w", field, err)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported path authority %q", field, policy.PathAuthority)
	}
}

func validateSharedFilePolicyPath(field, value string, write bool) error {
	var err error
	if write {
		_, err = sharedfilepath.ValidateAgentWritePath(value)
	} else {
		_, err = sharedfilepath.ValidateReadPath(value)
	}
	if err != nil {
		return fmt.Errorf("%s violates sharedfile path policy: %w", field, err)
	}
	return nil
}

// cleanWorkspaceRelativePath 归一化工作区相对路径并拒绝逃逸形式。
// 这里不拼接根目录，只负责把 absolute、home、traversal 这类越权输入挡在工具执行前。
func cleanWorkspaceRelativePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("path is empty")
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	if strings.HasPrefix(normalized, "~") || strings.HasPrefix(normalized, "/") || filepath.IsAbs(normalized) {
		return "", errors.New("absolute or home path not allowed")
	}
	cleaned := filepath.ToSlash(filepath.Clean(normalized))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path traversal not allowed")
	}
	return cleaned, nil
}

// buildToolDefinitions 将多个 ToolDefinition 合并为切片，便于注册。
func buildToolDefinitions(defs ...ToolDefinition) []ToolDefinition {
	return defs
}

// resourceToolDefinitions 根据 spec 创建一对 list/get 工具定义。
func resourceToolDefinitions(spec resourceToolSpec) []ToolDefinition {
	return buildToolDefinitions(
		defineTool(spec.ListName, spec.ListDescription, ObjectSchema(map[string]Schema{
			"keyword":  StringSchema("Search keyword (optional)."),
			"envelope": BooleanSchema("When true, return an object envelope with data/total/showing/truncated/hint while preserving the legacy item field."),
		}), spec.ListHandler),
		defineTool(spec.GetName, spec.GetDescription, ObjectSchema(map[string]Schema{
			"pos":         StringSchema(resourcePosDescription(spec.KeyField)),
			spec.KeyField: StringSchema(spec.KeyDescription),
		}), spec.GetHandler),
	)
}

// resourcePosDescription 根据 keyField 返回 pos 参数的描述文本。
func resourcePosDescription(keyField string) string {
	switch strings.TrimSpace(keyField) {
	case "prompt_key":
		return "Flattened prompt locator, e.g. prompt:<prompt_key>. Preferred over legacy prompt_key."
	case "card_key":
		return "Flattened command-card locator, e.g. command:<card_key>. Preferred over legacy card_key."
	default:
		return "Flattened resource locator. Preferred over the legacy key field."
	}
}

// requireDependency 检查依赖是否已注入，nil/nil 接口均视为未配置。
func requireDependency(dependency any, name string) error {
	if name == "" {
		return nil
	}
	if dependency == nil {
		return errors.New(name + " is not configured")
	}
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return errors.New(name + " is not configured")
		}
	}
	return nil
}

// requireFields 逐个校验字段是否非空，首个空字段立即返回错误。
func requireFields(fields ...requiredField) error {
	for _, field := range fields {
		if strings.TrimSpace(field.Value) == "" {
			return errors.New(field.Name + " is required")
		}
	}
	return nil
}

// requireTrimmed 校验并返回 trim 后的字段值，空串时返回 required 错误。
func requireTrimmed(value, field string) (string, error) {
	if err := requireFields(requiredField{Name: field, Value: value}); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// requireEnum 给 handler 层做 enum 字符串校验，与 StringSchema enum 共用同一份 allowed。
//   - value 为空（trim 后）→ 返 "<field> is required" 错（与 requireTrimmed 同语义，但
//     调用方只在「该字段必填且需校验枚举」场景使用）。
//   - 不在 allowed 内 → 返中英双语错误，列出 allowed 候选值。
//   - 命中 → 返 trim 后的值。
func requireEnum(value, field string, allowed []string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New(field + " is required")
	}
	for _, candidate := range allowed {
		if trimmed == candidate {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf(
		"%s 取值非法：%q，必须是 %v 之一 (invalid %s %q: must be one of %v)",
		field, trimmed, allowed, field, trimmed, allowed,
	)
}

// loadOrNotFound 把 not-found 错误或 nil 值统一转为 "kind id not found" 错误。
func loadOrNotFound[T any](value *T, err error, kind, id string) (*T, error) {
	if err != nil {
		if platformdb.IsNotFound(err) {
			return nil, fmt.Errorf("%s %s not found", kind, id)
		}
		return nil, err
	}
	if value == nil {
		return nil, fmt.Errorf("%s %s not found", kind, id)
	}
	return value, nil
}

// normalizeListLimit 规范化列表上限：0 或超限时使用默认值。
func normalizeListLimit(limit, defaultLimit, maxLimit int) int {
	if limit <= 0 || (maxLimit > 0 && limit > maxLimit) {
		return defaultLimit
	}
	return limit
}

// marshalRawJSON 把工具输入中的结构化值编码成 RawMessage。
// 空字符串/空对象是否保留由 opts 控制，避免调用方各自实现不同的 nil/{} 语义。
func marshalRawJSON(value any, opts rawJSONOptions) (json.RawMessage, error) {
	switch current := value.(type) {
	case string:
		trimmed := strings.TrimSpace(current)
		if opts.OmitEmptyString && trimmed == "" {
			return nil, nil
		}
		value = trimmed
	case map[string]any:
		if len(current) == 0 && opts.EmptyObject {
			return json.RawMessage("{}"), nil
		}
	}
	if value == nil {
		if opts.EmptyObject {
			return json.RawMessage("{}"), nil
		}
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
