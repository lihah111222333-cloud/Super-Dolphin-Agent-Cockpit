package shared

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// LogIgnoredError 记录可忽略错误，保持 shared 包旧入口兼容。
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	util.LogIgnoredError(logger, msg, err)
}
