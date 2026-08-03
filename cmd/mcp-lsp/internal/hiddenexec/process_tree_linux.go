//go:build linux

package hiddenexec

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	unixGracefulSignal = int(syscall.SIGTERM)
	unixForceSignal    = int(syscall.SIGKILL)
)

// These indirections keep the Linux security edge testable without replacing
// the production pidfd syscalls or introducing a signal-capable fallback.
var (
	openPidfd       = unix.PidfdOpen
	sendPidfdSignal = unix.PidfdSendSignal
)

func captureProcessIdentity(pid int) (ProcessIdentity, error) {
	if pid <= 1 {
		return ProcessIdentity{}, errors.New("process PID must be greater than 1")
	}
	stat, err := readLinuxStat(pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	uid, err := linuxProcessUID(pid)
	if err != nil {
		return ProcessIdentity{}, err
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read Linux boot identity: %w", err)
	}
	return ProcessIdentity{
		PID:            pid,
		StartToken:     strings.TrimSpace(string(bootID)) + "/" + stat.startTime,
		UID:            uid,
		SessionID:      stat.sessionID,
		ProcessGroupID: stat.processGroupID,
	}, nil
}

type linuxStat struct {
	parentPID      int
	processGroupID int
	sessionID      int
	startTime      string
}

func readLinuxStat(pid int) (linuxStat, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return linuxStat{}, err
	}
	closeParen := strings.LastIndexByte(string(payload), ')')
	if closeParen < 0 || closeParen+1 >= len(payload) {
		return linuxStat{}, fmt.Errorf("unexpected stat payload for pid %d", pid)
	}
	fields := strings.Fields(string(payload)[closeParen+1:])
	// fields: state=0, ppid=1, pgrp=2, session=3, ..., starttime=19.
	if len(fields) <= 19 {
		return linuxStat{}, fmt.Errorf("unexpected stat fields for pid %d", pid)
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return linuxStat{}, fmt.Errorf("parse parent PID for %d: %w", pid, err)
	}
	processGroupID, err := strconv.Atoi(fields[2])
	if err != nil {
		return linuxStat{}, fmt.Errorf("parse process group for %d: %w", pid, err)
	}
	sessionID, err := strconv.Atoi(fields[3])
	if err != nil {
		return linuxStat{}, fmt.Errorf("parse session for %d: %w", pid, err)
	}
	if fields[19] == "" {
		return linuxStat{}, fmt.Errorf("empty start time for pid %d", pid)
	}
	return linuxStat{parentPID: parentPID, processGroupID: processGroupID, sessionID: sessionID, startTime: fields[19]}, nil
}

func linuxProcessUID(pid int) (uint32, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	for line := range strings.SplitSeq(string(payload), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		uid, parseErr := strconv.ParseUint(fields[1], 10, 32)
		if parseErr != nil {
			return 0, fmt.Errorf("parse UID for pid %d: %w", pid, parseErr)
		}
		return uint32(uid), nil
	}
	return 0, fmt.Errorf("UID is missing for pid %d", pid)
}

func processTable() (map[int]ProcessIdentity, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	table := make(map[int]ProcessIdentity)
	for _, entry := range entries {
		pid, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil || pid <= 1 {
			continue
		}
		identity, identityErr := captureProcessIdentity(pid)
		if identityErr == nil {
			table[pid] = identity
			continue
		}
		if isProcessGone(identityErr) || errors.Is(identityErr, syscall.EIO) {
			continue
		}
		return nil, fmt.Errorf("capture Linux process %d while building process table: %w", pid, identityErr)
	}
	return table, nil
}

func processParent(pid int) (int, bool) {
	stat, err := readLinuxStat(pid)
	if err != nil {
		return 0, false
	}
	return stat.parentPID, true
}

func processRSSBytes(pid int) (uint64, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(payload))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected statm payload for pid %d", pid)
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse RSS pages for pid %d: %w", pid, err)
	}
	return pages * uint64(os.Getpagesize()), nil
}

func signalProcessMembers(members []ProcessIdentity, signal int) error {
	// Open every pidfd before sending any signal. This makes pidfd unavailable,
	// identity mismatch, and permission failure a strict zero-signal outcome.
	fds := make([]int, len(members))
	for i, member := range members {
		fd, err := openPidfd(member.PID, 0)
		if err != nil {
			for _, opened := range fds[:i] {
				_ = unix.Close(opened)
			}
			return fmt.Errorf("%w for member PID %d: %v", ErrProcessTreePidfdUnavailable, member.PID, err)
		}
		current, identityErr := captureProcessIdentity(member.PID)
		if identityErr != nil || !current.Equal(member) {
			_ = unix.Close(fd)
			for _, opened := range fds[:i] {
				_ = unix.Close(opened)
			}
			if identityErr != nil {
				return fmt.Errorf("%w for member PID %d: %v", ErrProcessTreeIdentityMismatch, member.PID, identityErr)
			}
			return fmt.Errorf("%w for member PID %d", ErrProcessTreeIdentityMismatch, member.PID)
		}
		fds[i] = fd
	}
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	for i, member := range members {
		if current, err := captureProcessIdentity(member.PID); err != nil || !current.Equal(member) {
			if err != nil {
				return fmt.Errorf("%w before signal for member PID %d: %v", ErrProcessTreeIdentityMismatch, member.PID, err)
			}
			return fmt.Errorf("%w before signal for member PID %d", ErrProcessTreeIdentityMismatch, member.PID)
		}
		if err := sendPidfdSignal(fds[i], unix.Signal(signal), nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("send signal %d to member PID %d: %w", signal, member.PID, err)
		}
	}
	return nil
}
