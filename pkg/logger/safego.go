package logger

import "runtime/debug"

// safeGo 在日志子系统内部启动带 panic recovery 的 goroutine。
// 本包不能导入 runtimesafe，否则会形成 import cycle；label 会写入恢复日志供排障定位。
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
