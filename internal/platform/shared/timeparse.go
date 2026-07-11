package shared

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/clone"
)

// ParseRFC3339Loose 解析 RFC3339/RFC3339Nano 时间，失败时返回零值。
func ParseRFC3339Loose(s string) time.Time {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed
	}
	parsed, _ := time.Parse(time.RFC3339, raw)
	return parsed
}

// DecodeHistoryMetadata 解码历史元数据；空、null、非法 JSON 都视为无元数据。
func DecodeHistoryMetadata(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload) == 0 {
		return nil
	}
	return payload
}

// CloneTime 复制 time 指针，nil 保持 nil。
func CloneTime(value *time.Time) *time.Time { return clone.Time(value) }

// CloneInt64 复制 int64 指针，nil 保持 nil。
func CloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// eventTimeKey 是 context 中事件时间的私有 key。
type eventTimeKey struct{}

// WithEventTime 把非零事件时间写入 context；nil context 会被提升为 Background。
func WithEventTime(ctx context.Context, timestamp time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if timestamp.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, eventTimeKey{}, timestamp)
}

// ResolveEventTime 按 context、payload、fallback、当前时间的顺序解析事件时间。
func ResolveEventTime(ctx context.Context, payload map[string]any, fallbacks ...time.Time) time.Time {
	if timestamp := eventTimeFromContext(ctx); !timestamp.IsZero() {
		return timestamp
	}
	if timestamp := EventTimeFromPayload(payload); !timestamp.IsZero() {
		return timestamp
	}
	return FirstEventTime(fallbacks...)
}

// FirstEventTime 返回第一个非零 fallback，全部为空时使用当前时间。
func FirstEventTime(fallbacks ...time.Time) time.Time {
	for _, timestamp := range fallbacks {
		if !timestamp.IsZero() {
			return timestamp
		}
	}
	return time.Now()
}

// EventTimeFromPayload 从常见 timestamp 字段中解析事件时间。
func EventTimeFromPayload(payload map[string]any) time.Time {
	if len(payload) == 0 {
		return time.Time{}
	}
	return ParseEventTime(strings.TrimSpace(FirstPayloadString(
		payload,
		"timestamp",
		"ts",
		"createdAt",
		"created_at",
		"updatedAt",
		"updated_at",
	)))
}

// ParseEventTime 解析事件时间字符串，当前接受 RFC3339/RFC3339Nano。
func ParseEventTime(raw string) time.Time {
	return ParseRFC3339Loose(raw)
}

// eventTimeFromContext 从 context 中读取事件时间，缺失或类型不匹配时返回零值。
func eventTimeFromContext(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	timestamp, _ := ctx.Value(eventTimeKey{}).(time.Time)
	return timestamp
}
