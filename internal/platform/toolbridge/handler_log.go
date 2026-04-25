package toolbridge

import pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

func (h *Handler) warn(msg string, args ...any) {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Warn(msg, args...)
}

func (h *Handler) debug(msg string, args ...any) {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Debug(msg, args...)
}
