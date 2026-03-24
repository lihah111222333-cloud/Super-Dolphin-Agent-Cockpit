package thread

import (
	"context"
	"strings"
	"time"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func resolveProviderThreadID(threadID, fallback string) string {
	return firstNonEmpty(threadID, fallback)
}

func bindingPublicThreadID(binding *bindingstore.Binding, fallback string) string {
	if binding == nil {
		return strings.TrimSpace(fallback)
	}
	return firstNonEmpty(binding.CodexThreadID, fallback)
}

func normalizeThreadContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return time.Now().Unix()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
