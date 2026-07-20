//go:build linux

package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func commandProcessGroupGone(processGroupID int) (bool, error) {
	err := syscall.Kill(-processGroupID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect executor process group %d after termination: %w", processGroupID, err)
	}
	return linuxProcessGroupQuiesced("/proc", processGroupID)
}

// linuxProcessGroupQuiesced 接受只剩 zombie/dead 成员的进程组，它们已不能继续访问执行区。
func linuxProcessGroupQuiesced(procRoot string, processGroupID int) (bool, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false, fmt.Errorf("read Linux process table: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		state, groupID, exists, err := readLinuxProcessStat(filepath.Join(procRoot, entry.Name(), "stat"))
		if err != nil {
			return false, err
		}
		if exists && groupID == processGroupID && state != 'Z' && state != 'X' {
			return false, nil
		}
	}
	return true, nil
}

func readLinuxProcessStat(path string) (byte, int, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("read Linux process stat %s: %w", path, err)
	}
	state, groupID, err := parseLinuxProcessStat(string(contents))
	if err != nil {
		return 0, 0, false, fmt.Errorf("parse Linux process stat %s: %w", path, err)
	}
	return state, groupID, true, nil
}

// parseLinuxProcessStat 从 comm 后最后一个右括号开始读取 state 与 pgrp，兼容命令名中的括号。
func parseLinuxProcessStat(value string) (byte, int, error) {
	commandEnd := strings.LastIndexByte(value, ')')
	if commandEnd < 0 {
		return 0, 0, errors.New("missing command terminator")
	}
	fields := strings.Fields(value[commandEnd+1:])
	if len(fields) < 3 || len(fields[0]) != 1 {
		return 0, 0, errors.New("missing state or process group")
	}
	groupID, err := strconv.Atoi(fields[2])
	if err != nil || groupID <= 0 {
		return 0, 0, errors.New("invalid process group")
	}
	return fields[0][0], groupID, nil
}
