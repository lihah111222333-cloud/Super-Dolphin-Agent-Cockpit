// Package safego centralizes panic-safe goroutine launchers for the
// backend. Every in-tree goroutine that is not a first-class part of the
// Go runtime (e.g. a server main loop started by a library) must go
// through Go so panics are logged with ctx + label instead of
// crashing the process.
package safego

import (
	"context"
	"runtime/debug"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Go launches fn in a new goroutine with panic recovery. On panic
// it logs the ctx + label + panic value + stack to the supplied logger
// and returns without crashing the process.
//
// The ctx is threaded into fn so callers can honor cancellation; passing
// context.Background() is fine when no upstream ctx is available (e.g.
// fire-and-forget fan-out from a sync event handler).
//
// label must be a short, stable identifier like "skill.scheduleFlush" so
// operators can grep telemetry.
// Go 处理go。
func Go(ctx context.Context, logger *pkglogger.Logger, label string, fn func(context.Context)) {
	if fn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				if logger != nil {
					logger.Error("safego: recovered panic",
						"label", label,
						"panic", rec,
						"stack", string(debug.Stack()),
					)
				} else {
					// Fallback to global logger if caller passed nil.
					pkglogger.Error("safego: recovered panic",
						"label", label,
						"panic", rec,
						"stack", string(debug.Stack()),
					)
				}
			}
		}()
		fn(ctx)
	}()
}
