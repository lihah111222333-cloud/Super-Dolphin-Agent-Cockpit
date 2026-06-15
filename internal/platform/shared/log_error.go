package shared

import (
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// LogIgnoredError delegates to util.LogIgnoredError.
// LogIgnoredError 处理日志ignored错误。
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	util.LogIgnoredError(logger, msg, err)
}
