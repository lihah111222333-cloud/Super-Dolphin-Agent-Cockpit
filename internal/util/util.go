// Package util provides lightweight, dependency-free helpers shared across
// the module layer.  Functions here have no internal/* imports beyond the
// standard library and pkg/logger.
package util

import (
	"context"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// FirstNonEmpty returns the first non-blank (after TrimSpace) value, or "".
func FirstNonEmpty(values ...string) string {
	return FirstTrimmed(values...)
}

// FirstTrimmed returns the first value that is non-empty after TrimSpace.
func FirstTrimmed(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// ClampLimit clamps val into [min, max] with a default fallback.
func ClampLimit(val, min, max, defaultVal int) int {
	if val < min {
		return defaultVal
	}
	if max > 0 && val > max {
		return max
	}
	return val
}

// NonNilContext returns ctx, or context.Background() if ctx is nil.
func NonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// LogIgnoredError logs an error that is intentionally not propagated.
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	if err != nil && logger != nil {
		logger.Warn(msg, "error", err)
	}
}

// IsRemoteTurnInput returns true if value looks like an HTTP(S) URL.
func IsRemoteTurnInput(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
