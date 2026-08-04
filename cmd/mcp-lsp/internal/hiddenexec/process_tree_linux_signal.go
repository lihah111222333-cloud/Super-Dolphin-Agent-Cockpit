//go:build linux

package hiddenexec

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

type linuxSignalOps struct {
	openPidfd       func(int, int) (int, error)
	sendPidfdSignal func(int, unix.Signal, *unix.Siginfo, int) error
}

func defaultLinuxSignalOps() linuxSignalOps {
	return linuxSignalOps{openPidfd: unix.PidfdOpen, sendPidfdSignal: unix.PidfdSendSignal}
}

func signalProcessMembers(members []ProcessIdentity, signal int) error {
	return signalProcessMembersWithOps(members, signal, defaultLinuxSignalOps())
}

func signalProcessMembersWithOps(members []ProcessIdentity, signal int, ops linuxSignalOps) error {
	fds, err := openVerifiedPidfds(members, ops)
	if err != nil {
		return err
	}
	defer closePidfds(fds)
	return sendVerifiedPidfdSignals(members, fds, signal, ops)
}

func openVerifiedPidfds(members []ProcessIdentity, ops linuxSignalOps) ([]int, error) {
	fds := make([]int, len(members))
	for i := range fds {
		fds[i] = -1
	}
	for i, member := range members {
		fd, err := ops.openPidfd(member.PID, 0)
		if err != nil {
			closePidfds(fds[:i])
			return nil, errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w for member PID %d: %v", ErrProcessTreePidfdUnavailable, member.PID, err))
		}
		if err := verifyPidfdMember(fd, member); err != nil {
			_ = unix.Close(fd)
			closePidfds(fds[:i])
			return nil, err
		}
		fds[i] = fd
	}
	return fds, nil
}

func verifyPidfdMember(_ int, member ProcessIdentity) error {
	current, err := captureProcessIdentity(member.PID)
	if err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w for member PID %d: %v", ErrProcessTreeIdentityMismatch, member.PID, err))
	}
	if !current.Equal(member) {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w for member PID %d", ErrProcessTreeIdentityMismatch, member.PID))
	}
	return nil
}

func closePidfds(fds []int) {
	for _, fd := range fds {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
}

func sendVerifiedPidfdSignals(members []ProcessIdentity, fds []int, signal int, ops linuxSignalOps) error {
	for i, member := range members {
		if err := verifyCurrentMember(member); err != nil {
			return err
		}
		if err := ops.sendPidfdSignal(fds[i], unix.Signal(signal), nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("send signal %d to member PID %d: %w", signal, member.PID, err)
		}
	}
	return nil
}

func verifyCurrentMember(member ProcessIdentity) error {
	current, err := captureProcessIdentity(member.PID)
	if err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w before signal for member PID %d: %v", ErrProcessTreeIdentityMismatch, member.PID, err))
	}
	if !current.Equal(member) {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w before signal for member PID %d", ErrProcessTreeIdentityMismatch, member.PID))
	}
	return nil
}

func defaultStartupKillGroup(pid int) error {
	return syscall.Kill(-pid, syscall.Signal(unixForceSignal))
}

func defaultStartupKillProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("startup process owner is unavailable")
	}
	return cmd.Process.Kill()
}

func terminateStartupProcess(cmd *exec.Cmd) error {
	return defaultStartupKillProcess(cmd)
}
