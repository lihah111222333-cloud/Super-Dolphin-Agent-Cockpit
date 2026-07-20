//go:build !windows

package gate

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func configureCommandCancellation(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	command.WaitDelay = 5 * time.Second
}

func runConfiguredCommand(command *exec.Cmd) error {
	runErr := command.Run()
	waitErr := waitForCommandProcessGroup(command)
	if waitErr == nil {
		return runErr
	}
	if runErr == nil {
		return waitErr
	}
	return errors.Join(runErr, waitErr)
}

// waitForCommandProcessGroup 确认父进程返回后组内没有仍可写执行区的后代进程。
func waitForCommandProcessGroup(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	processGroupID := command.Process.Pid
	if err := terminateCommandProcessGroup(processGroupID); err != nil {
		return err
	}
	return waitCommandProcessGroupGone(processGroupID)
}

// terminateCommandProcessGroup 只把 ESRCH 视为进程组已不存在。
func terminateCommandProcessGroup(processGroupID int) error {
	inspectErr := syscall.Kill(-processGroupID, 0)
	if errors.Is(inspectErr, syscall.ESRCH) {
		return nil
	}
	if inspectErr != nil {
		return fmt.Errorf("inspect executor process group %d: %w", processGroupID, inspectErr)
	}
	if err := syscall.Kill(-processGroupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate executor process group %d: %w", processGroupID, err)
	}
	return nil
}

// waitCommandProcessGroupGone 用有界 ticker 等待进程组稳定消失。
func waitCommandProcessGroupGone(processGroupID int) error {
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	absentObservations := 0
	for {
		select {
		case <-poll.C:
			gone, err := commandProcessGroupGone(processGroupID)
			if err != nil {
				return err
			}
			if gone {
				absentObservations++
				if absentObservations == 3 {
					return nil
				}
				continue
			}
			absentObservations = 0
		case <-timeout.C:
			return fmt.Errorf("executor process group %d did not terminate", processGroupID)
		}
	}
}
