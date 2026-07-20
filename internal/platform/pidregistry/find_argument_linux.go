//go:build linux

package pidregistry

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

const linuxProcRoot = "/proc"

func processIDs() ([]int, error) {
	return listLinuxProcessIDs(linuxProcRoot)
}

// listLinuxProcessIDs 严格枚举 procfs 中仍可确认存在的普通用户进程目录。
func listLinuxProcessIDs(procRoot string) ([]int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("pidregistry: list Linux processes: %w", err)
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pid, ok := parseLinuxProcessID(entry.Name())
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("pidregistry: inspect Linux process %d: %w", pid, err)
		}
		if !info.IsDir() {
			continue
		}
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

func parseLinuxProcessID(name string) (int, bool) {
	pid, err := strconv.Atoi(name)
	if err != nil || pid <= 1 || strconv.Itoa(pid) != name {
		return 0, false
	}
	return pid, true
}

func processArguments(pid int) ([]string, error) {
	return readLinuxProcessArguments(linuxProcRoot, pid)
}

// readLinuxProcessArguments 从 procfs 的 cmdline 原样读取并严格解析 NUL 分隔 argv。
func readLinuxProcessArguments(procRoot string, pid int) ([]string, error) {
	if pid <= 1 {
		return nil, errors.New("pidregistry: refusing to inspect PID <= 1")
	}
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrStableProcessNotFound
		}
		return nil, fmt.Errorf("pidregistry: read Linux process arguments: %w", err)
	}
	arguments, err := parseLinuxProcCmdline(raw)
	if err != nil {
		return nil, fmt.Errorf("pidregistry: parse Linux process arguments: %w", err)
	}
	return arguments, nil
}

func parseLinuxProcCmdline(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, errors.New("cmdline argv is not NUL terminated")
	}
	parts := bytes.Split(raw[:len(raw)-1], []byte{0})
	arguments := make([]string, len(parts))
	for index, part := range parts {
		arguments[index] = string(part)
	}
	return arguments, nil
}
