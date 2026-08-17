package hiddenexec

import "fmt"

// WindowsJobPolicyError 表示受控 Windows Job 层级策略阻止了进程启动。
// 这是共享 hiddenexec 契约实现，故意不加 windows build tag：Windows 实现产生该错误，
// 工具层稳定分类；非 Windows 只编译类型契约，不执行任何 Windows 行为，也不会改变其他平台的进程启动路径。
type WindowsJobPolicyError struct {
	Operation       string
	LimitFlags      uint32
	KillOnClose     bool
	BreakawayOK     bool
	SilentBreakaway bool
	Cause           error
}

// Error 输出不含路径、PID 或句柄的 Job 策略事实，保留可审计的根因链。
func (e *WindowsJobPolicyError) Error() string {
	if e == nil {
		return "Windows Job policy blocked process startup"
	}
	operation := e.Operation
	if operation == "" {
		operation = "Windows Job policy blocked process startup"
	}
	message := fmt.Sprintf(
		"%s: limit_flags=0x%08x kill_on_close=%t breakaway_ok=%t silent_breakaway_ok=%t",
		operation,
		e.LimitFlags,
		e.KillOnClose,
		e.BreakawayOK,
		e.SilentBreakaway,
	)
	return message
}

// Unwrap 保留底层 Win32 错误，供调用方区分 Job 策略与真实 ACL 授权失败。
func (e *WindowsJobPolicyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
