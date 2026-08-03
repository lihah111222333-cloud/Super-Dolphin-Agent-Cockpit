//go:build darwin

package hiddenexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	unixGracefulSignal = int(syscall.SIGTERM)
	unixForceSignal    = int(syscall.SIGKILL)
)

func captureProcessIdentity(pid int) (ProcessIdentity, error) {
	if pid <= 1 {
		return ProcessIdentity{}, errors.New("process PID must be greater than 1")
	}
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	if int(proc.Proc.P_pid) != pid {
		return ProcessIdentity{}, fmt.Errorf("process identity PID mismatch: got %d want %d", proc.Proc.P_pid, pid)
	}
	start := proc.Proc.P_starttime
	sessionID, err := unix.Getsid(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read Darwin session for pid %d: %w", pid, err)
	}
	processGroupID, err := unix.Getpgid(pid)
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read Darwin process group for pid %d: %w", pid, err)
	}
	return ProcessIdentity{
		PID:            pid,
		StartToken:     strconv.FormatInt(start.Sec, 10) + "." + strconv.FormatInt(int64(start.Usec), 10),
		UID:            proc.Eproc.Ucred.Uid,
		SessionID:      sessionID,
		ProcessGroupID: processGroupID,
	}, nil
}

func processTable() (map[int]ProcessIdentity, error) {
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	table := make(map[int]ProcessIdentity, len(entries))
	for _, proc := range entries {
		pid := int(proc.Proc.P_pid)
		if pid <= 1 {
			continue
		}
		start := proc.Proc.P_starttime
		sessionID, sessionErr := unix.Getsid(pid)
		if sessionErr != nil {
			if errors.Is(sessionErr, unix.ESRCH) || errors.Is(sessionErr, unix.EIO) {
				continue
			}
			return nil, fmt.Errorf("capture Darwin session for process %d while building process table: %w", pid, sessionErr)
		}
		processGroupID, groupErr := unix.Getpgid(pid)
		if groupErr != nil {
			if errors.Is(groupErr, unix.ESRCH) || errors.Is(groupErr, unix.EIO) {
				continue
			}
			return nil, fmt.Errorf("capture Darwin process group for process %d while building process table: %w", pid, groupErr)
		}
		table[pid] = ProcessIdentity{
			PID:            pid,
			StartToken:     strconv.FormatInt(start.Sec, 10) + "." + strconv.FormatInt(int64(start.Usec), 10),
			UID:            proc.Eproc.Ucred.Uid,
			SessionID:      sessionID,
			ProcessGroupID: processGroupID,
		}
	}
	return table, nil
}

func processParent(pid int) (int, bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, false
	}
	return int(proc.Eproc.Ppid), true
}

func processRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, errors.New("Darwin process RSS is unavailable")
	}
	kilobytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse Darwin process RSS for pid %d: %w", pid, err)
	}
	return kilobytes * 1024, nil
}

func signalProcessMembers(members []ProcessIdentity, signal int) error {
	for _, member := range members {
		current, err := captureProcessIdentity(member.PID)
		if err != nil {
			return fmt.Errorf("%w before signal for member PID %d: %v", ErrProcessTreeIdentityMismatch, member.PID, err)
		}
		if !current.Equal(member) {
			return fmt.Errorf("%w before signal for member PID %d", ErrProcessTreeIdentityMismatch, member.PID)
		}
		if err := syscall.Kill(member.PID, syscall.Signal(signal)); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("send signal %d to member PID %d: %w", signal, member.PID, err)
		}
	}
	return nil
}
