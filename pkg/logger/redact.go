package logger

import (
	"log/slog"
	"regexp"
	"strings"
)

const redactedValue = "[REDACTED]"

type logSecretPattern struct {
	pattern     *regexp.Regexp
	replacement string
}

type logRedactor struct {
	patterns []logSecretPattern
	markers  []string
}

// newLogRedactor 构造由单个 Runtime 持有的脱敏器。
func newLogRedactor() *logRedactor {
	return &logRedactor{
		patterns: []logSecretPattern{
			{
				pattern:     regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&]+`),
				replacement: `${1}` + redactedValue,
			},
			{
				pattern:     regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?token|token|password|cookie)\s*[:=]\s*)[^\s,;&]+`),
				replacement: `${1}` + redactedValue,
			},
			{
				pattern:     regexp.MustCompile(`(?i)(?:[A-Za-z]:\\|/)[^\r\n,;&"']*?\.db(?:-(?:wal|shm))?([[:space:],;&"')}]|$)`),
				replacement: redactedValue + `${1}`,
			},
			{
				pattern:     regexp.MustCompile(`(?i)((?:DATABASE_URL|POSTGRES_CONNECTION_STRING|SUPER_DOLPHIN_SQLITE_PATH|SUPER_DOLPHIN_INTERNAL_SQLITE_PATH)\s*[:=]\s*)[^\s,;&]+`),
				replacement: `${1}` + redactedValue,
			},
			{
				pattern:     regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
				replacement: redactedValue,
			},
		},
		markers: []string{
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
		},
	}
}

func (r *logRedactor) sanitizeAttr(attr slog.Attr) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	if r.secretLikeKey(attr.Key) {
		attr.Value = slog.StringValue(redactedValue)
		return attr
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(r.redactString(attr.Value.String()))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok && err != nil {
			attr.Value = slog.StringValue(r.redactString(err.Error()))
		}
	case slog.KindGroup:
		group := attr.Value.Group()
		safe := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			safe = append(safe, r.sanitizeAttr(child))
		}
		attr.Value = slog.GroupValue(safe...)
	}
	return attr
}

// redactLogString 对字符串内容应用日志脱敏规则。
func redactLogString(value string) string {
	return newLogRedactor().redactString(value)
}

func (r *logRedactor) redactString(value string) string {
	for _, current := range r.patterns {
		value = current.pattern.ReplaceAllString(value, current.replacement)
	}
	return value
}

func (r *logRedactor) secretLikeKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	for _, marker := range r.markers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
