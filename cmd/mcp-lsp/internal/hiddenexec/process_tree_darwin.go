//go:build darwin

package hiddenexec

import (
	"errors"
	"fmt"
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

// processStartIdentityPredatesCurrentBoot 使用 Darwin 的绝对进程启动时间和
// kern.boottime 建立重启边界；只有严格早于当前 boot 的身份才可退役。
func processStartIdentityPredatesCurrentBoot(startIdentity string) (bool, error) {
	seconds, microseconds, err := parseDarwinProcessStartIdentity(startIdentity)
	if err != nil {
		return false, err
	}
	bootSeconds, bootMicroseconds, err := currentDarwinBootTime()
	if err != nil {
		return false, err
	}
	return seconds < bootSeconds || (seconds == bootSeconds && microseconds < bootMicroseconds), nil
}

func parseDarwinProcessStartIdentity(startIdentity string) (int64, int64, error) {
	parts := strings.Split(startIdentity, ".")
	if len(parts) != 2 {
		return 0, 0, errors.New("Darwin process start identity is invalid")
	}
	seconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || seconds <= 0 {
		return 0, 0, errors.New("Darwin process start identity seconds are invalid")
	}
	microseconds, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || microseconds < 0 || microseconds >= 1_000_000 {
		return 0, 0, errors.New("Darwin process start identity microseconds are invalid")
	}
	return seconds, microseconds, nil
}

func currentDarwinBootTime() (int64, int64, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0, 0, fmt.Errorf("read Darwin boot time: %w", err)
	}
	if boot.Sec <= 0 || boot.Usec < 0 || boot.Usec >= 1_000_000 {
		return 0, 0, errors.New("Darwin boot time is invalid")
	}
	return boot.Sec, int64(boot.Usec), nil
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
	if len(members) == 0 {
		return nil
	}
	_ = signal
	// Darwin's os.Process/syscall APIs carry only a PID, not a stable process
	// handle. An identity probe cannot be atomically bound to a later signal,
	// so production member actions are deliberately zero-signal.
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("Darwin process-tree member signal requires a stable process handle; signal_sent=false"))
}

func defaultStartupKillGroup(int) error {
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("Darwin startup process-group signal requires a stable process handle; signal_sent=false"))
}

func defaultStartupKillProcess(*exec.Cmd) error {
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("Darwin startup exact-root signal requires a stable process handle; signal_sent=false"))
}

func terminateStartupProcess(cmd *exec.Cmd) error {
	return defaultStartupKillProcess(cmd)
}
