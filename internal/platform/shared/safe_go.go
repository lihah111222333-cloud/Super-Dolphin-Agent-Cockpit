package shared

import (
	"runtime/debug"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SafeGo launches a goroutine with panic recovery.
//
// Deprecated: Use runtimesafe.SafeGo(ctx, logger, label, fn) directly.
// This thin wrapper strips ctx and forces a generic log label, which
// degrades panic telemetry. Kept only for backward compatibility; as of
// 2026-04-18 no in-tree call sites remain and the
// TestSafeGoUsageCentralized archtest blocks regressions.
// SafeGo 处理safego。
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
