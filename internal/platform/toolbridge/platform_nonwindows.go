//go:build !windows

package toolbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

type stdioProcessGuard struct{}

// stdioConfigureCommand 让 Unix stdio MCP 子进程进入独立进程组，便于整组终止。
func stdioConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// stdioStartGuardedProcess 在 Unix 上配置独立进程组并启动 stdio MCP 子进程。
func stdioStartGuardedProcess(cmd *exec.Cmd, _ bool) (*stdioProcessGuard, error) {
	if cmd == nil {
		return nil, errors.New("toolbridge: nil stdio MCP command")
	}
	stdioConfigureCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, stdioAbortStartedProcess(cmd))
	}
	return &stdioProcessGuard{}, nil
}

// stdioAbortStartedProcess 终止并等待启动失败后仍然存在的 Unix 子进程。
func stdioAbortStartedProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return errors.Join(cmd.Process.Kill(), cmd.Wait())
}

// stdioTerminateProcessTree 优先杀掉 Unix 进程组，失败时再回退到单进程 Kill。
func stdioTerminateProcessTree(cmd *exec.Cmd, _ *stdioProcessGuard) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("toolbridge: invalid stdio MCP pid")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil || stdioProcessGone(err) {
		return nil
	}
	err := cmd.Process.Kill()
	if stdioProcessGone(err) {
		err = nil
	}
	return err
}

// stdioExpectedCloseWaitError 把预期内的 stdio 关闭错误归一化。
func stdioExpectedCloseWaitError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			switch status.Signal() {
			case syscall.SIGPIPE, syscall.SIGKILL, syscall.SIGTERM:
				return nil
			}
		}
	}
	return err
}

// stdioCleanupProcessTree 在关闭客户端后补杀残留进程组。
func stdioCleanupProcessTree(cmd *exec.Cmd, _ *stdioProcessGuard) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !stdioProcessGone(err) {
		return err
	}
	return nil
}

// stdioProcessGone 判断进程或进程组是否已经退出。
func stdioProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

// windowsACLApprovalRequest 在非 Windows 宿主上保持严格无操作；即使 peer
// 伪造 Windows 元数据，也不会改变原工具结果或触发 UI 审批。
func (h *Handler) windowsACLApprovalRequest(_ *mcpcontrol.ToolInstance, _ ToolCallRequest, _ *ToolCallResult) (contract.ApprovalRequest, bool) {
	return contract.ApprovalRequest{}, false
}

// logWindowsACLApprovalDecision 是非 Windows 编译面的空实现；调用门禁永远
// 返回 false，因此产品运行时不会到达这里。
func (h *Handler) logWindowsACLApprovalDecision(_ contract.ApprovalRequest, _ contract.ApprovalDecision, _ error) {
}

// handleSchemaValidationAuthorization 在非 Windows 上严格 no-op；schema typed 错误
// 保持原有 fail-fast，绝不创建 Windows approval envelope 或重试。
func (h *Handler) handleSchemaValidationAuthorization(
	context.Context,
	codexToolEntry,
	ToolCallRequest,
	error,
) schemaValidationAuthorizationDecision {
	return schemaValidationAuthorizationDecision{}
}
