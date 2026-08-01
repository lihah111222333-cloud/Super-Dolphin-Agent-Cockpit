//go:build darwin || linux

package hiddenexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type unixProcessTree struct {
	cmd *exec.Cmd
}

func startProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	if cmd == nil {
		return nil, errors.New("start process tree: command is nil")
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &ProcessTree{controller: &unixProcessTree{cmd: cmd}}, nil
}

func (p *unixProcessTree) terminate() error {
	return killProcessTree(p.cmd)
}

func (p *unixProcessTree) release() error {
	return nil
}

func (p *unixProcessTree) rssBytes() (uint64, error) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0, errors.New("Unix process-tree owner is not started")
	}
	return processTreeRSSBytes(p.cmd.Process.Pid)
}

func processAlive(pid int) (bool, error) {
	if pid <= 1 {
		return false, nil
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func processStartIdentity(pid int) (string, error) {
	if pid <= 1 {
		return "", errors.New("process PID must be greater than 1")
	}
	if runtime.GOOS == "linux" {
		return linuxProcessStartIdentity(pid)
	}
	output, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	identity := strings.Join(strings.Fields(string(output)), " ")
	if identity == "" {
		return "", fmt.Errorf("process %d has empty start identity", pid)
	}
	return identity, nil
}

// linuxProcessStartIdentity 用 boot ID 与 /proc 启动时钟组合出可抵抗 PID 复用的身份。
func linuxProcessStartIdentity(pid int) (string, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	closeParen := strings.LastIndexByte(string(payload), ')')
	if closeParen < 0 || closeParen+1 >= len(payload) {
		return "", fmt.Errorf("unexpected stat payload for pid %d", pid)
	}
	fields := strings.Fields(string(payload[closeParen+1:]))
	const startTimeIndexAfterCommand = 19
	if len(fields) <= startTimeIndexAfterCommand {
		return "", fmt.Errorf("unexpected stat fields for pid %d", pid)
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", fmt.Errorf("read Linux boot identity: %w", err)
	}
	return strings.TrimSpace(string(bootID)) + "/" + fields[startTimeIndexAfterCommand], nil
}

func processTreeRSSBytes(pid int) (uint64, error) {
	if runtime.GOOS == "linux" {
		return linuxProcessGroupRSSBytes(pid)
	}
	return psProcessGroupRSSBytes(pid)
}

func processRSSBytes(pid int) (uint64, error) {
	if runtime.GOOS == "linux" {
		return linuxRSSBytes(pid)
	}
	return psRSSBytes(pid)
}

// linuxProcessGroupRSSBytes 汇总独立语言服务器进程组的 RSS。
// 老版本启动的进程若尚未拥有独立组，则只读取父 PID，避免误计整个 mcp-lsp 进程组。
func linuxProcessGroupRSSBytes(pid int) (uint64, error) {
	groupID, err := linuxProcessGroupID(pid)
	if err != nil {
		return 0, err
	}
	if groupID != pid {
		return linuxRSSBytes(pid)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, entry := range entries {
		memberPID, parseErr := strconv.Atoi(entry.Name())
		if parseErr != nil {
			continue
		}
		memberGroupID, groupErr := linuxProcessGroupID(memberPID)
		if groupErr != nil || memberGroupID != groupID {
			continue
		}
		rss, rssErr := linuxRSSBytes(memberPID)
		if rssErr == nil {
			total += rss
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("language-server process group %d has no readable RSS", groupID)
	}
	return total, nil
}

// linuxProcessGroupID 从 procfs stat 中读取目标 PID 所属的进程组。
func linuxProcessGroupID(pid int) (int, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	closeParen := strings.LastIndexByte(string(payload), ')')
	if closeParen < 0 || closeParen+1 >= len(payload) {
		return 0, fmt.Errorf("unexpected stat payload for pid %d", pid)
	}
	fields := strings.Fields(string(payload[closeParen+1:]))
	if len(fields) < 3 {
		return 0, fmt.Errorf("unexpected stat fields for pid %d", pid)
	}
	groupID, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, fmt.Errorf("parse process group for pid %d: %w", pid, err)
	}
	return groupID, nil
}

func linuxRSSBytes(pid int) (uint64, error) {
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
		return 0, err
	}
	return pages * uint64(os.Getpagesize()), nil
}

// psProcessGroupRSSBytes 汇总 macOS 独立语言服务器进程组内所有成员的 RSS。
func psProcessGroupRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command("ps", "-o", "pgid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	groupID, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, fmt.Errorf("parse process group for pid %d: %w", pid, err)
	}
	if groupID != pid {
		return psRSSBytes(pid)
	}
	output, err = exec.Command("ps", "-axo", "pgid=,rss=").Output()
	if err != nil {
		return 0, err
	}
	var total uint64
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != strconv.Itoa(groupID) {
			continue
		}
		kilobytes, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("parse RSS for process group %d: %w", groupID, parseErr)
		}
		total += kilobytes * 1024
	}
	if total == 0 {
		return 0, fmt.Errorf("language-server process group %d has no readable RSS", groupID)
	}
	return total, nil
}

func psRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, nil
	}
	kilobytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return kilobytes * 1024, nil
}

// killProcessTree 终止独立 Unix 进程组；组信号失败时再回收已启动的父进程。
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 1 {
		return errors.New("refusing to kill language-server PID <= 1")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
