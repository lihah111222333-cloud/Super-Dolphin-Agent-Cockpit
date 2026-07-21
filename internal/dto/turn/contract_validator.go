package turn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// ValidateTurnRefV1 严格验证 canonical turn 身份且拒绝未知字段。
func ValidateTurnRefV1(value any) error { return validateNamed("TurnRefV1", value) }

// ValidatePublicErrorV1 严格验证安全公开错误且拒绝原始字段。
func ValidatePublicErrorV1(value any) error { return validateNamed("PublicErrorV1", value) }

// ValidateTurnTerminalV2 严格验证终态及其 outcome 依赖字段。
func ValidateTurnTerminalV2(value any) error { return validateNamed("TurnTerminalV2", value) }

func validateNamed(name string, value any) error {
	schema, err := schemaDocument(name)
	if err != nil {
		return err
	}
	normalized, err := normalizeValue(value)
	if err != nil {
		return fmt.Errorf("%s payload: %w", name, err)
	}
	return validateSchema(name, schema, normalized, "$", 0)
}

func schemaDocument(name string) (map[string]any, error) {
	raw, ok := generatedSchemas[name]
	if !ok {
		return nil, fmt.Errorf("unknown generated schema %q", name)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		return nil, fmt.Errorf("decode generated schema %q: %w", name, err)
	}
	return schema, nil
}

func normalizeValue(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return normalized, nil
}

// validateSchema 递归解释生成 schema，任何不支持或不匹配都立即返回错误。
func validateSchema(name string, schema map[string]any, value any, path string, depth int) error {
	if depth > 32 {
		return fmt.Errorf("%s exceeds schema recursion limit", path)
	}
	if reference, ok := schema["$ref"].(string); ok {
		referenced, err := schemaDocument(reference)
		if err != nil {
			return err
		}
		return validateSchema(name, referenced, value, path, depth+1)
	}
	if err := validateScalar(schema, value, path); err != nil {
		return err
	}
	if err := validateComposition(name, schema, value, path, depth+1); err != nil {
		return err
	}
	return validateStructured(name, schema, value, path, depth+1)
}

// validateScalar 验证不依赖子节点的类型、常量、枚举和字符串约束。
func validateScalar(schema map[string]any, value any, path string) error {
	if constant, ok := schema["const"]; ok && !jsonValuesEqual(value, constant) {
		return fmt.Errorf("%s must equal %v", path, constant)
	}
	if enum, ok := schema["enum"].([]any); ok && !containsJSONValue(enum, value) {
		return fmt.Errorf("%s has unsupported value %v", path, value)
	}
	if requiredType, ok := schema["type"].(string); ok && !matchesType(requiredType, value) {
		return fmt.Errorf("%s must be a %s", path, requiredType)
	}
	if text, ok := value.(string); ok {
		if minLength, ok := schema["minLength"].(float64); ok && len(text) < int(minLength) {
			return fmt.Errorf("%s must not be empty", path)
		}
	}
	return nil
}

// validateComposition 验证条件、并集与否定组成的终态规则。
func validateComposition(name string, schema map[string]any, value any, path string, depth int) error {
	if err := validateAllOf(name, schema, value, path, depth); err != nil {
		return err
	}
	if err := validateAnyOf(name, schema, value, path, depth); err != nil {
		return err
	}
	if forbidden, ok := schema["not"].(map[string]any); ok && validateSchema(name, forbidden, value, path, depth) == nil {
		return fmt.Errorf("%s matches a forbidden contract shape", path)
	}
	return nil
}

func validateAllOf(name string, schema map[string]any, value any, path string, depth int) error {
	entries, ok := schema["allOf"].([]any)
	if !ok {
		return nil
	}
	for _, entry := range entries {
		part, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("%s has invalid allOf entry", name)
		}
		if err := validateConditional(name, part, value, path, depth); err != nil {
			return err
		}
	}
	return nil
}

func validateAnyOf(name string, schema map[string]any, value any, path string, depth int) error {
	entries, ok := schema["anyOf"].([]any)
	if !ok {
		return nil
	}
	for _, entry := range entries {
		part, ok := entry.(map[string]any)
		if ok && validateSchema(name, part, value, path, depth) == nil {
			return nil
		}
	}
	return fmt.Errorf("%s does not match any permitted contract shape", path)
}

// validateStructured 只把对象和数组交给各自的严格子验证器。
func validateStructured(name string, schema map[string]any, value any, path string, depth int) error {
	if object, ok := value.(map[string]any); ok {
		return validateObject(name, schema, object, path, depth)
	}
	if array, ok := value.([]any); ok {
		return validateArray(name, schema, array, path, depth)
	}
	return nil
}

func validateConditional(name string, schema map[string]any, value any, path string, depth int) error {
	condition, ok := schema["if"].(map[string]any)
	if !ok {
		return validateSchema(name, schema, value, path, depth)
	}
	if validateSchema(name, condition, value, path, depth) == nil {
		if then, ok := schema["then"].(map[string]any); ok {
			return validateSchema(name, then, value, path, depth)
		}
		return nil
	}
	if otherwise, ok := schema["else"].(map[string]any); ok {
		return validateSchema(name, otherwise, value, path, depth)
	}
	return nil
}

// validateObject 验证字段存在性、未知字段和每个已登记子 schema。
func validateObject(name string, schema map[string]any, value map[string]any, path string, depth int) error {
	properties, err := schemaProperties(schema, name)
	if err != nil {
		return err
	}
	if err := validateRequiredFields(schema, value, path, name); err != nil {
		return err
	}
	if err := validateKnownFields(schema, value, properties, path); err != nil {
		return err
	}
	for field, childValue := range value {
		childSchema, exists := properties[field].(map[string]any)
		if !exists {
			continue
		}
		if err := validateSchema(name, childSchema, childValue, path+"."+field, depth); err != nil {
			return err
		}
	}
	return nil
}

func schemaProperties(schema map[string]any, name string) (map[string]any, error) {
	if raw, exists := schema["properties"]; exists {
		properties, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s has invalid properties", name)
		}
		return properties, nil
	}
	return map[string]any{}, nil
}

func validateRequiredFields(schema map[string]any, value map[string]any, path, name string) error {
	required, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	for _, field := range required {
		fieldName, ok := field.(string)
		if !ok {
			return fmt.Errorf("%s has invalid required field", name)
		}
		if _, exists := value[fieldName]; !exists {
			return fmt.Errorf("%s.%s is required", path, fieldName)
		}
	}
	return nil
}

func validateKnownFields(schema map[string]any, value, properties map[string]any, path string) error {
	additional, ok := schema["additionalProperties"].(bool)
	if !ok || additional {
		return nil
	}
	for field := range value {
		if _, exists := properties[field]; !exists {
			return fmt.Errorf("%s.%s is unknown", path, field)
		}
	}
	return nil
}

// validateArray 验证去重要求并把元素交给其唯一 items schema。
func validateArray(name string, schema map[string]any, value []any, path string, depth int) error {
	if unique, ok := schema["uniqueItems"].(bool); ok && unique {
		for left := range value {
			for right := left + 1; right < len(value); right++ {
				if jsonValuesEqual(value[left], value[right]) {
					return fmt.Errorf("%s contains duplicate item", path)
				}
			}
		}
	}
	itemSchema, ok := schema["items"].(map[string]any)
	if !ok {
		return nil
	}
	for index, item := range value {
		if err := validateSchema(name, itemSchema, item, fmt.Sprintf("%s[%d]", path, index), depth); err != nil {
			return err
		}
	}
	return nil
}

func matchesType(name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

func containsJSONValue(values []any, target any) bool {
	for _, value := range values {
		if jsonValuesEqual(value, target) {
			return true
		}
	}
	return false
}

func jsonValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftJSON, rightJSON)
}
