//go:build linux

package hiddenexec

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	unixGracefulSignal = int(syscall.SIGTERM)
	unixForceSignal    = int(syscall.SIGKILL)
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

// readLinuxStat 读取 Linux /proc stat，并将已终止进程排除出活动 owner。
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
	if isLinuxProcessTerminal(fields[0]) {
		return linuxStat{}, syscall.ESRCH
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

// isLinuxProcessTerminal 报告 /proc stat 中不可再视为活动进程的终止状态。
func isLinuxProcessTerminal(state string) bool {
	return state == "Z" || state == "X"
}

// linuxProcessUID 读取 Linux 进程的有效 UID，供身份快照绑定权限边界。
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

// processTable 枚举当前可读取身份的 Linux 进程，忽略并发退出的条目。
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
