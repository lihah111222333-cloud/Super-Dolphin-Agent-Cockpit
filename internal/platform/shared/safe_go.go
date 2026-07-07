package shared

import (
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"runtime/debug"
	"sync"
)

// SafeGo 启动带 panic recovery 的 goroutine。
//
// Deprecated: 请直接使用 runtimesafe.SafeGo(ctx, logger, label, fn)。这个旧入口
// 没有 ctx 且只能写通用日志标签，只为兼容旧调用保留。
func SafeGo(logger *pkglogger.Logger, fn func()) {
	var safeWG sync.WaitGroup
	safeWG.Go(func() {
		defer func() {
			if r := recover(); r != nil && logger != nil {
				logger.Error("recovered panic in goroutine", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	})
}
