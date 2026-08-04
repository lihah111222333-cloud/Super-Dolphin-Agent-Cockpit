//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"golang.org/x/sys/unix"
)

type startupAbortHooks struct {
	captureIdentity func(int) (ProcessIdentity, error)
	groupOwned      func(int) (bool, error)
	startWait       func(*exec.Cmd) chan error
	waitTimeout     time.Duration
}

func abortStartedProcessTree(cmd *exec.Cmd, hooks startupAbortHooks) (error, *ProcessTree) {
	if cmd == nil || cmd.Process == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup abort: process owner is unavailable")), nil
	}
	pid := cmd.Process.Pid
	waitDone := hooks.startWait(cmd)
	owned, ownershipErr := hooks.groupOwned(pid)
	actionErr, pending := startupAbortAction(cmd, pid, owned, ownershipErr)
	waitErr := waitStartedProcess(pid, waitDone, hooks.waitTimeout)
	if waitErr != nil {
		pending = true
		if err := cmd.Process.Kill(); err != nil && !isProcessGone(err) {
			actionErr = errors.Join(actionErr, fmt.Errorf("startup abort: kill exact root after wait timeout: %w", err))
		}
	}
	if pending || actionErr != nil || waitErr != nil {
		return errors.Join(ErrProcessTreeCleanupPending, actionErr, waitErr), newStartupProcessTree(cmd, waitDone)
	}
	return nil, nil
}

func startupAbortAction(cmd *exec.Cmd, pid int, owned bool, ownershipErr error) (error, bool) {
	if ownershipErr != nil {
		if isProcessGone(ownershipErr) {
			return exactStartupKill(cmd, pid, nil), true
		}
		actionErr := fmt.Errorf("startup abort: verify independent session/PGID for pid %d: %w", pid, ownershipErr)
		return errors.Join(actionErr, exactStartupKill(cmd, pid, nil)), true
	}
	if !owned {
		actionErr := fmt.Errorf("startup abort: pid %d is not an independently owned session/PGID", pid)
		return errors.Join(actionErr, exactStartupKill(cmd, pid, nil)), true
	}
	if err := syscall.Kill(-pid, syscall.Signal(unixForceSignal)); err != nil && !isProcessGone(err) {
		return errors.Join(fmt.Errorf("startup abort: kill process group %d: %w", pid, err), exactStartupKill(cmd, pid, nil)), true
	}
	return nil, false
}

func exactStartupKill(cmd *exec.Cmd, pid int, prior error) error {
	if err := cmd.Process.Kill(); err != nil && !isProcessGone(err) {
		return errors.Join(prior, fmt.Errorf("startup abort: kill exact root process %d: %w", pid, err))
	}
	return prior
}

func startStartupProcessWait(cmd *exec.Cmd) chan error {
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.startup-abort-wait", func(context.Context) {
		waitDone <- cmd.Wait()
	})
	return waitDone
}

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
