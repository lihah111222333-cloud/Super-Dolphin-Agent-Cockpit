//go:build windows

package codexapp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// processSig 抽象 Windows 上的子进程控制意图。
// 当前没有共享控制台时三种意图都落到终止进程；未来若 app-server 支持窗口或控制台再细分优雅停止。
type processSig int

const (
	sigInterrupt processSig = iota
	sigTerminate
	sigForceKill
)

func setCodexProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000200, HideWindow: true}
}

// wrapWithFDLimit 在 Windows 上不调整 fd 上限，只解析真正的可执行文件再启动。
// 先剥离 npm shim 可以绕过 cmd.exe 参数重解析和命令行长度限制。
func wrapWithFDLimit(argv []string) *exec.Cmd {
	resolved := resolveCodexBinary(argv[0])
	return exec.Command(resolved, argv[1:]...)
}

// resolveCodexBinary 将 npm 生成的 .cmd shim 解析为真实 .exe。
// Go 在 Windows 上会经 cmd.exe 执行 .cmd/.bat，长参数和换行参数容易被截断或重写。
func resolveCodexBinary(binary string) string {
	if binary == "" {
		return binary
	}
	if strings.ContainsAny(binary, `\/`) {
		return binary
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return binary
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext != ".cmd" && ext != ".bat" {
		return resolved
	}
	real, ok := unwrapCodexNpmShim(resolved)
	if !ok {
		return resolved
	}
	pkglogger.Info("codexapp: unwrapped npm shim",
		"shim", resolved, "exe", real)
	return real
}

var codexNpmShimExeRE = regexp.MustCompile(`(?i)"%dp0%[\\/]([^"]+\.exe)"`)

func unwrapCodexNpmShim(cmdPath string) (string, bool) {
	data, err := os.ReadFile(cmdPath)
	if err != nil {
		return "", false
	}
	matches := codexNpmShimExeRE.FindSubmatch(data)
	if len(matches) < 2 {
		return "", false
	}
	rel := strings.TrimSpace(string(matches[1]))
	if rel == "" {
		return "", false
	}
	base := filepath.Dir(cmdPath)
	abs := filepath.Clean(filepath.Join(base, rel))
	if _, err := os.Stat(abs); err != nil {
		return "", false
	}
	return abs, true
}

// processGuard 持有 Windows Job Object，用来监督 Codex 子进程树。
// Job Object 会把后代进程纳入同一清理边界，父进程崩溃时也能靠 kill-on-close 回收。
type processGuard struct {
	handle windows.Handle
}

// attachProcessGuard 为刚启动的 Codex 子进程创建并绑定 Job Object。
// 绑定失败只记录告警，后续仍可退化为单进程 terminate，避免启动路径被监督能力阻断。
func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	handle, err := createKillOnCloseJob()
	if err != nil {
		pkglogger.Warn("codexapp: create job object failed", "error", err)
		return nil
	}
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		pkglogger.Warn("codexapp: open process handle failed",
			"pid", cmd.Process.Pid, "error", err)
		windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(procHandle)
	if err := windows.AssignProcessToJobObject(handle, procHandle); err != nil {
		pkglogger.Warn("codexapp: assign process to job failed",
			"pid", cmd.Process.Pid, "error", err)
		windows.CloseHandle(handle)
		return nil
	}
	return &processGuard{handle: handle}
}

func (g *processGuard) close() {
	if g == nil || g.handle == 0 {
		return
	}
	// 关闭 Job handle 会触发 JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE，
	// 这是父进程没有机会显式清理时的最后兜底。
	windows.CloseHandle(g.handle)
	g.handle = 0
}

// signalCodexProcess 终止 Windows 上的 Codex 子进程树。
// Job Object 可用时优先终止整棵树；否则退回单进程 terminate。
func signalCodexProcess(cmd *exec.Cmd, guard *processGuard, sig processSig) error {
	_ = sig
	if guard != nil && guard.handle != 0 {
		if err := windows.TerminateJobObject(guard.handle, 1); err == nil {
			return nil
		}
		// Job 已关闭或失效时继续走单进程终止，避免关闭路径卡在监督句柄上。
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return terminatePID(cmd.Process.Pid)
}

func createKillOnCloseJob() (windows.Handle, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: 0x2000, // windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		},
	}
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

func sendSignalToPID(pid int, sig processSig) error {
	_ = sig
	return terminatePID(pid)
}

func isProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

func isProcessGoneErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrProcessDone) {
		return true
	}
	return errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}

func killMCPProcess(pid int) error {
	if pid <= 1 {
		return errors.New("refusing to kill PID <= 1")
	}
	return terminatePID(pid)
}

// terminatePID 打开目标进程并发送 TerminateProcess。
// 目标在打开或终止期间消失时返回带 PID 的错误，方便诊断进程生命周期竞态。
func terminatePID(pid int) error {
	if pid <= 0 {
		return errors.New("invalid codex pid")
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		if isProcessGoneErr(err) {
			return fmt.Errorf("codex process %d not found before terminate: %w", pid, err)
		}
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		if isProcessGoneErr(err) {
			return fmt.Errorf("codex process %d vanished during terminate: %w", pid, err)
		}
		return err
	}
	return nil
}

// discoverAllProcesses 通过 Toolhelp32 枚举进程图并筛出受管理的 MCP sidecar。
// ExeFile 只有镜像文件名，因此会先去掉 .exe 后缀再匹配内部受管二进制表。
func discoverAllProcesses() (map[int]int, []mcpProcessInfo) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		pkglogger.Warn("orphan cleanup: Toolhelp32 snapshot failed", "error", err)
		return nil, nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		pkglogger.Warn("orphan cleanup: Process32First failed", "error", err)
		return nil, nil
	}

	allProcs := make(map[int]int, 256)
	var mcpProcs []mcpProcessInfo
	for {
		pid := int(entry.ProcessID)
		ppid := int(entry.ParentProcessID)
		allProcs[pid] = ppid

		name := windows.UTF16ToString(entry.ExeFile[:])
		if bin := matchManagedBinary(name); bin != "" {
			mcpProcs = append(mcpProcs, mcpProcessInfo{pid: pid, ppid: ppid, binary: bin})
		}

		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return allProcs, mcpProcs
}

// discoverAppServerProcessList 在 Windows 上只返回进程图，不主动扫描 app-server argv。
// 完整 argv 需要读 PEB，跨版本脆弱；当前依赖 Job Object 回收由本进程启动的 app-server。
func discoverAppServerProcessList() (map[int]int, []appServerProcessInfo) {
	allProcs, _ := discoverAllProcesses()
	return allProcs, nil
}

func matchManagedBinary(exeFile string) string {
	name := exeFile
	if idx := lastPathSep(name); idx >= 0 {
		name = name[idx+1:]
	}
	if dot := lastDotExe(name); dot >= 0 {
		name = name[:dot]
	}
	if _, ok := managedMCPBinaries[name]; ok {
		return name
	}
	return ""
}

func lastPathSep(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' || s[i] == '/' {
			return i
		}
	}
	return -1
}

func lastDotExe(s string) int {
	// 只剥离真正的 .exe 后缀，避免把 server.v1 这类名称误裁剪。
	if len(s) < 4 {
		return -1
	}
	suffix := s[len(s)-4:]
	if suffix == ".exe" || suffix == ".EXE" {
		return len(s) - 4
	}
	return -1
}
