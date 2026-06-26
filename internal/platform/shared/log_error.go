package shared

import (
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// LogIgnoredError 记录可忽略错误，保持 shared 包旧入口兼容。
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	util.LogIgnoredError(logger, msg, err)
}
