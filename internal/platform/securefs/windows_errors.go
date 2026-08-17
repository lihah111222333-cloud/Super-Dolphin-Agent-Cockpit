package securefs

// 本文件故意不加 windows build tag：类型化 Win32 5/1314 错误是跨进程 wire
// 契约，非 Windows 构建也要识别和序列化它；真实 errno 分类位于平台带标签实现。

import (
	"errors"
	"fmt"
	"syscall"
)

// WindowsPermissionKind 表示可跨平台传递的 Windows 权限错误分类。
type WindowsPermissionKind uint8

const (
	// WindowsPermissionUnknown 表示错误不是已知的 Windows 权限错误。
	WindowsPermissionUnknown WindowsPermissionKind = iota
	// WindowsPermissionDenied 表示 Win32 ERROR_ACCESS_DENIED（5）。
	WindowsPermissionDenied
	// WindowsPrivilegeNotHeld 表示 Win32 ERROR_PRIVILEGE_NOT_HELD（1314）。
	WindowsPrivilegeNotHeld
	// WindowsPermissionAccessDenied 是 WindowsPermissionDenied 的语义别名。
	WindowsPermissionAccessDenied = WindowsPermissionDenied
)

var (
	// ErrWindowsPermissionDenied 是 Windows 访问被拒绝的稳定分类哨兵。
	ErrWindowsPermissionDenied = errors.New("windows permission denied")
	// ErrWindowsAccessDenied 是 ErrWindowsPermissionDenied 的兼容别名。
	ErrWindowsAccessDenied = ErrWindowsPermissionDenied
	// ErrWindowsPrivilegeNotHeld 是 Windows 权限未持有的稳定分类哨兵。
	ErrWindowsPrivilegeNotHeld = errors.New("windows privilege not held")
)

// WindowsPermissionError 保留 Windows ACL/权限操作的分类、脱敏路径和原始错误链。
// Code 为 Win32 错误码；Kind 目前明确覆盖 5 和 1314，其他错误为 Unknown。
type WindowsPermissionError struct {
	Summary   string
	Operation string
	Path      string
	Code      uint32
	Kind      WindowsPermissionKind
	Err       error

	rawPath string
}

// WindowsACLError 是 WindowsPermissionError 的语义别名，供 ACL 调用方使用。
type WindowsACLError = WindowsPermissionError

// WindowsSecurityError 是 WindowsPermissionError 的语义别名，供安全操作调用方使用。
type WindowsSecurityError = WindowsPermissionError

// String 返回稳定的权限错误分类名称。
func (k WindowsPermissionKind) String() string {
	switch k {
	case WindowsPermissionDenied:
		return "access_denied"
	case WindowsPrivilegeNotHeld:
		return "privilege_not_held"
	default:
		return "unknown"
	}
}

// Error 返回不含原始路径的诊断文本。
func (e *WindowsPermissionError) Error() string {
	if e == nil {
		return "Windows permission operation failed"
	}
	path := e.rawPath
	if path == "" {
		path = e.Path
	}
	summary := e.Summary
	if summary == "" {
		summary = "Windows permission operation failed"
	}
	operation := e.Operation
	if operation == "" {
		operation = "unknown"
	}
	code := "unknown"
	if e.Code != 0 {
		code = fmt.Sprintf("%d", e.Code)
	}
	detail := "<nil>"
	if e.Err != nil {
		detail = SafeErrorForPath(e.Err, path)
	}
	return fmt.Sprintf(
		"%s %s: windows_operation=%s windows_permission_kind=%s windows_error_code=%s: %s",
		summary,
		RedactPath(path),
		operation,
		e.Kind.String(),
		code,
		detail,
	)
}

// Unwrap 保留底层 Win32 错误，支持 errors.Is/errors.As 穿透。
func (e *WindowsPermissionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Is 提供稳定权限分类哨兵，同时继续委托底层错误链匹配。
func (e *WindowsPermissionError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrWindowsPermissionDenied:
		return e.Kind == WindowsPermissionDenied
	case ErrWindowsPrivilegeNotHeld:
		return e.Kind == WindowsPrivilegeNotHeld
	default:
		return errors.Is(e.Err, target)
	}
}

// Win32Code 返回底层 Win32 错误码；未知错误返回 0。
func (e *WindowsPermissionError) Win32Code() uint32 {
	if e == nil {
		return 0
	}
	return e.Code
}

// WindowsPermissionCode 从错误链读取 typed Win32 权限码；非 typed 错误返回 false。
// 调用方据此把 5/1314 分类为 authorization_required，而不会猜测原始 errno。
func WindowsPermissionCode(err error) (uint32, bool) {
	var permissionErr *WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil || permissionErr.Code == 0 {
		return 0, false
	}
	return permissionErr.Code, true
}

// PermissionKind 返回稳定的 Windows 权限错误分类。
func (e *WindowsPermissionError) PermissionKind() WindowsPermissionKind {
	if e == nil {
		return WindowsPermissionUnknown
	}
	return e.Kind
}

// NewWindowsPermissionError 将 Windows ACL/权限操作错误包装为可跨包识别的类型。
// 非 Windows 调用方可使用该构造器创建合成错误，但原始 Unix 错误不会被自动分类。
func NewWindowsPermissionError(operation, path string, cause error) error {
	if cause == nil {
		return nil
	}
	return newWindowsPermissionError(operation, operation, path, cause)
}

// ClassifyWindowsPermissionError 返回错误链中的已知 Windows 权限分类。
// 未经 Windows 包装的普通错误（包括非 Windows 的同数值 errno）不会被误判。
func ClassifyWindowsPermissionError(err error) (WindowsPermissionKind, bool) {
	var permissionErr *WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		return WindowsPermissionUnknown, false
	}
	if permissionErr.Kind == WindowsPermissionUnknown {
		return WindowsPermissionUnknown, false
	}
	return permissionErr.Kind, true
}

// IsWindowsPermissionDenied 判断错误链是否包含 Windows 访问拒绝分类。
func IsWindowsPermissionDenied(err error) bool {
	return errors.Is(err, ErrWindowsPermissionDenied)
}

// IsWindowsPrivilegeNotHeld 判断错误链是否包含 Windows 权限未持有分类。
func IsWindowsPrivilegeNotHeld(err error) bool {
	return errors.Is(err, ErrWindowsPrivilegeNotHeld)
}

// newWindowsSecurityOperationError 保留 Windows 错误链，并只输出脱敏路径、操作阶段和稳定错误码。
func newWindowsSecurityOperationError(summary, operation, path string, cause error) error {
	return newWindowsPermissionError(summary, operation, path, cause)
}

func newWindowsPermissionError(summary, operation, path string, cause error) error {
	if cause == nil {
		return nil
	}
	code, _ := windowsErrorCode(cause)
	return &WindowsPermissionError{
		Summary:   summary,
		Operation: operation,
		Path:      RedactPath(path),
		Code:      code,
		Kind:      classifyWindowsPermissionCode(code),
		Err:       cause,
		rawPath:   path,
	}
}

func classifyWindowsPermissionCode(code uint32) WindowsPermissionKind {
	switch code {
	case 5:
		return WindowsPermissionDenied
	case 1314:
		return WindowsPrivilegeNotHeld
	default:
		return WindowsPermissionUnknown
	}
}

func windowsErrorCode(err error) (uint32, bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return 0, false
	}
	return uint32(errno), true
}
