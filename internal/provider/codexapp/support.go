package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(d, cancel)
	return ctx, func() {
		timer.Stop()
		cancel()
	}
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
