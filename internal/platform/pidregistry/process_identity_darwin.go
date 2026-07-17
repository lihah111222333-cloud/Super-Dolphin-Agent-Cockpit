//go:build darwin

package pidregistry

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// readProcessIdentity 从 Darwin 内核读取进程创建时间和可执行文件路径。
func readProcessIdentity(pid int) (processIdentity, error) {
	return readStableProcessIdentity(pid, readDarwinProcessStartToken, readDarwinProcessExecutable)
}

func readDarwinProcessStartToken(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", classifyDarwinIdentityRead("start time", err)
	}
	start := info.Proc.P_starttime
	return strconv.FormatInt(start.Sec, 10) + ":" + strconv.FormatInt(int64(start.Usec), 10), nil
}

// readDarwinProcessExecutable 从 procargs2 的 executable 区读取 canonical path。
func readDarwinProcessExecutable(pid int) (string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", classifyDarwinIdentityRead("executable", err)
	}
	if len(raw) <= 4 {
		return "", fmt.Errorf("pidregistry: process executable missing for PID %d", pid)
	}
	pathBytes := raw[4:]
	end := bytes.IndexByte(pathBytes, 0)
	if end <= 0 {
		return "", fmt.Errorf("pidregistry: process executable is malformed for PID %d", pid)
	}
	pathBytes = pathBytes[:end]
	executable := filepath.Clean(strings.TrimSpace(string(pathBytes)))
	if executable == "." || executable == "" {
		return "", fmt.Errorf("pidregistry: process executable missing for PID %d", pid)
	}
	return executable, nil
}

// classifyDarwinIdentityRead 将 procargs2 的 transient disappearance 与结构错误分离。
func classifyDarwinIdentityRead(field string, err error) error {
	wrapped := fmt.Errorf("pidregistry: read process %s: %w", field, err)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) {
		return errors.Join(ErrStableProcessNotFound, wrapped)
	}
	return wrapped
}
