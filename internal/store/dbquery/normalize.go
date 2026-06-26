package dbquery

import "math"

const maxNormalizedIntegerFloat64 = 1 << 53

func normalizeArgs(args []any) []any {
	if len(args) == 0 {
		return nil
	}
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = normalizeArg(arg)
	}
	return out
}

// normalizeArg 规范化 JSON-RPC 传入的查询参数。
// JavaScript 安全整数范围内的 float64 会转成 int64，避免 SQLite 参数比较时整数 ID 被当作浮点值。
func normalizeArg(arg any) any {
	switch value := arg.(type) {
	case float64:
		if value == math.Trunc(value) && value >= -float64(maxNormalizedIntegerFloat64) && value <= float64(maxNormalizedIntegerFloat64) {
			return int64(value)
		}
	case []any:
		return normalizeArgs(value)
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, entry := range value {
			out[key] = normalizeArg(entry)
		}
		return out
	}
	return arg
}
