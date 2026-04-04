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
			"keyword": StringSchema("Search keyword (optional)."),
		}), spec.ListHandler),
		defineTool(spec.GetName, spec.GetDescription, ObjectSchema(map[string]Schema{
			spec.KeyField: StringSchema(spec.KeyDescription),
		}, spec.KeyField), spec.GetHandler),
	)
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
