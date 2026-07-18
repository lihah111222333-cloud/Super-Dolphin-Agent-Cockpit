//go:build !windows

package schema

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

type processGuard struct {
	identity                pidregistry.StableProcessIdentity
	groupID                 int
	captureIdentity         func(int) (pidregistry.StableProcessIdentity, error)
	killGroup               func(int, syscall.Signal) error
	beforeWaitResultPublish func()
}

func configureProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// attachProcessGuard 捕获启动时稳定 PID identity 与独占 PGID。
func attachProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("process is not started")
	}
	groupID, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	if groupID != cmd.Process.Pid {
		return nil, errors.New("child process group is not isolated")
	}
	identity, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	return &processGuard{
		identity:        identity,
		groupID:         groupID,
		captureIdentity: pidregistry.CaptureStableProcessIdentity,
		killGroup:       syscall.Kill,
	}, nil
}

// terminateProcessTree 终止 helper 的整个 Unix 进程组。
func terminateProcessTree(cmd *exec.Cmd, guard *processGuard) error {
	if err := validateProcessGuard(cmd, guard); err != nil {
		return err
	}
	owned, err := processGuardOwnsCurrentProcess(guard)
	if err != nil || !owned {
		return err
	}
	return killVerifiedProcessGroup(guard)
}

// validateProcessGuard 校验命令、稳定身份与进程组仍属于同一个启动实例。
func validateProcessGuard(cmd *exec.Cmd, guard *processGuard) error {
	if cmd == nil || cmd.Process == nil || guard == nil ||
		guard.captureIdentity == nil || guard.killGroup == nil {
		return errors.New("process is not started")
	}
	if cmd.Process.Pid != guard.identity.PID || guard.groupID != guard.identity.PID {
		return errors.New("schema worker process guard identity is invalid")
	}
	return nil
}

func processGuardOwnsCurrentProcess(guard *processGuard) (bool, error) {
	current, err := guard.captureIdentity(guard.identity.PID)
	if errors.Is(err, pidregistry.ErrStableProcessNotFound) ||
		errors.Is(err, pidregistry.ErrStableProcessIdentityMismatch) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.ProcessStartToken == guard.identity.ProcessStartToken &&
		current.ExecutableIdentity == guard.identity.ExecutableIdentity, nil
}

// killVerifiedProcessGroup 在最终 PGID 复验通过后才发送 SIGKILL。
func killVerifiedProcessGroup(guard *processGuard) error {
	groupID, err := syscall.Getpgid(guard.identity.PID)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	if groupID != guard.groupID {
		return errors.New("schema worker process group identity changed")
	}
	err = guard.killGroup(-guard.groupID, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func waitGuardedProcess(cmd *exec.Cmd, guard *processGuard) error {
	err := cmd.Wait()
	if guard != nil && guard.beforeWaitResultPublish != nil {
		guard.beforeWaitResultPublish()
	}
	return err
}

func closeProcessGuard(_ *processGuard) error {
	return nil
}
