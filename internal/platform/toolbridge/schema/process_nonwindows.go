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
	ownershipEstablished    bool
	attachmentEstablished   bool
	groupKilled             bool
	beforeWaitResultPublish func()
}

// prepareProcessGuard 在 worker 启动前建立由 guardian 固定的独占进程组。
func prepareProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	return prepareProcessGuardWithLease(cmd, acquireProcessGroupLease)
}

// prepareProcessGuardWithLease 将 lease 建立保留为启动前可注入阶段，确保失败时 worker 尚未 Start。
func prepareProcessGuardWithLease(
	cmd *exec.Cmd,
	acquire func() (*exec.Cmd, *os.File, int, error),
) (*processGuard, error) {
	if cmd == nil {
		return nil, errors.New("process command is nil")
	}
	if acquire == nil {
		return nil, errors.New("process-group lease acquirer is nil")
	}
	leaseProcess, leaseWriter, groupID, err := acquire()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: groupID}
	return &processGuard{
		groupID:              groupID,
		leaseProcess:         leaseProcess,
		leaseWriter:          leaseWriter,
		killGroup:            syscall.Kill,
		ownershipEstablished: true,
	}, nil
}

// attachProcessGuard 确认已启动 worker 属于启动前持有的进程组边界。
func attachProcessGuard(cmd *exec.Cmd, guard *processGuard) error {
	return attachProcessGuardWithProbe(cmd, guard, nil)
}

// attachProcessGuardWithProbe 逐阶段校验 worker identity 与预持有进程组，供故障测试注入真实内部失败。
func attachProcessGuardWithProbe(cmd *exec.Cmd, guard *processGuard, probe processGuardAttachProbe) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	if err := validateOwnedProcessGroup(guard); err != nil {
		return err
	}
	if err := runProcessGuardAttachProbe(probe, processGuardAttachCaptureIdentity); err != nil {
		return err
	}
	identity, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return err
	}
	if err := runProcessGuardAttachProbe(probe, processGuardAttachValidateProcessGroup); err != nil {
		return err
	}
	groupID, err := processGroupID(cmd.Process.Pid)
	if err != nil {
		return err
	}
	if groupID != guard.groupID {
		return errors.New("schema worker joined an unexpected process group")
	}
	guard.identity = identity
	if err := runProcessGuardAttachProbe(probe, processGuardAttachValidateOwnership); err != nil {
		return err
	}
	return establishProcessGuardAttachment(cmd, guard)
}

func establishProcessGuardAttachment(cmd *exec.Cmd, guard *processGuard) error {
	if err := validateProcessGuardOwnership(cmd, guard); err != nil {
		return err
	}
	guard.attachmentEstablished = true
	return nil
}

func runProcessGuardAttachProbe(probe processGuardAttachProbe, stage processGuardAttachStage) error {
	if probe == nil {
		return nil
	}
	return probe(stage)
}

func processGroupID(pid int) (int, error) {
	groupID, err := syscall.Getpgid(pid)
	if err != nil {
		return 0, err
	}
	return groupID, nil
}

// validateProcessGuardOwnership 二次确认 PID generation、组成员关系和 guardian lease。
func validateProcessGuardOwnership(cmd *exec.Cmd, guard *processGuard) error {
	if cmd == nil || cmd.Process == nil || guard == nil {
		return errors.New("process is not started")
	}
	currentIdentity, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return err
	}
	if currentIdentity != guard.identity {
		return errors.New("schema worker process identity changed while acquiring process-group lease")
	}
	currentGroupID, err := processGroupID(cmd.Process.Pid)
	if err != nil {
		return err
	}
	if currentGroupID != guard.groupID {
		return errors.New("schema worker process group changed while acquiring process-group lease")
	}
	return nil
}

// acquireProcessGroupLease 创建并验证一个由父进程持有的独占进程组 leader。
func acquireProcessGroupLease() (*exec.Cmd, *os.File, int, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, 0, err
	}
	leaseProcess := exec.Command("/bin/sh", "-c", "read _ || :")
	leaseProcess.Stdin = reader
	leaseProcess.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leaseProcess.Start(); err != nil {
		return nil, nil, 0, errors.Join(err, reader.Close(), writer.Close())
	}
	if err := reader.Close(); err != nil {
		return nil, nil, 0, errors.Join(err, abortProcessGroupLease(leaseProcess, writer))
	}
	actualGroupID, err := syscall.Getpgid(leaseProcess.Process.Pid)
	if err != nil {
		return nil, nil, 0, errors.Join(err, abortProcessGroupLease(leaseProcess, writer))
	}
	if actualGroupID != leaseProcess.Process.Pid {
		err := errors.New("process-group guardian is not its group leader")
		return nil, nil, 0, errors.Join(err, abortProcessGroupLease(leaseProcess, writer))
	}
	return leaseProcess, writer, actualGroupID, nil
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

// terminateUnattachedProcessTree 仅终止已验证属于该启动实例的 leased process group。
func terminateUnattachedProcessTree(cmd *exec.Cmd, guard *processGuard) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	if err := validateOwnedProcessGroup(guard); err != nil {
		return err
	}
	return killLeasedProcessGroup(guard)
}

// validateProcessGuard 校验命令、稳定身份与进程组租约属于同一个启动实例。
func validateProcessGuard(cmd *exec.Cmd, guard *processGuard) error {
	if err := validateProcessGuardPrerequisites(cmd, guard); err != nil {
		return err
	}
	if !guard.ownershipEstablished {
		return errors.New("schema worker process guard ownership is not established")
	}
	if !guard.attachmentEstablished {
		return errors.New("schema worker process guard attachment is not established")
	}
	if cmd.Process.Pid != guard.identity.PID {
		return errors.New("schema worker process guard identity is invalid")
	}
	return validateOwnedProcessGroup(guard)
}

// validateProcessGuardPrerequisites 拒绝缺失 command、lease 或 kill capability 的半成品 guard。
func validateProcessGuardPrerequisites(cmd *exec.Cmd, guard *processGuard) error {
	if cmd == nil || cmd.Process == nil || guard == nil {
		return errors.New("process is not started")
	}
	if guard.leaseProcess == nil || guard.leaseProcess.Process == nil {
		return errors.New("process-group lease is not attached")
	}
	if guard.leaseWriter == nil || guard.killGroup == nil {
		return errors.New("process-group lease is not attached")
	}
	return nil
}

func validateProcessGroupLease(guard *processGuard) error {
	leaseGroupID, err := syscall.Getpgid(guard.leaseProcess.Process.Pid)
	if err != nil {
		return err
	}
	if leaseGroupID != guard.groupID {
		return errors.New("schema worker process group lease is invalid")
	}
	return nil
}

// validateOwnedProcessGroup 只依赖 guardian lease 证明进程组 ownership，不依赖可能已退出的 worker PID。
func validateOwnedProcessGroup(guard *processGuard) error {
	if guard == nil || !guard.ownershipEstablished {
		return errors.New("schema worker process guard ownership is not established")
	}
	if guard.leaseProcess == nil || guard.leaseProcess.Process == nil || guard.leaseWriter == nil || guard.killGroup == nil {
		return errors.New("process-group lease is not attached")
	}
	if guard.groupID != guard.leaseProcess.Process.Pid {
		return errors.New("process-group lease identity is invalid")
	}
	return validateProcessGroupLease(guard)
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
