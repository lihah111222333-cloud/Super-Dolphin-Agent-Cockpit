// Package runtimesafe 提供统一的 panic-safe goroutine 启动入口。
// 业务后台 goroutine 应通过 SafeGo 记录 ctx、label、panic 和 stack，避免静默退出或直接冲垮进程。
package runtimesafe

import (
	"context"
	"runtime/debug"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// SafeGo 启动带 panic recovery 的 goroutine，并把 label、panic 和 stack 写入日志。
// ctx 会传入 fn 供调用方响应取消；label 必须稳定，便于排查后台任务来源。
func SafeGo(ctx context.Context, logger *pkglogger.Logger, label string, fn func(context.Context)) {
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
					logger.Error("runtimesafe: recovered panic",
						"label", label,
						"panic", rec,
						"stack", string(debug.Stack()),
					)
				} else {
					// 调用方未传 logger 时仍写全局日志，避免 panic 观测丢失。
					pkglogger.Error("runtimesafe: recovered panic",
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
