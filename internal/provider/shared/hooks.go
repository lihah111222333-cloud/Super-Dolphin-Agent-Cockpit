package shared

import (
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Tool Result hooks
// ---------------------------------------------------------------------------

// ToolResultMeta is a provider-local mirror of the tool result metadata.
// It avoids a direct import of module/turn.
type ToolResultMeta struct {
	ThreadID  string
	TurnID    string
	CallID    string
	ToolName  string
	Timestamp time.Time
}

// ToolResultRecord is a provider-local mirror of the tool result record.
type ToolResultRecord struct {
	Preview       string
	PersistedPath string
	Truncated     bool
	OriginalSize  int
}

// CaptureToolResultFunc is the function signature for capturing tool results.
type CaptureToolResultFunc func(meta ToolResultMeta, raw string) ToolResultRecord

// ResetToolResultScopeFunc is the function signature for resetting tool result scope.
type ResetToolResultScopeFunc func(threadID, turnID string)

var captureToolResultHook atomic.Pointer[CaptureToolResultFunc]
var resetToolResultScopeHook atomic.Pointer[ResetToolResultScopeFunc]

// SetCaptureToolResultHook sets the global capture hook. Called by module/turn at fx init.
// SetCaptureToolResultHook 设置capture工具结果hook。
func SetCaptureToolResultHook(fn CaptureToolResultFunc) {
	if fn == nil {
		captureToolResultHook.Store(nil)
		return
	}
	captureToolResultHook.Store(&fn)
}

// SetResetToolResultScopeHook sets the global reset hook. Called by module/turn at fx init.
// SetResetToolResultScopeHook 设置reset工具结果作用域hook。
func SetResetToolResultScopeHook(fn ResetToolResultScopeFunc) {
	if fn == nil {
		resetToolResultScopeHook.Store(nil)
		return
	}
	resetToolResultScopeHook.Store(&fn)
}

// CaptureToolResult calls the registered hook. Returns zero record if no hook is set.
// CaptureToolResult 生成capture工具结果。
func CaptureToolResult(meta ToolResultMeta, raw string) ToolResultRecord {
	ptr := captureToolResultHook.Load()
	if ptr == nil {
		return ToolResultRecord{}
	}
	return (*ptr)(meta, raw)
}

// ResetToolResultScope calls the registered hook. No-op if no hook is set.
// ResetToolResultScope 重置工具结果作用域。
func ResetToolResultScope(threadID, turnID string) {
	ptr := resetToolResultScopeHook.Load()
	if ptr == nil {
		return
	}
	(*ptr)(threadID, turnID)
}

// ---------------------------------------------------------------------------
// Skill Block Trim hook
// ---------------------------------------------------------------------------

// TrimInjectedSkillBlocksFunc is the function signature for trimming skill blocks.
type TrimInjectedSkillBlocksFunc func(text string) string

var trimSkillBlocksHook atomic.Pointer[TrimInjectedSkillBlocksFunc]

// SetTrimSkillBlocksHook sets the global skill-block trim hook.
// Called by module/skill at fx init.
// SetTrimSkillBlocksHook 设置裁剪技能blockshook。
func SetTrimSkillBlocksHook(fn TrimInjectedSkillBlocksFunc) {
	if fn == nil {
		trimSkillBlocksHook.Store(nil)
		return
	}
	trimSkillBlocksHook.Store(&fn)
}

// TrimInjectedSkillBlocks calls the registered hook.
// Returns the original text if no hook is set.
// TrimInjectedSkillBlocks 处理裁剪injected技能blocks。
func TrimInjectedSkillBlocks(text string) string {
	ptr := trimSkillBlocksHook.Load()
	if ptr == nil {
		return text
	}
	return (*ptr)(text)
}
