//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	unixSupervisorControlFD      = 3
	unixSupervisorCheckInterval  = 250 * time.Millisecond
	unixSupervisorForceAfter     = 2 * time.Second
	unixSupervisorGracefulSignal = byte('T')
	unixSupervisorForceSignal    = byte('K')
)

// UnixSupervisedCommand 在 mcp-lsp 与真实语言服务器之间建立专用监管进程和父控制管道。
// 监管进程是独立 session/PGID leader，只对自己的进程组执行信号，避免裸 PID 复用风险。
type UnixSupervisedCommand struct {
	cmd          *exec.Cmd
	controlRead  *os.File
	controlWrite *os.File
	mu           sync.Mutex
	readClosed   bool
	writeClosed  bool
	startCalled  bool
}

// NewPlatformSupervisedCommand 在 Unix 上返回带稳定父控制管道的语言服务器命令。
func NewPlatformSupervisedCommand(name string, args ...string) (SupervisedProcessCommand, error) {
	return NewUnixSupervisedCommand(name, args...)
}

// NewUnixSupervisedCommand 构造由当前 mcp-lsp 二进制监管的真实语言服务器命令。
func NewUnixSupervisedCommand(name string, args ...string) (*UnixSupervisedCommand, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("Unix LSP supervisor executable is empty")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve mcp-lsp executable for Unix LSP supervisor: %w", err)
	}
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create Unix LSP supervisor control pipe: %w", err)
	}
	commandArgs := make([]string, 0, len(args)+3)
	commandArgs = append(commandArgs, processSupervisorModeArgument, "", name)
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(executable, commandArgs...)
	cmd.ExtraFiles = []*os.File{controlRead}
	configureCommand(cmd)
	return &UnixSupervisedCommand{
		cmd:          cmd,
		controlRead:  controlRead,
		controlWrite: controlWrite,
	}, nil
}

// Command 返回供 transport 配置工作目录、环境和 stdio 管道的监管根命令。
func (c *UnixSupervisedCommand) Command() *exec.Cmd {
	if c == nil {
		return nil
	}
	return c.cmd
}

// StartProcessTree 启动监管根并把控制管道绑定到 exact ProcessTree owner。
func (c *UnixSupervisedCommand) StartProcessTree() (*ProcessTree, error) {
	if c == nil || c.cmd == nil {
		return nil, errors.New("Unix supervised command is nil")
	}
	c.mu.Lock()
	if c.startCalled {
		c.mu.Unlock()
		return nil, errors.New("Unix supervised command was already started")
	}
	c.startCalled = true
	c.mu.Unlock()
	if err := c.detachSupervisorWorkingDirectory(); err != nil {
		return nil, errors.Join(err, c.Close())
	}

	hooks := startupAbortHooks{
		captureIdentity: captureProcessIdentity,
		groupOwned:      startupProcessGroupOwned,
		startWait:       startStartupProcessWait,
		waitTimeout:     3 * time.Second,
		killGroup: func(int) error {
			return c.signal(unixForceSignal)
		},
		killProcess: func(*exec.Cmd) error {
			return c.signal(unixForceSignal)
		},
	}
	tree, startErr := startProcessTreeWithHooks(c.cmd, hooks)
	readCloseErr := c.closeControlRead()
	if tree != nil {
		attachUnixProcessSupervisor(tree, c)
	}
	if startErr != nil && tree == nil {
		return nil, errors.Join(startErr, readCloseErr, c.Close())
	}
	return tree, errors.Join(startErr, readCloseErr)
}

// detachSupervisorWorkingDirectory 把真实 LSP 的工作目录写入内部参数，并让监管根固定从系统根目录启动。
func (c *UnixSupervisedCommand) detachSupervisorWorkingDirectory() error {
	workingDir := c.cmd.Dir
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve Unix supervised LSP working directory: %w", err)
		}
	}
	absoluteDir, err := filepath.Abs(workingDir)
	if err != nil {
		return fmt.Errorf("resolve absolute Unix supervised LSP working directory: %w", err)
	}
	info, err := os.Stat(absoluteDir)
	if err != nil {
		return fmt.Errorf("stat Unix supervised LSP working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("Unix supervised LSP working directory is not a directory")
	}
	if len(c.cmd.Args) < 4 || c.cmd.Args[1] != processSupervisorModeArgument {
		return errors.New("Unix LSP supervisor command arguments are invalid")
	}
	c.cmd.Args[2] = filepath.Clean(absoluteDir)
	c.cmd.Dir = string(filepath.Separator)
	return nil
}

// Close 关闭父侧控制管道；只在命令尚未移交给 ProcessTree owner 或 owner 已释放时调用。
func (c *UnixSupervisedCommand) Close() error {
	if c == nil {
		return nil
	}
	return errors.Join(c.closeControlRead(), c.closeControlWrite())
}

// signal 通过稳定父控制管道让监管根对自己的 session/PGID 执行 TERM 或 KILL。
func (c *UnixSupervisedCommand) signal(value int) error {
	var control byte
	switch value {
	case unixGracefulSignal:
		control = unixSupervisorGracefulSignal
	case unixForceSignal:
		control = unixSupervisorForceSignal
	default:
		return fmt.Errorf("unsupported Unix LSP supervisor signal: %d", value)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeClosed || c.controlWrite == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("Unix LSP supervisor control owner is closed; signal_sent=false"))
	}
	if _, err := c.controlWrite.Write([]byte{control}); err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("write Unix LSP supervisor control; signal_sent=false: %w", err))
	}
	return nil
}

func (c *UnixSupervisedCommand) closeControlRead() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readClosed || c.controlRead == nil {
		return nil
	}
	c.readClosed = true
	if err := c.controlRead.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close Unix LSP supervisor control reader: %w", err)
	}
	return nil
}

func (c *UnixSupervisedCommand) closeControlWrite() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeClosed || c.controlWrite == nil {
		return nil
	}
	c.writeClosed = true
	if err := c.controlWrite.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close Unix LSP supervisor control writer: %w", err)
	}
	return nil
}

// attachUnixProcessSupervisor 把稳定控制动作挂到正常 owner 或启动失败后保留的 owner。
func attachUnixProcessSupervisor(tree *ProcessTree, control *UnixSupervisedCommand) {
	if tree == nil || tree.controller == nil || control == nil {
		return
	}
	switch owner := tree.controller.(type) {
	case *unixProcessTree:
		owner.mu.Lock()
		owner.signalMembers = func(members []ProcessIdentity, value int) error {
			if len(members) == 0 {
				return nil
			}
			return control.signal(value)
		}
		owner.releaseOwner = control.Close
		owner.mu.Unlock()
	case *startupProcessTree:
		owner.mu.Lock()
		owner.terminateHook = func() error { return control.signal(unixForceSignal) }
		owner.releaseHook = control.Close
		owner.mu.Unlock()
	}
}

type unixSupervisorControlEvent struct {
	value byte
	err   error
	eof   bool
}

type unixProcessSupervisor struct {
	control       *os.File
	workingDir    string
	waitDone      <-chan error
	controlEvents <-chan unixSupervisorControlEvent
	termSignals   chan os.Signal
	ticker        *time.Ticker
	forceTimer    <-chan time.Time
	terminating   bool
}

// runProcessSupervisor 在 Unix 上运行内部监管模式。
func runProcessSupervisor(args []string) int {
	return runUnixProcessSupervisor(args)
}

// runUnixProcessSupervisor 启动真实语言服务器并监听父控制管道、父进程和 CWD 身份。
func runUnixProcessSupervisor(args []string) int {
	if len(args) < 4 || args[1] != processSupervisorModeArgument || !filepath.IsAbs(args[2]) || strings.TrimSpace(args[3]) == "" {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=invalid_invocation action=exit\n")
		return 2
	}
	supervisor, err := newUnixProcessSupervisor(filepath.Clean(args[2]), args[3], args[4:]...)
	if err != nil {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=start_failed action=exit\n")
		return 1
	}
	defer supervisor.close()
	return supervisor.run()
}

// newUnixProcessSupervisor 创建监管状态并启动真实语言服务器。
func newUnixProcessSupervisor(workingDir, binary string, args ...string) (*unixProcessSupervisor, error) {
	control, err := openUnixSupervisorControl()
	if err != nil {
		return nil, err
	}
	termSignals := make(chan os.Signal, 2)
	signal.Notify(termSignals, syscall.SIGTERM)
	child := exec.Command(binary, args...)
	child.Dir = workingDir
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		signal.Stop(termSignals)
		return nil, errors.Join(fmt.Errorf("start Unix supervised LSP child: %w", err), control.Close())
	}
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.unix-supervisor-child-wait", func(context.Context) {
		waitDone <- child.Wait()
	})
	controlEvents := make(chan unixSupervisorControlEvent, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.unix-supervisor-control-read", func(context.Context) {
		readUnixSupervisorControl(control, controlEvents)
	})
	return &unixProcessSupervisor{
		control:       control,
		workingDir:    workingDir,
		waitDone:      waitDone,
		controlEvents: controlEvents,
		termSignals:   termSignals,
		ticker:        time.NewTicker(unixSupervisorCheckInterval),
	}, nil
}

// openUnixSupervisorControl 只接受父 owner 通过 ExtraFiles 传入的稳定管道能力。
func openUnixSupervisorControl() (*os.File, error) {
	control := os.NewFile(unixSupervisorControlFD, "mcp-lsp-lsp-supervisor-control")
	if control == nil {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=missing_control action=exit\n")
		return nil, errors.New("Unix LSP supervisor control FD is unavailable")
	}
	info, err := control.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("stat Unix LSP supervisor control FD: %w", err), control.Close())
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errors.Join(fmt.Errorf("Unix LSP supervisor control FD is not a pipe: mode=%s", info.Mode()), control.Close())
	}
	return control, nil
}

// close 释放监管根自身拥有的只读资源。
func (s *unixProcessSupervisor) close() {
	if s == nil {
		return
	}
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.termSignals != nil {
		signal.Stop(s.termSignals)
	}
	if s.control != nil {
		_ = s.control.Close()
	}
}

// run 运行监管事件循环，直到真实语言服务器退出或必须强制清理整个 owner 组。
func (s *unixProcessSupervisor) run() int {
	for {
		complete, exitCode := s.runNextEvent()
		if complete {
			return exitCode
		}
	}
}

// runNextEvent 等待一个监管事件并把错误统一投影为退出决定。
func (s *unixProcessSupervisor) runNextEvent() (bool, int) {
	select {
	case waitErr := <-s.waitDone:
		return true, s.childExit(waitErr)
	case event := <-s.controlEvents:
		return supervisorEventResult(s.handleControl(event))
	case <-s.termSignals:
		return supervisorEventResult(s.beginTermination("root_term"))
	case <-s.ticker.C:
		return supervisorEventResult(s.handleOrphanCheck())
	case <-s.forceTimer:
		return supervisorEventResult(s.forceGroup("grace_timeout"))
	}
}

// supervisorEventResult 把单个监管动作结果转换为事件循环控制结果。
func supervisorEventResult(err error) (bool, int) {
	if err != nil {
		return true, 1
	}
	return false, 0
}

// handleOrphanCheck 只在 PPID 与 CWD 的双重证据同时成立时启动组回收。
func (s *unixProcessSupervisor) handleOrphanCheck() error {
	if !unixProcessSupervisorOrphaned(os.Getppid, s.workingDir, os.Stat) {
		return nil
	}
	return s.beginTermination("orphan_deleted_cwd")
}

// handleControl 把父控制协议映射为监管根自己的 TERM/KILL 动作。
func (s *unixProcessSupervisor) handleControl(event unixSupervisorControlEvent) error {
	switch {
	case event.err != nil:
		return s.beginTermination("control_error")
	case event.eof:
		return s.beginTermination("parent_control_eof")
	case event.value == unixSupervisorGracefulSignal:
		return s.beginTermination("parent_term")
	case event.value == unixSupervisorForceSignal:
		return s.forceGroup("parent_kill")
	default:
		return s.beginTermination("invalid_control")
	}
}

// beginTermination 只发送一次 TERM，并为不合作的语言服务器启动强制回收时限。
func (s *unixProcessSupervisor) beginTermination(event string) error {
	if s.terminating {
		return nil
	}
	s.terminating = true
	if err := signalUnixSupervisorGroup(syscall.SIGTERM); err != nil {
		return s.forceGroup("term_failed")
	}
	_, _ = os.Stderr.WriteString("lsp_process_supervisor event=" + event + " action=terminate_group signal_sent=true\n")
	s.forceTimer = time.After(unixSupervisorForceAfter)
	return nil
}

// forceGroup 对监管根自己的稳定 PGID 发送 KILL；成功后当前进程不会继续执行。
func (s *unixProcessSupervisor) forceGroup(event string) error {
	_, _ = os.Stderr.WriteString("lsp_process_supervisor event=" + event + " action=force_group signal_requested=true\n")
	if err := signalUnixSupervisorGroup(syscall.SIGKILL); err != nil {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=" + event + " action=force_group signal_sent=false\n")
		return err
	}
	return nil
}

// childExit 在真实语言服务器退出时清理仍留在同一 owner 组的后代并投影退出码。
func (s *unixProcessSupervisor) childExit(waitErr error) int {
	if unixSupervisorHasOwnedDescendants() {
		if err := s.forceGroup("descendants_after_child_exit"); err != nil {
			return 1
		}
	}
	return supervisorChildExitCode(waitErr)
}

// readUnixSupervisorControl 读取单字节父控制协议；EOF 是 exact parent owner 已消失的稳定证据。
func readUnixSupervisorControl(control *os.File, events chan<- unixSupervisorControlEvent) {
	for {
		var payload [1]byte
		count, err := control.Read(payload[:])
		if count == 1 {
			events <- unixSupervisorControlEvent{value: payload[0]}
			continue
		}
		if errors.Is(err, io.EOF) {
			events <- unixSupervisorControlEvent{eof: true}
			return
		}
		if err != nil {
			events <- unixSupervisorControlEvent{err: err}
			return
		}
	}
}

// unixProcessSupervisorOrphaned 只在 PPID 已归 1 且被监管工作目录明确不存在时返回 true。
// 权限或其他不确定探测错误不能授权破坏性动作。
func unixProcessSupervisorOrphaned(
	getPPID func() int,
	workingDir string,
	statPath func(string) (os.FileInfo, error),
) bool {
	if getPPID() > 1 {
		return false
	}
	if !filepath.IsAbs(workingDir) {
		return false
	}
	_, err := statPath(filepath.Clean(workingDir))
	return errors.Is(err, os.ErrNotExist)
}

// signalUnixSupervisorGroup 只向当前监管进程自己的稳定 PGID 发送信号。
func signalUnixSupervisorGroup(value syscall.Signal) error {
	pid := os.Getpid()
	if pid <= 1 {
		return errors.New("Unix LSP supervisor PID is invalid")
	}
	if err := syscall.Kill(-pid, value); err != nil {
		return fmt.Errorf("signal Unix LSP supervisor owned process group: %w", err)
	}
	return nil
}

// unixSupervisorHasOwnedDescendants 检查监管根退出前是否仍有同 session/PGID 后代。
func unixSupervisorHasOwnedDescendants() bool {
	root, err := captureProcessIdentity(os.Getpid())
	if err != nil {
		return true
	}
	table, err := processTable()
	if err != nil {
		return true
	}
	for _, member := range table {
		if member.PID != root.PID && sameProcessTreeGroup(member, root) {
			return true
		}
	}
	return false
}

// supervisorChildExitCode 将真实语言服务器退出状态投影为监管根退出码。
func supervisorChildExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 1
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return 1
	}
	if status.Exited() {
		return status.ExitStatus()
	}
	if status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}
