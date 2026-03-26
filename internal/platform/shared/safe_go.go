package shared

import (
	"log/slog"
	"runtime/debug"
)

// SafeGo launches a goroutine with panic recovery.
func SafeGo(logger *slog.Logger, fn func()) {
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
