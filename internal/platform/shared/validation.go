package shared

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// FirstNonEmpty delegates to util.FirstNonEmpty.
func FirstNonEmpty(values ...string) string { return util.FirstNonEmpty(values...) }

// FirstTrimmed delegates to util.FirstTrimmed.
func FirstTrimmed(values ...string) string { return util.FirstTrimmed(values...) }

// ClampLimit delegates to util.ClampLimit.
func ClampLimit(val, min, max, defaultVal int) int { return util.ClampLimit(val, min, max, defaultVal) }

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
