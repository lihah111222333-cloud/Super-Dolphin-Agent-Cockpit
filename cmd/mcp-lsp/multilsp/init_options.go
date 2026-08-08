package multilsp

import "slices"

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneAnyValue(value)
	}
	return output
}

func cloneAnyValue(input any) any {
	switch value := input.(type) {
	case map[string]any:
		return cloneAnyMap(value)
	case []any:
		output := make([]any, len(value))
		for index := range value {
			output[index] = cloneAnyValue(value[index])
		}
		return output
	case []string:
		return slices.Clone(value)
	default:
		return value
	}
}

// CloneInitOptions 深拷贝 adapter 初始化选项，隔离 manager、factory 与 client 的可变配置。
func CloneInitOptions(input map[string]any) map[string]any {
	return cloneAnyMap(input)
}
