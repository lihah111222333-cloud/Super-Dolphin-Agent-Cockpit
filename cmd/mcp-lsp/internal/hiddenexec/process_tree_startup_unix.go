//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"golang.org/x/sys/unix"
)

type startupAbortHooks struct {
	// Signals are injected in contract tests; production defaults stay fail-closed.
	captureIdentity func(int) (ProcessIdentity, error)
	groupOwned      func(int) (bool, error)
	startWait       func(*exec.Cmd) chan error
	waitTimeout     time.Duration
	killGroup       func(int) error
	killProcess     func(*exec.Cmd) error
}

// abortStartedProcessTree 在 Unix 启动绑定失败时执行零信号优先的有界清理。
func abortStartedProcessTree(cmd *exec.Cmd, hooks startupAbortHooks, expected *ProcessIdentity) (error, *ProcessTree) {
	if cmd == nil || cmd.Process == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup abort: process owner is unavailable")), nil
	}
	pid := cmd.Process.Pid
	waitDone := hooks.startWait(cmd)
	owned, ownershipErr := hooks.groupOwned(pid)
	actionErr, pending := startupAbortAction(cmd, pid, expected, owned, ownershipErr, hooks)
	waitErr := waitStartedProcess(pid, waitDone, hooks.waitTimeout)
	if waitErr != nil {
		pending = true
		if actionErr == nil {
			if err := exactStartupKill(cmd, pid, nil, expected, hooks.captureIdentity, hooks.killProcess); err != nil {
				actionErr = errors.Join(actionErr, fmt.Errorf("startup abort: kill exact root after wait timeout: %w", err))
			}
		}
	}
	if pending || actionErr != nil || waitErr != nil {
		return errors.Join(ErrProcessTreeCleanupPending, actionErr, waitErr), newStartupProcessTreeWithIdentity(cmd, waitDone, identityValue(expected), expected != nil, hooks.captureIdentity, true, nil)
	}
	return nil, nil
}

// startupAbortAction 只有在启动身份与 action-time 身份一致且 owner 可证明时才发送信号。
func startupAbortAction(cmd *exec.Cmd, pid int, expected *ProcessIdentity, owned bool, ownershipErr error, hooks startupAbortHooks) (error, bool) {
	if err := verifyStartupProcessIdentity(pid, identityValue(expected), expected != nil, hooks.captureIdentity); err != nil {
		return fmt.Errorf("startup abort: verify immutable root identity for pid %d: %w", pid, err), true
	}
	if ownershipErr != nil {
		actionErr := fmt.Errorf("startup abort: verify independent session/PGID for pid %d: %w", pid, ownershipErr)
		return actionErr, true
	}
	if !owned {
		actionErr := fmt.Errorf("startup abort: pid %d is not an independently owned session/PGID", pid)
		return actionErr, true
	}
	killGroup := hooks.killGroup
	if killGroup == nil {
		killGroup = defaultStartupKillGroup
	}
	if err := killGroup(pid); err != nil && !isProcessGone(err) {
		return fmt.Errorf("startup abort: kill process group %d: %w", pid, err), true
	}
	return nil, false
}

// exactStartupKill 重读身份后才允许对 retained startup root 发送精确信号。
func exactStartupKill(cmd *exec.Cmd, pid int, prior error, expected *ProcessIdentity, captureIdentity func(int) (ProcessIdentity, error), killProcess func(*exec.Cmd) error) error {
	if err := verifyStartupProcessIdentity(pid, identityValue(expected), expected != nil, captureIdentity); err != nil {
		return errors.Join(prior, err)
	}
	if killProcess == nil {
		killProcess = defaultStartupKillProcess
	}
	if err := killProcess(cmd); err != nil && !isProcessGone(err) {
		return errors.Join(prior, fmt.Errorf("startup abort: kill exact root process %d: %w", pid, err))
	}
	return prior
}

func identityValue(identity *ProcessIdentity) ProcessIdentity {
	if identity == nil {
		return ProcessIdentity{}
	}
	return *identity
}

func startStartupProcessWait(cmd *exec.Cmd) chan error {
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.startup-abort-wait", func(context.Context) {
		waitDone <- cmd.Wait()
	})
	return waitDone
}

// waitStartedProcess 在有界等待内保存唯一 Wait 结果，超时交由 retained owner 重试。
func waitStartedProcess(pid int, waitDone chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waitDone:
		// Preserve the single result for the retained startup owner.
		waitDone <- err
		var exitErr *exec.ExitError
		if err == nil || errors.As(err, &exitErr) || isProcessGone(err) {
			return nil
		}
		return fmt.Errorf("startup abort: reap process %d: %w", pid, err)
	case <-timer.C:
		return fmt.Errorf("startup abort: process %d did not exit within %s", pid, timeout)
	}
}

func startupProcessGroupOwned(pid int) (bool, error) {
	pgid, err := unix.Getpgid(pid)
	if err != nil {
		return false, err
	}
	sid, err := unix.Getsid(pid)
	if err != nil {
		return false, err
	}
	return pgid == pid && sid == pid, nil
}
