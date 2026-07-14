//go:build linux

package pidregistry

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readProcessIdentity 从 procfs 读取进程启动时钟和可执行文件路径。
func readProcessIdentity(pid int) (processIdentity, error) {
	if pid <= 1 {
		return processIdentity{}, fmt.Errorf("pidregistry: invalid process identity PID %d", pid)
	}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: read process stat: %w", err)
	}
	closing := strings.LastIndexByte(string(stat), ')')
	if closing < 0 {
		return processIdentity{}, fmt.Errorf("pidregistry: malformed process stat for PID %d", pid)
	}
	fields := strings.Fields(string(stat)[closing+1:])
	if len(fields) <= 19 || strings.TrimSpace(fields[19]) == "" {
		return processIdentity{}, fmt.Errorf("pidregistry: process start token missing for PID %d", pid)
	}
	executable, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: read process executable: %w", err)
	}
	return processIdentity{startToken: fields[19], executable: filepath.Clean(executable)}, nil
}
