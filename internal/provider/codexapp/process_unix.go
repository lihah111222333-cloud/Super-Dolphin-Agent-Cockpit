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

// processSig is the platform-neutral signal abstraction codexapp uses to
// manage its child processes (codex app-server, peer sidecars, MCP children).
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

// wrapWithFDLimit wraps argv in `sh -c "ulimit -n ...; exec argv..."`. On
// macOS GUI-launched processes inherit launchd's 256 fd soft limit, which is
// too low for batch agent scenarios; the shell wrapper guarantees the child
// starts with a high limit regardless of launch context.
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

// processGuard is the Unix no-op variant of the per-child supervisor handle.
// On Unix the Setpgid SysProcAttr already gives us a process group to target
// via negative-pid syscall.Kill — no extra kernel resource is needed.
type processGuard struct{}

// attachProcessGuard is called immediately after exec.Cmd.Start so the Windows
// variant can assign the fresh child to a Job Object. On Unix it is a no-op
// that returns a zero-value guard; callers may still invoke close() safely.
func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	_ = cmd
	return &processGuard{}
}

func (g *processGuard) close() {
	_ = g
}

// signalCodexProcess 处理signalcodex进程。
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
	// Try the process group first so the codex + its MCP children get the
	// same signal. Fall back to the individual process if the group lookup
	// fails (e.g. Setpgid did not take effect for some reason).
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

// killMCPProcess terminates an MCP sidecar process and, when possible, its
// entire process group. Returns nil when the process has already exited.
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

// discoverAllProcesses returns (allProcs, mcpProcs) where allProcs maps
// pid->ppid for every process we can see, and mcpProcs filters the managed
// MCP binary set. On Unix this runs `ps -eo pid,ppid,comm`.
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

// isAppServerArgs checks whether the process arguments match
// "codex app-server --listen ws://...".
// We look for the pattern in the args slice: [..., "app-server", "--listen", ws://...]
func isAppServerArgs(args []string) bool { return isCodexAppServerListenArgs(args) }

// discoverAppServerProcessList returns (allProcs, appServerProcs) where the
// latter is the filtered "codex app-server --listen ..." subset. On Unix this
// runs `ps -eo pid,ppid,args` (note the different ps format from the plain
// MCP discovery — we need the full argv to spot the --listen flag).
// discoverAppServerProcessList 处理discoverapp服务端进程list。
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
