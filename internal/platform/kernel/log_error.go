package kernel

import (
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// LogIgnoredError records an error that a boundary intentionally does not return.
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	if err != nil && logger != nil {
		logger.Warn(msg, "error", err)
	}
}
