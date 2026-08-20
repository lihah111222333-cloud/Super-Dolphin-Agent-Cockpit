//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	ownedCommandTermGrace    = 100 * time.Millisecond
	ownedCommandPollInterval = 10 * time.Millisecond
	ownedCommandWaitDelay    = time.Second
	ownedCommandWaitLimit    = 2 * time.Second
)

type ownedGroupStillPresentError struct{}

// Error 返回进程组仍存在的稳定错误文本。
func (ownedGroupStillPresentError) Error() string {
	return "owned command process group remains"
}

// runOwnedCommand 在独立 Unix session/PGID 中运行命令，并在取消时回收整个进程组。
func runOwnedCommand(ctx context.Context, command *exec.Cmd) error {
	if err := validateOwnedCommandInput(ctx, command); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("owned command context is already done: %w", err)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.WaitDelay = ownedCommandWaitDelay
	if err := command.Start(); err != nil {
		return fmt.Errorf("start owned command: %w", err)
	}
	if command.Process == nil || command.Process.Pid <= 1 {
		return abortUnownedCommand(command, nil, errors.New("owned command did not expose a valid process"))
	}
	waitDone := waitOwnedCommand(command)
	pgid, err := verifyOwnedProcessGroup(command.Process.Pid)
	if err != nil {
		return abortUnownedCommand(command, waitDone, err)
	}
	return finishOwnedCommand(ctx, waitDone, pgid)
}

// validateOwnedCommandInput 拒绝无法证明独占 ownership 的命令输入。
func validateOwnedCommandInput(ctx context.Context, command *exec.Cmd) error {
	if ctx == nil {
		return errors.New("owned command context is nil")
	}
	if command == nil {
		return errors.New("owned command is nil")
	}
	if command.SysProcAttr != nil {
		return errors.New("owned command SysProcAttr must be unset")
	}
	if command.Process != nil || command.ProcessState != nil {
		return errors.New("owned command must not already be started")
	}
	if command.Cancel != nil || command.WaitDelay != 0 {
		return errors.New("owned command must not carry external cancellation or wait policy")
	}
	if command.Path == "" || len(command.Args) == 0 {
		return errors.New("owned command must have an executable path and arguments")
	}
	return nil
}

// waitOwnedCommand 只建立一个可回收的 Wait goroutine，并用有缓冲通道保存结果。
func waitOwnedCommand(command *exec.Cmd) <-chan error {
	done := make(chan error, 1)
	var waiter sync.WaitGroup
	waiter.Go(func() {
		done <- command.Wait()
	})
	return done
}

// verifyOwnedProcessGroup 确认 Setsid 创建的 PGID 与根 PID 完全一致。
func verifyOwnedProcessGroup(pid int) (int, error) {
	pgid, err := syscall.Getpgid(pid)
	if err == nil {
		if pgid != pid {
			return 0, fmt.Errorf("owned process group %d does not belong to pid %d", pgid, pid)
		}
		return pgid, nil
	}
	if !errors.Is(err, syscall.ESRCH) {
		return 0, fmt.Errorf("verify owned process group for pid %d: %w", pid, err)
	}
	if _, probeErr := ownedGroupExists(pid); probeErr != nil {
		return 0, fmt.Errorf("verify owned process group for pid %d after leader exit: %w", pid, probeErr)
	}
	// Setsid was installed by this helper; ESRCH means the leader exited before Getpgid.
	return pid, nil
}

// abortUnownedCommand 只终止根进程，不向未知进程组发送信号，并保留 ownership 根因。
func abortUnownedCommand(command *exec.Cmd, waitDone <-chan error, ownershipErr error) error {
	errorsToJoin := []error{ownershipErr}
	if command != nil && command.Process != nil {
		if err := killUnownedRoot(command); err != nil {
			errorsToJoin = append(errorsToJoin, err)
		}
	}
	if waitDone != nil {
		waitErr, timeoutErr := awaitOwnedWait(waitDone)
		if timeoutErr != nil {
			errorsToJoin = append(errorsToJoin, timeoutErr)
		} else if waitErr != nil {
			errorsToJoin = append(errorsToJoin, fmt.Errorf("reap unowned command: %w", waitErr))
		}
	}
	return errors.Join(errorsToJoin...)
}

// killUnownedRoot 仅杀死 ownership 未确认时的根进程，避免宽泛 kill。
func killUnownedRoot(command *exec.Cmd) error {
	err := command.Process.Kill()
	if err == nil || errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("kill unowned command root: %w", err)
}

// finishOwnedCommand 统一处理正常退出、非零退出和 context 取消。
func finishOwnedCommand(ctx context.Context, waitDone <-chan error, pgid int) error {
	select {
	case waitErr := <-waitDone:
		return finishCompletedOwnedCommand(ctx, waitErr, pgid)
	case <-ctx.Done():
		return finishCanceledOwnedCommand(ctx.Err(), waitDone, pgid)
	}
}

// finishCompletedOwnedCommand 在根退出后确认没有遗留同组 descendant。
func finishCompletedOwnedCommand(ctx context.Context, waitErr error, pgid int) error {
	rootErr := wrapOwnedWaitError(waitErr)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(fmt.Errorf("owned command context canceled: %w", ctxErr), rootErr, terminateOwnedGroup(pgid))
	}
	exists, err := ownedGroupExists(pgid)
	if err != nil {
		return errors.Join(rootErr, err)
	}
	if !exists {
		return rootErr
	}
	cleanupErr := terminateOwnedGroup(pgid)
	descendantErr := errors.New("owned command exited but left process-group descendants")
	return errors.Join(rootErr, descendantErr, cleanupErr)
}

// finishCanceledOwnedCommand 先终止整个负 PGID，再有界回收根进程并保留 ctx 根因。
func finishCanceledOwnedCommand(ctxErr error, waitDone <-chan error, pgid int) error {
	errorsToJoin := []error{fmt.Errorf("owned command context canceled: %w", ctxErr)}
	if err := terminateOwnedGroup(pgid); err != nil {
		errorsToJoin = append(errorsToJoin, err)
	}
	waitErr, timeoutErr := awaitOwnedWait(waitDone)
	if timeoutErr != nil {
		errorsToJoin = append(errorsToJoin, timeoutErr)
	} else if waitErr != nil {
		errorsToJoin = append(errorsToJoin, fmt.Errorf("reap canceled owned command: %w", waitErr))
	}
	return errors.Join(errorsToJoin...)
}

// wrapOwnedWaitError 保留 exec.ExitError，使调用方仍可判断非零退出根因。
func wrapOwnedWaitError(waitErr error) error {
	if waitErr == nil {
		return nil
	}
	return fmt.Errorf("owned command exited: %w", waitErr)
}

// terminateOwnedGroup 按 TERM、短 grace、KILL 顺序回收并确认整个进程组消失。
func terminateOwnedGroup(pgid int) error {
	errorsToJoin := make([]error, 0, 3)
	if err := signalOwnedGroup(pgid, syscall.SIGTERM); err != nil {
		errorsToJoin = append(errorsToJoin, err)
	}
	if err := waitOwnedGroupGone(pgid, ownedCommandTermGrace); err != nil {
		if _, ok := errors.AsType[ownedGroupStillPresentError](err); !ok {
			errorsToJoin = append(errorsToJoin, err)
		}
	}
	if err := signalOwnedGroup(pgid, syscall.SIGKILL); err != nil {
		errorsToJoin = append(errorsToJoin, err)
	}
	if err := waitOwnedGroupGone(pgid, ownedCommandWaitLimit); err != nil {
		errorsToJoin = append(errorsToJoin, err)
	}
	return errors.Join(errorsToJoin...)
}

// signalOwnedGroup 只向精确的负 PGID 发送信号，ESRCH 表示组已消失。
func signalOwnedGroup(pgid int, signal syscall.Signal) error {
	if pgid <= 1 {
		return fmt.Errorf("owned process group id %d is invalid", pgid)
	}
	if err := syscall.Kill(-pgid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal owned process group %d with %d: %w", pgid, signal, err)
	}
	return nil
}

// waitOwnedGroupGone 有界轮询负 PGID，确认组内没有存活成员。
func waitOwnedGroupGone(pgid int, limit time.Duration) error {
	ticker := time.NewTicker(ownedCommandPollInterval)
	defer ticker.Stop()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		exists, err := ownedGroupExists(pgid)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			return ownedGroupStillPresentError{}
		}
	}
}

// ownedGroupExists 使用 signal 0 探测精确 PGID，不扫描或杀死其他进程。
func ownedGroupExists(pgid int) (bool, error) {
	if pgid <= 1 {
		return false, fmt.Errorf("owned process group id %d is invalid", pgid)
	}
	err := syscall.Kill(-pgid, 0)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, fmt.Errorf("probe owned process group %d: %w", pgid, err)
}

// awaitOwnedWait 在固定上限内取得唯一 Wait 结果，禁止无界等待。
func awaitOwnedWait(waitDone <-chan error) (error, error) {
	timer := time.NewTimer(ownedCommandWaitLimit)
	defer timer.Stop()
	select {
	case err := <-waitDone:
		return err, nil
	case <-timer.C:
		return nil, errors.New("owned command Wait exceeded bounded reap limit")
	}
}
