//go:build windows

package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	// Windows CreationFlags 常量用于隐藏 stdio MCP 窗口并创建独立进程组。
	stdioCreateNewProcessGroup = 0x00000200
	stdioCreateNoWindow        = 0x08000000
	stdioCreateSuspended       = 0x00000004
	stdioJobKillOnClose        = 0x00002000
	stdioJobBreakawayOK        = 0x00000800

	windowsACLApprovalKind                     = "windows_acl"
	windowsACLAuthorizationRequiredCode        = "authorization_required"
	windowsACLAccessDeniedCode          uint32 = 5
	windowsACLPrivilegeNotHeldCode      uint32 = 1314
)

// stdioProcessGuard 保存 Windows Job Object 句柄，Close 时负责释放整棵进程树。
type stdioProcessGuard struct {
	handle windows.Handle
}

type stdioWindowsOps struct {
	createJobObject          func() (windows.Handle, error)
	setInformationJobObject  func(windows.Handle, *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error
	openProcess              func(uint32, bool, uint32) (windows.Handle, error)
	assignProcessToJobObject func(windows.Handle, windows.Handle) error
	resumeProcess            func(windows.Handle) error
	terminateJobObject       func(windows.Handle, uint32) error
	closeHandle              func(windows.Handle) error
}

func newStdioWindowsOps() stdioWindowsOps {
	return stdioWindowsOps{
		createJobObject: windowsCreateJobObject,
		setInformationJobObject: func(handle windows.Handle, info *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
			_, err := windows.SetInformationJobObject(
				handle,
				windows.JobObjectExtendedLimitInformation,
				uintptr(unsafe.Pointer(info)),
				uint32(unsafe.Sizeof(*info)),
			)
			return err
		},
		openProcess:              windows.OpenProcess,
		assignProcessToJobObject: windows.AssignProcessToJobObject,
		resumeProcess:            stdioResumeProcess,
		terminateJobObject:       windows.TerminateJobObject,
		closeHandle:              windows.CloseHandle,
	}
}

func windowsCreateJobObject() (windows.Handle, error) {
	return windows.CreateJobObject(nil, nil)
}

func (ops stdioWindowsOps) validate() error {
	switch {
	case ops.createJobObject == nil:
		return errors.New("toolbridge: stdio Windows create-job operation is nil")
	case ops.setInformationJobObject == nil:
		return errors.New("toolbridge: stdio Windows set-job operation is nil")
	case ops.openProcess == nil:
		return errors.New("toolbridge: stdio Windows open-process operation is nil")
	case ops.assignProcessToJobObject == nil:
		return errors.New("toolbridge: stdio Windows assign-job operation is nil")
	case ops.resumeProcess == nil:
		return errors.New("toolbridge: stdio Windows resume-process operation is nil")
	case ops.terminateJobObject == nil:
		return errors.New("toolbridge: stdio Windows terminate-job operation is nil")
	case ops.closeHandle == nil:
		return errors.New("toolbridge: stdio Windows close-handle operation is nil")
	default:
		return nil
	}
}

// stdioConfigureCommand 配置 Windows stdio MCP 子进程的隐藏窗口和独立进程组。
func stdioConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: stdioCreateSuspended | stdioCreateNewProcessGroup | stdioCreateNoWindow,
		HideWindow:    true,
	}
}

// stdioStartGuardedProcess 创建 Job Object，挂接挂起的子进程，再恢复执行。
func stdioStartGuardedProcess(cmd *exec.Cmd, allowBreakaway bool) (*stdioProcessGuard, error) {
	return stdioStartGuardedProcessWithOps(cmd, allowBreakaway, newStdioWindowsOps())
}

// stdioStartGuardedProcessWithOps 为 Windows 生命周期测试提供 Win32 操作故障注入 seam。
func stdioStartGuardedProcessWithOps(cmd *exec.Cmd, allowBreakaway bool, ops stdioWindowsOps) (*stdioProcessGuard, error) {
	if cmd == nil {
		return nil, errors.New("toolbridge: nil stdio MCP command")
	}
	if cmd.Process != nil {
		return nil, errors.New("toolbridge: stdio MCP command already started")
	}
	if err := ops.validate(); err != nil {
		return nil, err
	}

	jobHandle, err := stdioCreateKillOnCloseJobWithOps(allowBreakaway, ops)
	if err != nil {
		return nil, fmt.Errorf("toolbridge: create stdio MCP job: %w", err)
	}
	stdioConfigureCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, stdioAbortWindowsProcess(cmd, jobHandle, 0, false, ops, err)
	}

	processHandle, err := ops.openProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return nil, stdioAbortWindowsProcess(cmd, jobHandle, 0, false, ops, fmt.Errorf("open stdio MCP process: %w", err))
	}
	if err := ops.assignProcessToJobObject(jobHandle, processHandle); err != nil {
		return nil, stdioAbortWindowsProcess(cmd, jobHandle, processHandle, false, ops, fmt.Errorf("assign stdio MCP process to job: %w", err))
	}
	if err := ops.resumeProcess(processHandle); err != nil {
		return nil, stdioAbortWindowsProcess(cmd, jobHandle, processHandle, true, ops, fmt.Errorf("resume stdio MCP process: %w", err))
	}
	if err := ops.closeHandle(processHandle); err != nil && !stdioProcessGone(err) {
		return nil, stdioAbortWindowsProcess(cmd, jobHandle, processHandle, true, ops, fmt.Errorf("close stdio MCP process handle: %w", err))
	}
	return &stdioProcessGuard{handle: jobHandle}, nil
}

// stdioAbortWindowsProcess 终止、等待并关闭 guarded-start 任一阶段失败后的所有资源。
func stdioAbortWindowsProcess(
	cmd *exec.Cmd,
	jobHandle windows.Handle,
	processHandle windows.Handle,
	assigned bool,
	ops stdioWindowsOps,
	cause error,
) error {
	cleanupErrs := make([]error, 0, 4)
	if assigned {
		if err := ops.terminateJobObject(jobHandle, 1); err != nil && !stdioProcessGone(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("terminate failed stdio MCP job: %w", err))
		}
	}
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !stdioProcessGone(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("kill failed stdio MCP process: %w", err))
		}
		if err := cmd.Wait(); err != nil && !stdioProcessGone(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("wait failed stdio MCP process: %w", err))
		}
	}
	if processHandle != 0 {
		if err := ops.closeHandle(processHandle); err != nil && !stdioProcessGone(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("close stdio MCP process handle: %w", err))
		}
	}
	if jobHandle != 0 {
		if err := ops.closeHandle(jobHandle); err != nil && !stdioProcessGone(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("close stdio MCP job handle: %w", err))
		}
	}
	return errors.Join(cause, errors.Join(cleanupErrs...))
}

// stdioTerminateProcessTree 优先终止 Job Object，失败时再尝试 Kill 单个进程。
func stdioTerminateProcessTree(cmd *exec.Cmd, guard *stdioProcessGuard) error {
	ops := newStdioWindowsOps()
	var jobErr error
	if guard != nil && guard.handle != 0 {
		if err := ops.terminateJobObject(guard.handle, 1); err == nil {
			return nil
		} else if !stdioProcessGone(err) {
			jobErr = err
		}
	}
	if cmd == nil || cmd.Process == nil {
		return jobErr
	}
	err := cmd.Process.Kill()
	if stdioProcessGone(err) {
		err = nil
	}
	return errors.Join(jobErr, err)
}

// stdioExpectedCloseWaitError 在 Windows 上保留 Wait 错误，由调用方决定是否上报。
func stdioExpectedCloseWaitError(err error) error {
	return err
}

// stdioCleanupProcessTree 关闭 Job Object 句柄；KillOnClose 会清理仍挂住的子进程。
func stdioCleanupProcessTree(_ *exec.Cmd, guard *stdioProcessGuard) error {
	if guard == nil || guard.handle == 0 {
		return nil
	}
	err := newStdioWindowsOps().closeHandle(guard.handle)
	guard.handle = 0
	if stdioProcessGone(err) {
		return nil
	}
	return err
}

func stdioCreateKillOnCloseJobWithOps(allowBreakaway bool, ops stdioWindowsOps) (windows.Handle, error) {
	if err := ops.validate(); err != nil {
		return 0, err
	}
	h, err := ops.createJobObject()
	if err != nil {
		return 0, err
	}
	limitFlags := uint32(stdioJobKillOnClose)
	if allowBreakaway {
		limitFlags |= stdioJobBreakawayOK
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: limitFlags,
		},
	}
	if err := ops.setInformationJobObject(h, &info); err != nil {
		return 0, errors.Join(err, ops.closeHandle(h))
	}
	return h, nil
}

var stdioNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func stdioResumeProcess(handle windows.Handle) error {
	status, _, callErr := stdioNtResumeProcess.Call(uintptr(handle))
	if status == 0 {
		return nil
	}
	if callErr != nil {
		return fmt.Errorf("NtResumeProcess returned NTSTATUS %#x: %w", status, callErr)
	}
	return fmt.Errorf("NtResumeProcess returned NTSTATUS %#x", status)
}

// stdioProcessGone 判断 Windows 进程或 Job 句柄是否已不可用。
func stdioProcessGone(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}

// windowsACLAuthorization 只保留弹窗需要的稳定分类，不携带路径、错误文本
// 或 peer 自报身份，避免权限错误借审批 payload 泄露本机信息。
type windowsACLAuthorization struct {
	ErrorCode      uint32
	PermissionKind string
}

// windowsACLAuthorizationEnvelope 是 mcp-lsp 类型化错误的最小可信形状。
// Success 必须显式为 false，Code 与 meta 的三个字段也必须完全一致。
type windowsACLAuthorizationEnvelope struct {
	Success   *bool           `json:"success"`
	Error     string          `json:"error,omitempty"`
	Code      string          `json:"code"`
	Retryable bool            `json:"retryable,omitempty"`
	Hint      string          `json:"hint,omitempty"`
	Meta      json.RawMessage `json:"meta"`
}

type windowsACLAuthorizationMeta struct {
	AuthorizationRequired *bool  `json:"authorization_required"`
	WindowsErrorCode      uint32 `json:"windows_error_code"`
	WindowsPermissionKind string `json:"windows_permission_kind"`
}

// windowsACLAuthorizationFromResult 仅解析受信 MCP _meta；模型可见文本及
// structuredContent 即使包含相似字样也不会触发授权弹窗。
func windowsACLAuthorizationFromResult(result *ToolCallResult) (windowsACLAuthorization, bool) {
	if result == nil || result.Success {
		return windowsACLAuthorization{}, false
	}
	for _, item := range result.ContentItems {
		if authorization, ok := parseWindowsACLAuthorizationMeta(item.Meta); ok {
			return authorization, true
		}
	}
	return windowsACLAuthorization{}, false
}

func parseWindowsACLAuthorizationMeta(raw json.RawMessage) (windowsACLAuthorization, bool) {
	var meta windowsACLAuthorizationMeta
	if err := json.Unmarshal(raw, &meta); err != nil ||
		meta.AuthorizationRequired == nil || !*meta.AuthorizationRequired {
		return windowsACLAuthorization{}, false
	}
	wantKind := ""
	switch meta.WindowsErrorCode {
	case windowsACLAccessDeniedCode:
		wantKind = "access_denied"
	case windowsACLPrivilegeNotHeldCode:
		wantKind = "privilege_not_held"
	default:
		return windowsACLAuthorization{}, false
	}
	if meta.WindowsPermissionKind != wantKind {
		return windowsACLAuthorization{}, false
	}
	return windowsACLAuthorization{
		ErrorCode:      meta.WindowsErrorCode,
		PermissionKind: wantKind,
	}, true
}

// parseWindowsACLAuthorization 严格解析类型化 envelope，供跨平台契约测试验证；
// 是否允许弹窗仍由 windowsACLAuthorizationFromResult 的宿主平台门禁决定。
func parseWindowsACLAuthorization(raw json.RawMessage) (windowsACLAuthorization, bool) {
	var envelope windowsACLAuthorizationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil ||
		envelope.Success == nil || *envelope.Success ||
		envelope.Code != windowsACLAuthorizationRequiredCode {
		return windowsACLAuthorization{}, false
	}
	var meta windowsACLAuthorizationMeta
	if err := json.Unmarshal(envelope.Meta, &meta); err != nil ||
		meta.AuthorizationRequired == nil || !*meta.AuthorizationRequired {
		return windowsACLAuthorization{}, false
	}
	wantKind := ""
	switch meta.WindowsErrorCode {
	case windowsACLAccessDeniedCode:
		wantKind = "access_denied"
	case windowsACLPrivilegeNotHeldCode:
		wantKind = "privilege_not_held"
	default:
		return windowsACLAuthorization{}, false
	}
	if meta.WindowsPermissionKind != wantKind {
		return windowsACLAuthorization{}, false
	}
	return windowsACLAuthorization{
		ErrorCode:      meta.WindowsErrorCode,
		PermissionKind: wantKind,
	}, true
}

// windowsACLApprovalRequest 使用宿主 ToolCallRequest 的可信身份构造审批。
// peer envelope 和模型 arguments 都不能覆盖 AgentID、ThreadID、TurnID 或 CallID。
func (h *Handler) windowsACLApprovalRequest(instance *mcpcontrol.ToolInstance, req ToolCallRequest, result *ToolCallResult) (contract.ApprovalRequest, bool) {
	clientKind := ""
	if instance != nil {
		clientKind = instance.ClientKind
	}
	return h.windowsACLApprovalRequestForClientKind(clientKind, req, result)
}

// windowsACLApprovalRequestForClientKind 供 peer 与 Codex surface schema 两条受信边界
// 共用；clientKind 只能来自已绑定的宿主实例或 surface entry，不能由错误 payload 覆盖。
func (h *Handler) windowsACLApprovalRequestForClientKind(clientKind string, req ToolCallRequest, result *ToolCallResult) (contract.ApprovalRequest, bool) {
	if h == nil || h.approvalRequester == nil ||
		strings.TrimSpace(clientKind) != mcpdto.ClientKindLSP ||
		classifyTool(req.Name) != mcpdto.ClientKindLSP ||
		strings.TrimSpace(req.CallID) == "" {
		return contract.ApprovalRequest{}, false
	}
	authorization, ok := windowsACLAuthorizationFromResult(result)
	if !ok {
		return contract.ApprovalRequest{}, false
	}
	h.info("toolbridge: Windows ACL authorization requested",
		"windows_error_code", authorization.ErrorCode,
		"windows_permission_kind", authorization.PermissionKind,
	)
	return contract.ApprovalRequest{
		CallID:     strings.TrimSpace(req.CallID),
		ApprovalID: strings.TrimSpace(req.CallID),
		ToolName:   strings.TrimSpace(req.Name),
		AgentID:    strings.TrimSpace(req.AgentID),
		ThreadID:   strings.TrimSpace(req.ThreadID),
		TurnID:     strings.TrimSpace(req.TurnID),
		Reason: fmt.Sprintf(
			"Windows 权限不足（错误码 %d，类型 %s）。批准后将使用同一受管 LSP 实例重试一次。",
			authorization.ErrorCode,
			authorization.PermissionKind,
		),
		Kind:         windowsACLApprovalKind,
		SourceMethod: ProxyMethodToolsCall,
		Payload: map[string]any{
			"authorization_required":  true,
			"windows_error_code":      authorization.ErrorCode,
			"windows_permission_kind": authorization.PermissionKind,
		},
	}, true
}

// logWindowsACLApprovalDecision 只记录稳定分类和决策状态；审批错误正文、工具
// arguments 与路径不进入日志。
func (h *Handler) logWindowsACLApprovalDecision(req contract.ApprovalRequest, decision contract.ApprovalDecision, err error) {
	outcome := "no_decision"
	switch {
	case err != nil:
		outcome = "request_failed"
	case decision.Approved != nil && *decision.Approved:
		outcome = "approved_retry_once"
	case decision.Approved != nil:
		outcome = "denied"
	}
	h.info("toolbridge: Windows ACL authorization resolved",
		"outcome", outcome,
		"windows_error_code", req.Payload["windows_error_code"],
		"windows_permission_kind", req.Payload["windows_permission_kind"],
	)
}

// handleSchemaValidationAuthorization 只处理已恢复为 securefs typed 的 5/1314；
// 没有可信 CallID、LSP family 或 approval requester 时保持 fail-fast，不伪造审批。
func (h *Handler) handleSchemaValidationAuthorization(
	ctx context.Context,
	entry codexToolEntry,
	req ToolCallRequest,
	err error,
) schemaValidationAuthorizationDecision {
	if h == nil || h.approvalRequester == nil || entry.executionKind != "stdio" ||
		strings.TrimSpace(entry.family) != mcpdto.ClientKindLSP ||
		strings.TrimSpace(req.CallID) == "" {
		return schemaValidationAuthorizationDecision{}
	}
	authorization, ok := windowsACLAuthorizationFromSchemaError(err)
	if !ok {
		return schemaValidationAuthorizationDecision{}
	}
	result, resultErr := windowsACLAuthorizationToolResult(authorization)
	if resultErr != nil {
		return schemaValidationAuthorizationDecision{err: resultErr, validationDone: true, handled: true}
	}
	approval, ok := h.windowsACLApprovalRequestForClientKind(entry.family, req, result)
	if !ok {
		return schemaValidationAuthorizationDecision{}
	}
	decision, approvalErr := h.approvalRequester.RequestApproval(ctx, approval)
	h.logWindowsACLApprovalDecision(approval, decision, approvalErr)
	if approvalErr != nil || decision.Approved == nil || !*decision.Approved {
		return schemaValidationAuthorizationDecision{result: result, validationDone: true, handled: true}
	}

	// 批准只允许重做当前 schema validation 一次；第二次失败绝不再次申请或执行工具。
	retryErr := h.validateCodexSurfaceEntryArguments(ctx, entry, req.Arguments)
	if retryErr == nil {
		return schemaValidationAuthorizationDecision{handled: true}
	}
	if _, stillPermission := windowsACLAuthorizationFromSchemaError(retryErr); stillPermission {
		return schemaValidationAuthorizationDecision{result: result, validationDone: true, handled: true}
	}
	if recoveryResult, recoverable := toolCallRecoveryFailureResult(retryErr); recoverable {
		return schemaValidationAuthorizationDecision{result: recoveryResult, validationDone: true, handled: true}
	}
	return schemaValidationAuthorizationDecision{err: retryErr, validationDone: true, handled: true}
}

func windowsACLAuthorizationFromSchemaError(err error) (windowsACLAuthorization, bool) {
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		return windowsACLAuthorization{}, false
	}
	switch permissionErr.Win32Code() {
	case windowsACLAccessDeniedCode:
		return windowsACLAuthorization{ErrorCode: windowsACLAccessDeniedCode, PermissionKind: "access_denied"}, true
	case windowsACLPrivilegeNotHeldCode:
		return windowsACLAuthorization{ErrorCode: windowsACLPrivilegeNotHeldCode, PermissionKind: "privilege_not_held"}, true
	default:
		return windowsACLAuthorization{}, false
	}
}

func windowsACLAuthorizationToolResult(authorization windowsACLAuthorization) (*ToolCallResult, error) {
	authorizationRequired := true
	meta, err := json.Marshal(windowsACLAuthorizationMeta{
		AuthorizationRequired: &authorizationRequired,
		WindowsErrorCode:      authorization.ErrorCode,
		WindowsPermissionKind: authorization.PermissionKind,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Windows authorization result metadata: %w", err)
	}
	result := toolCallErrorResult("Windows authorization is required.")
	if len(result.ContentItems) == 0 {
		return nil, errors.New("Windows authorization result has no content carrier")
	}
	result.ContentItems[0].Meta = meta
	return result, nil
}
