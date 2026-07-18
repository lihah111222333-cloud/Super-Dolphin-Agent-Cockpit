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
	leaseProcess            *exec.Cmd
	leaseWriter             *os.File
	killGroup               func(int, syscall.Signal) error
	groupKilled             bool
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
	leaseProcess, leaseWriter, err := acquireProcessGroupLease(groupID)
	if err != nil {
		return nil, err
	}
	return &processGuard{
		identity:     identity,
		groupID:      groupID,
		leaseProcess: leaseProcess,
		leaseWriter:  leaseWriter,
		killGroup:    syscall.Kill,
	}, nil
}

// acquireProcessGroupLease 在目标组内保留一个父进程控制且暂不回收的成员。
func acquireProcessGroupLease(groupID int) (*exec.Cmd, *os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	leaseProcess := exec.Command("/bin/sh", "-c", "read _ || :")
	leaseProcess.Stdin = reader
	leaseProcess.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: groupID}
	if err := leaseProcess.Start(); err != nil {
		return nil, nil, errors.Join(err, reader.Close(), writer.Close())
	}
	if err := reader.Close(); err != nil {
		return nil, nil, errors.Join(err, abortProcessGroupLease(leaseProcess, writer))
	}
	actualGroupID, err := syscall.Getpgid(leaseProcess.Process.Pid)
	if err != nil {
		return nil, nil, errors.Join(err, abortProcessGroupLease(leaseProcess, writer))
	}
	if actualGroupID != groupID {
		err := errors.New("process-group lease joined an unexpected group")
		return nil, nil, errors.Join(err, abortProcessGroupLease(leaseProcess, writer))
	}
	return leaseProcess, writer, nil
}

// abortProcessGroupLease 清理尚未完成绑定的进程组租约。
func abortProcessGroupLease(leaseProcess *exec.Cmd, writer *os.File) error {
	closeErr := writer.Close()
	killErr := leaseProcess.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	waitErr := leaseProcess.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		waitErr = nil
	}
	return errors.Join(closeErr, killErr, waitErr)
}

// terminateProcessTree 终止 helper 的整个 Unix 进程组。
func terminateProcessTree(cmd *exec.Cmd, guard *processGuard) error {
	if err := validateProcessGuard(cmd, guard); err != nil {
		return err
	}
	return killLeasedProcessGroup(guard)
}

// validateProcessGuard 校验命令、稳定身份与进程组租约属于同一个启动实例。
func validateProcessGuard(cmd *exec.Cmd, guard *processGuard) error {
	if cmd == nil || cmd.Process == nil || guard == nil ||
		guard.leaseProcess == nil || guard.leaseProcess.Process == nil ||
		guard.leaseWriter == nil || guard.killGroup == nil {
		return errors.New("process is not started")
	}
	if cmd.Process.Pid != guard.identity.PID || guard.groupID != guard.identity.PID {
		return errors.New("schema worker process guard identity is invalid")
	}
	return nil
}

// killLeasedProcessGroup 通过仍存活的组成员固定 PGID 后发送 SIGKILL。
func killLeasedProcessGroup(guard *processGuard) error {
	if err := guard.killGroup(-guard.groupID, syscall.SIGKILL); err != nil {
		return err
	}
	guard.groupKilled = true
	return nil
}

func waitGuardedProcess(cmd *exec.Cmd, guard *processGuard) error {
	err := cmd.Wait()
	if guard != nil && guard.beforeWaitResultPublish != nil {
		guard.beforeWaitResultPublish()
	}
	return err
}

// closeProcessGuard 释放进程组租约并回收 guardian。
func closeProcessGuard(guard *processGuard) error {
	if guard == nil {
		return nil
	}
	if guard.leaseProcess == nil || guard.leaseWriter == nil {
		return errors.New("process-group lease is not attached")
	}
	leaseProcess := guard.leaseProcess
	leaseWriter := guard.leaseWriter
	guard.leaseProcess = nil
	guard.leaseWriter = nil
	closeErr := leaseWriter.Close()
	waitErr := leaseProcess.Wait()
	var exitErr *exec.ExitError
	if guard.groupKilled && errors.As(waitErr, &exitErr) {
		waitErr = nil
	}
	return errors.Join(closeErr, waitErr)
}
