package logger

import "runtime/debug"

// safeGo launches fn in a new goroutine with panic recovery. It is a
// package-local variant of runtimesafe.SafeGo used for log-subsystem
// goroutines that must not panic-crash the process. It intentionally
// avoids importing internal/platform/runtimesafe to prevent an import
// cycle (runtimesafe imports pkg/logger).
//
// label is recorded on the recover log line for operator debugging.
func safeGo(label string, fn func()) {
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				Error("logger: recovered panic in goroutine",
					"label", label,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}
