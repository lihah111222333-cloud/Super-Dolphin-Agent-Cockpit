package shared

import (
	"sync/atomic"
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
// Preview 给 UI 展示，PersistedPath 指向落盘内容，Truncated/OriginalSize 描述截断边界。
type ToolResultRecord struct {
	Preview       string
	PersistedPath string
	Truncated     bool
	OriginalSize  int
}

// CaptureToolResultFunc 是工具结果捕获 hook 的函数签名。
type CaptureToolResultFunc func(meta ToolResultMeta, raw string) ToolResultRecord

// ResetToolResultScopeFunc 是清理指定 thread/turn 工具结果作用域的函数签名。
type ResetToolResultScopeFunc func(threadID, turnID string)

var captureToolResultHook atomic.Pointer[CaptureToolResultFunc]
var resetToolResultScopeHook atomic.Pointer[ResetToolResultScopeFunc]

// SetCaptureToolResultHook 设置全局工具结果捕获 hook。
// module/turn 在 fx init 注入实现；nil 会清空 hook，便于测试隔离。
func SetCaptureToolResultHook(fn CaptureToolResultFunc) {
	if fn == nil {
		captureToolResultHook.Store(nil)
		return
	}
	captureToolResultHook.Store(&fn)
}

// SetResetToolResultScopeHook 设置全局工具结果作用域清理 hook。
// 该 hook 用于 turn 结束或重置时释放 provider 层缓存。
func SetResetToolResultScopeHook(fn ResetToolResultScopeFunc) {
	if fn == nil {
		resetToolResultScopeHook.Store(nil)
		return
	}
	resetToolResultScopeHook.Store(&fn)
}

// CaptureToolResult 调用已注册的工具结果捕获 hook。
// 未注册时返回零值记录，provider 调用方不需要关心模块是否已装配。
func CaptureToolResult(meta ToolResultMeta, raw string) ToolResultRecord {
	ptr := captureToolResultHook.Load()
	if ptr == nil {
		return ToolResultRecord{}
	}
	return (*ptr)(meta, raw)
}

// ResetToolResultScope 调用已注册的作用域清理 hook。
// 未注册时是 no-op，避免 provider 单测必须装配 turn 模块。
func ResetToolResultScope(threadID, turnID string) {
	ptr := resetToolResultScopeHook.Load()
	if ptr == nil {
		return
	}
	(*ptr)(threadID, turnID)
}

// ---------------------------------------------------------------------------
// 技能注入块裁剪 hook。
// ---------------------------------------------------------------------------

// TrimInjectedSkillBlocksFunc 是裁剪 prompt 中技能注入块的函数签名。
type TrimInjectedSkillBlocksFunc func(text string) string

var trimSkillBlocksHook atomic.Pointer[TrimInjectedSkillBlocksFunc]

// SetTrimSkillBlocksHook 设置全局技能块裁剪 hook。
// module/skill 在 fx init 注入实现；nil 会清空 hook。
func SetTrimSkillBlocksHook(fn TrimInjectedSkillBlocksFunc) {
	if fn == nil {
		trimSkillBlocksHook.Store(nil)
		return
	}
	trimSkillBlocksHook.Store(&fn)
}

// TrimInjectedSkillBlocks 调用已注册的技能块裁剪 hook。
// 未注册时返回原文，保证 provider 可独立运行。
func TrimInjectedSkillBlocks(text string) string {
	ptr := trimSkillBlocksHook.Load()
	if ptr == nil {
		return text
	}
	return (*ptr)(text)
}
