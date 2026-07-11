package shared

import (
	"fmt"
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
	CaptureToolResult    CaptureToolResultFunc
	ResetToolResultScope ResetToolResultScopeFunc
}

// RuntimeHooksReady 是 provider 模块要求的 Fx readiness token。
// 只有 ConfigureRuntimeHooks 完成全量校验后才能生成。
type RuntimeHooksReady struct{}

var runtimeHooks atomic.Pointer[RuntimeHooks]

// ConfigureRuntimeHooks 原子发布完整 hook bundle，缺少任一核心能力都会阻断 Fx 装配。
func ConfigureRuntimeHooks(hooks RuntimeHooks) (RuntimeHooksReady, error) {
	if hooks.CaptureToolResult == nil {
		return RuntimeHooksReady{}, fmt.Errorf("provider runtime hooks: capture tool result is required")
	}
	if hooks.ResetToolResultScope == nil {
		return RuntimeHooksReady{}, fmt.Errorf("provider runtime hooks: reset tool result scope is required")
	}
	runtimeHooks.Store(&hooks)
	return RuntimeHooksReady{}, nil
}

// CaptureToolResult 调用已注册的工具结果捕获 hook。
// 根图未装配时立即失败，禁止返回零值记录掩盖工具结果丢失。
func CaptureToolResult(meta ToolResultMeta, raw string) (ToolResultRecord, error) {
	hooks, err := configuredRuntimeHooks()
	if err != nil {
		return ToolResultRecord{}, err
	}
	return hooks.CaptureToolResult(meta, raw)
}

// ResetToolResultScope 调用已注册的作用域清理 hook。
// 根图未装配时立即失败，禁止静默保留跨 turn 缓存。
func ResetToolResultScope(threadID, turnID string) error {
	hooks, err := configuredRuntimeHooks()
	if err != nil {
		return err
	}
	return hooks.ResetToolResultScope(threadID, turnID)
}

func configuredRuntimeHooks() (*RuntimeHooks, error) {
	hooks := runtimeHooks.Load()
	if hooks == nil {
		return nil, fmt.Errorf("provider runtime hooks are not configured")
	}
	return hooks, nil
}
