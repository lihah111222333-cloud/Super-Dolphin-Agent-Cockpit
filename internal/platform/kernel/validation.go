package kernel

import (
	"strings"
)

// FirstNonEmpty returns the first non-blank value after trimming whitespace.
func FirstNonEmpty(values ...string) string { return FirstTrimmed(values...) }

// FirstTrimmed returns the first value that remains non-empty after TrimSpace.
func FirstTrimmed(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// ClampLimit clamps val into [min, max], falling back when val is below min.
func ClampLimit(val, min, max, defaultVal int) int {
	if val < min {
		return defaultVal
	}
	if max > 0 && val > max {
		return max
	}
	return val
}

// FirstPayloadString 处理first载荷string。
func FirstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if text := payloadString(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func payloadString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return FirstPayloadString(typed, "text", "summary", "message", "output", "result")
	default:
		return ""
	}
}
