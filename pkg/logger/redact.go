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
		pattern:     regexp.MustCompile(`(?i)(?:[A-Za-z]:\\|/)[^\r\n,;&"']*?\.db(?:-(?:wal|shm))?`),
		replacement: redactedValue,
	},
	{
		pattern:     regexp.MustCompile(`(?i)((?:DATABASE_URL|POSTGRES_CONNECTION_STRING|SUPER_DOLPHIN_SQLITE_PATH|SUPER_DOLPHIN_INTERNAL_SQLITE_PATH)\s*[:=]\s*)[^\s,;&]+`),
		replacement: `${1}` + redactedValue,
	},
	{
		pattern:     regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
		replacement: redactedValue,
	},
}

// sanitizeLogAttr 清理日志attr。
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

// secretLikeLogKey 处理密钥like日志键。
func secretLikeLogKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	for _, marker := range secretLikeLogKeyMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

var secretLikeLogKeyMarkers = []string{
	"token",
	"password",
	"secret",
	"database_url",
	"postgres_connection_string",
	"sqlite_path",
	"sqlite_db_path",
	"authorization",
	"api_key",
	"apikey",
	"cookie",
}
