package logger

import (
	"log/slog"
	"regexp"
	"strings"
)

const redactedValue = "[REDACTED]"

var logSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		pattern:     regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&]+`),
		replacement: `${1}` + redactedValue,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?token|token|password|cookie)\s*[:=]\s*)[^\s,;&]+`),
		replacement: `${1}` + redactedValue,
	},
	{
		pattern:     regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
		replacement: redactedValue,
	},
}

func sanitizeLogAttr(attr slog.Attr) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	if secretLikeLogKey(attr.Key) {
		attr.Value = slog.StringValue(redactedValue)
		return attr
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(redactLogString(attr.Value.String()))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok && err != nil {
			attr.Value = slog.StringValue(redactLogString(err.Error()))
		}
	case slog.KindGroup:
		group := attr.Value.Group()
		safe := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			safe = append(safe, sanitizeLogAttr(child))
		}
		attr.Value = slog.GroupValue(safe...)
	}
	return attr
}

func redactLogString(value string) string {
	for _, current := range logSecretPatterns {
		value = current.pattern.ReplaceAllString(value, current.replacement)
	}
	return value
}

func secretLikeLogKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "cookie")
}
