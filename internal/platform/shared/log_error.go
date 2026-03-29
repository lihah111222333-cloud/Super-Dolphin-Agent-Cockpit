package shared

import pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

// LogIgnoredError logs an error that is intentionally not propagated.
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	if err != nil && logger != nil {
		logger.Warn(msg, "error", err)
	}
}
