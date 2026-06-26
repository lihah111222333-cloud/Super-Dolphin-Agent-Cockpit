//go:build !windows

package codexapp

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// processSig 抽象 Codex app-server、peer sidecar 和 MCP 子进程的停止信号。
// Unix 与 Windows 的具体信号不同，上层只通过这三个意图表达生命周期动作。
type processSig int

const (
	sigInterrupt processSig = iota
	sigTerminate
	sigForceKill
)

func toUnixSignal(sig processSig) syscall.Signal {
	switch sig {
	case sigInterrupt:
		return syscall.SIGINT
	case sigTerminate:
		return syscall.SIGTERM
	case sigForceKill:
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func setCodexProcessAttrs(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// wrapWithFDLimit 用 shell 包一层 ulimit 提升后再 exec 原命令。
// macOS GUI 启动时常继承 launchd 的低 fd 上限，批量 agent 场景必须在子进程入口处主动抬高。
func wrapWithFDLimit(argv []string) *exec.Cmd {
	shellCmd := fmt.Sprintf(
		"ulimit -n 1048576 2>/dev/null || ulimit -n 65535 2>/dev/null || true; exec %s %s",
		shellQuoteArg(argv[0]), shellQuoteArgs(argv[1:]),
	)
	return exec.Command("/bin/sh", "-c", shellCmd)
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n'\"\\$`;&|<>*?()[]{}!") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

// processGuard 是 Unix 平台的空监督句柄。
// 子进程已通过 Setpgid 拥有独立进程组，后续用负 PID 发信号即可覆盖整棵子树。
type processGuard struct{}

// attachProcessGuard 在子进程启动后返回平台监督句柄。
// Unix 不需要额外内核对象，但仍返回非 nil 句柄以保持跨平台关闭路径一致。
func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	_ = cmd
	return &processGuard{}
}

func (g *processGuard) close() {
	_ = g
}

// signalCodexProcess 向 Codex 子进程组发送指定停止信号。
// 进程已退出时按幂等成功处理，避免关闭路径把正常竞态误报为失败。
func signalCodexProcess(cmd *exec.Cmd, guard *processGuard, sig processSig) error {
	_ = guard
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid codex pid")
	}
	sysSig := toUnixSignal(sig)
	// 优先打到进程组，确保 codex 和它拉起的 MCP 子进程收到同一信号。
	// 如果进程组不可达，再退到单进程信号，避免关闭路径完全失效。
	if err := syscall.Kill(-pid, sysSig); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	err := cmd.Process.Signal(sysSig)
	if isProcessGoneErr(err) {
		return nil
	}
	return err
}

func sendSignalToPID(pid int, sig processSig) error { return syscall.Kill(pid, toUnixSignal(sig)) }

func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func isProcessGoneErr(err error) bool { return errors.Is(err, syscall.ESRCH) }

// killMCPProcess 强制终止 MCP sidecar 及其进程组。
// PID 小于等于 1 会直接拒绝；目标已退出时按幂等成功处理。
func killMCPProcess(pid int) error {
	if pid <= 1 {
		return errors.New("refusing to kill PID <= 1")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// discoverAllProcesses 枚举当前可见进程并筛出受管理的 MCP sidecar。
// 返回的 pid->ppid 图供孤儿清理追踪子树，ps 失败时只记录告警并让清理方跳过本轮。
func discoverAllProcesses() (map[int]int, []mcpProcessInfo) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,comm").Output()
	if err != nil {
		pkglogger.Warn("orphan cleanup: ps command failed", "error", err)
		return nil, nil
	}

	allProcs := make(map[int]int, 256)
	var mcpProcs []mcpProcessInfo
	for _, line := range strings.Split(string(out), "\n") {
		pid, ppid, binary, ok := parseProcessLine(line)
		if !ok {
			continue
		}
		allProcs[pid] = ppid
		if binary != "" {
			mcpProcs = append(mcpProcs, mcpProcessInfo{pid: pid, ppid: ppid, binary: binary})
		}
	}
	return allProcs, mcpProcs
}

// isAppServerArgs 判断进程参数是否像 Codex app-server 监听进程。
// 匹配逻辑只看 argv 片段，供孤儿清理在 ps 输出里识别可回收目标。
func isAppServerArgs(args []string) bool { return isCodexAppServerListenArgs(args) }

// discoverAppServerProcessList 枚举进程图并筛出 Codex app-server 监听进程。
// 这里必须读取完整 argv 才能确认 `app-server --listen`，因此使用带 args 的 ps 格式。
func discoverAppServerProcessList() (map[int]int, []appServerProcessInfo) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,args").Output()
	if err != nil {
		pkglogger.Warn("orphan sweeper: ps command failed", "error", err)
		return nil, nil
	}

	allProcs := make(map[int]int, 256)
	var appServers []appServerProcessInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		allProcs[pid] = ppid
		if len(fields) >= 5 && isAppServerArgs(fields[2:]) {
			appServers = append(appServers, appServerProcessInfo{pid: pid, ppid: ppid})
		}
	}
	return allProcs, appServers
}
