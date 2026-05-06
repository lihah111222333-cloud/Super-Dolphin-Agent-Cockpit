package shared

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
)

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

// CloneTime delegates to clone.Time.
func CloneTime(value *time.Time) *time.Time { return clone.Time(value) }

type eventTimeKey struct{}

func WithEventTime(ctx context.Context, timestamp time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if timestamp.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, eventTimeKey{}, timestamp)
}

func ResolveEventTime(ctx context.Context, payload map[string]any, fallbacks ...time.Time) time.Time {
	if timestamp := eventTimeFromContext(ctx); !timestamp.IsZero() {
		return timestamp
	}
	if timestamp := EventTimeFromPayload(payload); !timestamp.IsZero() {
		return timestamp
	}
	return FirstEventTime(fallbacks...)
}

func FirstEventTime(fallbacks ...time.Time) time.Time {
	for _, timestamp := range fallbacks {
		if !timestamp.IsZero() {
			return timestamp
		}
	}
	return time.Now()
}

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

func ParseEventTime(raw string) time.Time {
	return ParseRFC3339Loose(raw)
}

func eventTimeFromContext(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	timestamp, _ := ctx.Value(eventTimeKey{}).(time.Time)
	return timestamp
}
