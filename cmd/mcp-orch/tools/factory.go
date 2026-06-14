package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type requiredField struct {
	Name  string
	Value string
}

type rawJSONOptions struct {
	EmptyObject     bool
	OmitEmptyString bool
}

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

func defineTool(name, description string, schema Schema, handler ToolHandler) ToolDefinition {
	return ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema,
		Handler:     handler,
	}
}

func buildToolDefinitions(defs ...ToolDefinition) []ToolDefinition {
	return defs
}

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

func requireFields(fields ...requiredField) error {
	for _, field := range fields {
		if strings.TrimSpace(field.Value) == "" {
			return errors.New(field.Name + " is required")
		}
	}
	return nil
}

func requireTrimmed(value, field string) (string, error) {
	if err := requireFields(requiredField{Name: field, Value: value}); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// requireEnum 给 handler 层做兜底的 enum 字符串校验，与 StringSchema enum
// 共用同一份 allowed（通过 EnumValues 从 schema 反取，单源驱动）。
//   - value 为空（trim 后）→ 返 "<field> is required" 错（与 requireTrimmed 同语义，但
//     调用方只在「该字段必填且需校验枚举」场景使用）。
//   - 不在 allowed 内 → 返中英双语错误，列出 allowed 候选值。
//   - 命中 → 返 trim 后的值。
//
// requireEnum is the handler-layer fallback validator for string enum
// fields. It shares the allowed-values slice with the schema via
// EnumValues so there is a single source of truth. Returns a bilingual
// error (Chinese + English) when the value is outside the allowed set,
// keeping the style aligned with translateStartDAGError.
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

func normalizeListLimit(limit, defaultLimit, maxLimit int) int {
	if limit <= 0 || (maxLimit > 0 && limit > maxLimit) {
		return defaultLimit
	}
	return limit
}

// marshalRawJSON 编码原始JSON。
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
