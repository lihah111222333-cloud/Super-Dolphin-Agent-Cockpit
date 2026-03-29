package shared

import (
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"runtime/debug"
)

// SafeGo launches a goroutine with panic recovery.
func SafeGo(logger *pkglogger.Logger, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil && logger != nil {
				logger.Error("recovered panic in goroutine",
					"panic", r,
					"stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}
