package shared

import (
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// 工具结果持久化 hook。
// ---------------------------------------------------------------------------

// ToolResultMeta 是 provider 层复制的工具结果元数据。
// 该类型避免 provider 直接导入 module/turn，同时保持持久化 hook 的 wire 字段稳定。
type ToolResultMeta struct {
	ThreadID  string
	TurnID    string
	CallID    string
	ToolName  string
	Timestamp time.Time
}

// ToolResultRecord 是工具结果持久化后的 provider 层返回记录。
// Preview 给 UI 展示，PersistedPath 指向落盘内容，PersistFailed/PersistError 描述落盘失败。
type ToolResultRecord struct {
	Preview       string
	PersistedPath string
	PersistFailed bool
	PersistError  string
	Truncated     bool
	OriginalSize  int
}

// CaptureToolResultFunc 是工具结果捕获 hook 的函数签名。
type CaptureToolResultFunc func(meta ToolResultMeta, raw string) (ToolResultRecord, error)

// ResetToolResultScopeFunc 是清理指定 thread/turn 工具结果作用域的函数签名。
type ResetToolResultScopeFunc func(threadID, turnID string) error

// RuntimeHooks 集中描述 provider 运行时依赖，必须由 Fx 根图一次性完整装配。
type RuntimeHooks struct {
	Capture CaptureToolResultFunc
	Reset   ResetToolResultScopeFunc
}

// ConfigureRuntimeHooks 校验完整 hook bundle 并返回显式 runtime owner。
// 调用方必须把返回值注入到每一条 provider 执行链，禁止依赖进程级共享状态。
func ConfigureRuntimeHooks(hooks RuntimeHooks) (RuntimeHooks, error) {
	if hooks.Capture == nil {
		return RuntimeHooks{}, fmt.Errorf("provider runtime hooks: capture tool result is required")
	}
	if hooks.Reset == nil {
		return RuntimeHooks{}, fmt.Errorf("provider runtime hooks: reset tool result scope is required")
	}
	return hooks, nil
}

// CaptureToolResult 调用当前 runtime owner 的工具结果捕获 hook。
// 缺失 owner 时立即失败，禁止返回零值记录掩盖工具结果丢失。
func (hooks RuntimeHooks) CaptureToolResult(meta ToolResultMeta, raw string) (ToolResultRecord, error) {
	if hooks.Capture == nil {
		return ToolResultRecord{}, fmt.Errorf("provider runtime hooks: capture tool result is required")
	}
	return hooks.Capture(meta, raw)
}

// ResetToolResultScope 调用当前 runtime owner 的作用域清理 hook。
// 缺失 owner 时立即失败，禁止静默保留跨 turn 缓存。
func (hooks RuntimeHooks) ResetToolResultScope(threadID, turnID string) error {
	if hooks.Reset == nil {
		return fmt.Errorf("provider runtime hooks: reset tool result scope is required")
	}
	return hooks.Reset(threadID, turnID)
}
