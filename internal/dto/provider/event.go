package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// RawProviderEvent 是 provider driver 原始事件，尚未转换成统一业务事件。
type RawProviderEvent struct {
	EventType string
	Data      any
	Terminal  *TerminalOutcome
}

// TerminalOutcome 是 provider adapter 一次解析后供投影与 runtime 共用的终态真值。
type TerminalOutcome struct {
	Success       bool
	Status        string
	Cause         string
	RequestID     string
	ContractError string
}

// Type 返回 provider raw event 分发用的类型编号。
func (RawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }

// SanitizedCopy 返回只含安全元数据的 provider raw event 副本。
// 调试所需的关联 ID、payload 大小和 hash 会保留，原始 payload 内容不会进入日志或总线。
func (e RawProviderEvent) SanitizedCopy() RawProviderEvent {
	return RawProviderEvent{EventType: strings.TrimSpace(e.EventType), Data: e.safeMetadata()}
}

// SafePayload 返回安全元数据 JSON，供 AgentError/Warning 等 DTO 的 Payload 字段使用。
func (e RawProviderEvent) SafePayload() json.RawMessage {
	raw, err := json.Marshal(e.safeMetadata())
	if err != nil {
		return nil
	}
	return raw
}

func (e RawProviderEvent) safeMetadata() map[string]any {
	raw := rawProviderPayloadBytes(e.Data)
	sum := sha256.Sum256(raw)
	payload := payloadMap(e.Data)
	out := map[string]any{
		"event_type":          strings.TrimSpace(e.EventType),
		"payload_size_bytes":  len(raw),
		"payload_sha256":      hex.EncodeToString(sum[:]),
		"payload_field_names": safeProviderFieldNames(payload),
	}
	copySafeString(out, payload, "session_id", "session_id", "sessionId")
	copySafeString(out, payload, "thread_id", "thread_id", "threadId")
	copySafeString(out, payload, "agent_id", "agent_id", "agentId")
	copySafeString(out, payload, "turn_id", "turn_id", "turnId")
	copySafeString(out, payload, "call_id", "call_id", "callId")
	copySafeString(out, payload, "tool_name", "tool_name", "toolName")
	copySafeMetric(out, payload, "context_window", "context_window", "contextWindow")
	copySafeMetric(out, payload, "contextWindowTokens", "contextWindowTokens", "context_window_tokens")
	copySafeMetric(out, payload, "input_tokens", "input_tokens", "inputTokens")
	copySafeMetric(out, payload, "output_tokens", "output_tokens", "outputTokens")
	copySafeMetric(out, payload, "total_tokens", "total_tokens", "totalTokens")
	if usage := safeUsageMetrics(payload["usage"]); len(usage) > 0 {
		out["usage"] = usage
	}
	return out
}

func rawProviderPayloadBytes(data any) []byte {
	switch typed := data.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return append([]byte(nil), typed...)
	case []byte:
		return append([]byte(nil), typed...)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return []byte("<unmarshalable>")
		}
		return raw
	}
}

// payloadMap 只把 provider payload 转成字段名集合使用的 map。
// 解析失败时返回空集合，不能把原始 payload 带回日志或事件。
func payloadMap(data any) map[string]any {
	switch typed := data.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	case json.RawMessage:
		var out map[string]any
		_ = json.Unmarshal(typed, &out)
		return out
	case []byte:
		var out map[string]any
		_ = json.Unmarshal(typed, &out)
		return out
	default:
		return nil
	}
}

func safeProviderFieldNames(payload map[string]any) []string {
	names := make([]string, 0, len(payload))
	for key := range payload {
		normalized := safeProviderFieldName(key)
		if normalized != "" {
			names = append(names, normalized)
		}
	}
	sort.Strings(names)
	return names
}

// safeProviderFieldName 把允许公开的 provider 字段名归一化。
// 未登记字段直接丢弃，避免 token、prompt 或嵌套 payload 名称泄露。
func safeProviderFieldName(key string) string {
	switch key {
	case "session_id", "sessionId":
		return "session_id"
	case "thread_id", "threadId":
		return "thread_id"
	case "agent_id", "agentId":
		return "agent_id"
	case "turn_id", "turnId":
		return "turn_id"
	case "call_id", "callId":
		return "call_id"
	case "tool_name", "toolName":
		return "tool_name"
	case "message", "code", "status", "success", "type":
		return key
	case "context_window", "contextWindow", "context_window_tokens", "contextWindowTokens",
		"input_tokens", "inputTokens", "output_tokens", "outputTokens", "total_tokens", "totalTokens",
		"usage":
		return key
	default:
		return ""
	}
}

func copySafeString(out map[string]any, payload map[string]any, target string, keys ...string) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			out[target] = text
			return
		}
	}
}

func copySafeMetric(out map[string]any, payload map[string]any, target string, keys ...string) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		if safe, ok := safeMetricValue(value); ok {
			out[target] = safe
			return
		}
	}
}

func safeUsageMetrics(value any) map[string]any {
	usage, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any)
	copySafeMetric(out, usage, "contextWindowTokens", "contextWindowTokens", "context_window_tokens")
	copySafeMetric(out, usage, "inputTokens", "inputTokens", "input_tokens")
	copySafeMetric(out, usage, "outputTokens", "outputTokens", "output_tokens")
	copySafeMetric(out, usage, "totalTokens", "totalTokens", "total_tokens")
	return out
}

func safeMetricValue(value any) (any, bool) {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return typed, true
	default:
		return nil, false
	}
}

// BusRawProviderEvent 将原始 provider 事件包装为事件总线载荷。
type BusRawProviderEvent struct {
	Event RawProviderEvent
}

// Type 返回 provider raw event 总线载荷的类型编号。
func (BusRawProviderEvent) Type() uint32 { return shared.EventTypeProviderRaw }
