package shared

import "strings"

func FirstNonEmpty(values ...string) string {
	return FirstTrimmed(values...)
}

func FirstTrimmed(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func ClampLimit(val, min, max, defaultVal int) int {
	if val < min {
		return defaultVal
	}
	if max > 0 && val > max {
		return max
	}
	return val
}

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
