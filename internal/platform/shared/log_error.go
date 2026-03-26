package shared

import "log/slog"

// LogIgnoredError logs an error that is intentionally not propagated.
func LogIgnoredError(logger *slog.Logger, msg string, err error) {
	if err != nil && logger != nil {
		logger.Warn(msg, "error", err)
	}
}
