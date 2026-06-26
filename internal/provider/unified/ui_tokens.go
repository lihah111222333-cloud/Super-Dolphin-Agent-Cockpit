package unified

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// PublishUITokensUpdated 将 provider token payload 翻译为 UI token 更新事件。
func PublishUITokensUpdated(data any, publish func(ev any)) {
	if publish == nil {
		return
	}
	payload := payloadMap(data)
	if len(payload) == 0 {
		return
	}
	ev, ok := tokensUpdatedEvent(payload)
	if !ok {
		return
	}
	publish(ev)
}

// tokensUpdatedEvent 从多种 provider token 字段形态中提取当前 turn 的 token 用量。
func tokensUpdatedEvent(payload map[string]any) (uidto.UITokensUpdated, bool) {
	usage := nestedMap(payload, "usage")
	tokenUsage := nestedMap(payload, "tokenUsage")
	totalUsage := nestedMap(tokenUsage, "total")
	lastUsage := nestedMap(tokenUsage, "last")

	// 优先使用 tokenUsage.last 表示当前 turn，用 total 仅作缺字段时的兼容输入。
	input, hasInput := firstInt(lastUsage, totalUsage, "inputTokens", "input_tokens")
	if !hasInput {
		input, hasInput = firstInt(usage, payload, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens")
	}

	output, hasOutput := firstInt(lastUsage, totalUsage, "outputTokens", "output_tokens")
	if !hasOutput {
		output, hasOutput = firstInt(usage, payload, "outputTokens", "output_tokens", "completionTokens", "completion_tokens")
	}

	total, hasTotal := firstInt(lastUsage, totalUsage, "totalTokens", "total_tokens")
	if !hasTotal {
		total, hasTotal = intFromMap(payload, "totalTokens", "total_tokens")
	}

	window, hasWindow := contextWindowValue(payload, usage)
	if !hasInput && !hasOutput && !hasTotal && !hasWindow {
		return uidto.UITokensUpdated{}, false
	}
	if !hasTotal {
		total = input + output
	}
	return uidto.UITokensUpdated{
		UITurnHeader: shareddto.UITurnHeader{
			UIProjectionHeader: shareddto.UIProjectionHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: tokenEventTime(payload)},
					ThreadID:    shared.FirstNonEmpty(stringValue(payload, "threadId", "thread_id"), stringValue(usage, "threadId", "thread_id")),
				},
				Projection: "thread",
			},
			TurnIDHeader: shareddto.TurnIDHeader{
				TurnID: shared.FirstNonEmpty(stringValue(payload, "turnId", "turn_id"), stringValue(usage, "turnId", "turn_id")),
			},
		},
		InputTokens:         input,
		OutputTokens:        output,
		TotalTokens:         total,
		ContextWindowTokens: window,
	}, true
}

// payloadMap 将 map、RawMessage 或 []byte payload 转为 map；无法解析时返回 nil 阻断发布。
func payloadMap(data any) map[string]any {
	switch typed := data.(type) {
	case map[string]any:
		return typed
	case json.RawMessage:
		var payload map[string]any
		if json.Unmarshal(typed, &payload) == nil {
			return payload
		}
	case []byte:
		var payload map[string]any
		if json.Unmarshal(typed, &payload) == nil {
			return payload
		}
	}
	return nil
}

// nestedMap 读取嵌套 map 字段，类型不匹配时按缺失处理。
func nestedMap(payload map[string]any, key string) map[string]any {
	value, _ := payload[key].(map[string]any)
	return value
}

// firstInt 先读 preferred，再读 fallback，用于区分当前 turn 与累计 token 字段。
func firstInt(preferred, fallback map[string]any, keys ...string) (int, bool) {
	if value, ok := intFromMap(preferred, keys...); ok {
		return value, true
	}
	return intFromMap(fallback, keys...)
}

// contextWindowValue 从顶层、tokenUsage 或 usage 中读取模型上下文窗口大小。
func contextWindowValue(payload, usage map[string]any) (int, bool) {
	tokenUsage := nestedMap(payload, "tokenUsage")
	keys := []string{"modelContextWindow", "contextWindow", "contextWindowTokens", "context_window", "context_window_tokens"}
	for _, key := range keys {
		if value, ok := intFromMap(payload, key); ok {
			return value, true
		}
		if value, ok := intFromMap(tokenUsage, key); ok {
			return value, true
		}
		if value, ok := intFromMap(usage, key); ok {
			return value, true
		}
	}
	return 0, false
}

// intFromMap 从多个候选键读取整数，兼容 JSON number、Go int 和数字字符串。
func intFromMap(payload map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case int:
			return typed, true
		case int64:
			return int(typed), true
		case json.Number:
			parsed, err := typed.Int64()
			return int(parsed), err == nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			return parsed, err == nil
		}
	}
	return 0, false
}

// stringValue 从多个候选键读取非空字符串，JSON number 保留原始文本形式。
func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed = strings.TrimSpace(typed); typed != "" {
				return typed
			}
		case json.Number:
			return typed.String()
		}
	}
	return ""
}

// tokenEventTime 解析 provider 事件时间；缺失或格式错误时使用当前时间标记投影更新时间。
func tokenEventTime(payload map[string]any) time.Time {
	raw := stringValue(payload, "timestamp", "ts")
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed
		}
	}
	return time.Now()
}
