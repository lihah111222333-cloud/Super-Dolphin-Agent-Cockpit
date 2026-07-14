//go:build darwin

package pidregistry

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// readProcessIdentity 从 Darwin 内核读取进程创建时间和可执行文件路径。
func readProcessIdentity(pid int) (processIdentity, error) {
	if pid <= 1 {
		return processIdentity{}, fmt.Errorf("pidregistry: invalid process identity PID %d", pid)
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: read process start time: %w", err)
	}
	start := info.Proc.P_starttime
	startToken := strconv.FormatInt(start.Sec, 10) + ":" + strconv.FormatInt(int64(start.Usec), 10)
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return processIdentity{}, fmt.Errorf("pidregistry: read process executable: %w", err)
	}
	if len(raw) <= 4 {
		return processIdentity{}, fmt.Errorf("pidregistry: process executable missing for PID %d", pid)
	}
	pathBytes := raw[4:]
	if end := bytes.IndexByte(pathBytes, 0); end >= 0 {
		pathBytes = pathBytes[:end]
	}
	executable := filepath.Clean(strings.TrimSpace(string(pathBytes)))
	if executable == "." || executable == "" {
		return processIdentity{}, fmt.Errorf("pidregistry: process executable missing for PID %d", pid)
	}
	return processIdentity{startToken: startToken, executable: executable}, nil
}
