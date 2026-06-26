// Package safego 提供带 panic 保护的 goroutine 启动入口。
// 非库内部主循环的后端 goroutine 应通过 Go 启动，保证 panic 会带 ctx 和 label 进入日志。
package safego

import (
	"context"
	"runtime/debug"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Go 在新 goroutine 中运行 fn，并在 panic 时记录 label、panic 值和堆栈。
// ctx 会传入 fn 供其响应取消；logger 为空时回退到全局日志器，避免恢复路径再 panic。
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
					// 调用方没有提供 logger 时仍要保留 panic 证据。
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
