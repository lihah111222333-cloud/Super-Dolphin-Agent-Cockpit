//go:build darwin

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
	darwinSupervisorControlFD      = 3
	darwinSupervisorCheckInterval  = 250 * time.Millisecond
	darwinSupervisorForceAfter     = 2 * time.Second
	darwinSupervisorGracefulSignal = byte('T')
	darwinSupervisorForceSignal    = byte('K')
)

// DarwinSupervisedCommand 在 mcp-lsp 与真实语言服务器之间建立专用监管进程和父控制管道。
// 监管进程是独立 session/PGID leader，只对自己的进程组执行信号，避免裸 PID 复用风险。
type DarwinSupervisedCommand struct {
	cmd          *exec.Cmd
	controlRead  *os.File
	controlWrite *os.File
	mu           sync.Mutex
	readClosed   bool
	writeClosed  bool
	startCalled  bool
}

// NewPlatformSupervisedCommand 在 Darwin 上返回带稳定父控制管道的语言服务器命令。
func NewPlatformSupervisedCommand(name string, args ...string) (SupervisedProcessCommand, error) {
	return NewDarwinSupervisedCommand(name, args...)
}

// NewDarwinSupervisedCommand 构造由当前 mcp-lsp 二进制监管的真实语言服务器命令。
func NewDarwinSupervisedCommand(name string, args ...string) (*DarwinSupervisedCommand, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("Darwin LSP supervisor executable is empty")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve mcp-lsp executable for Darwin LSP supervisor: %w", err)
	}
	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create Darwin LSP supervisor control pipe: %w", err)
	}
	commandArgs := make([]string, 0, len(args)+2)
	commandArgs = append(commandArgs, processSupervisorModeArgument, name)
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(executable, commandArgs...)
	cmd.ExtraFiles = []*os.File{controlRead}
	configureCommand(cmd)
	return &DarwinSupervisedCommand{
		cmd:          cmd,
		controlRead:  controlRead,
		controlWrite: controlWrite,
	}, nil
}

// Command 返回供 transport 配置工作目录、环境和 stdio 管道的监管根命令。
func (c *DarwinSupervisedCommand) Command() *exec.Cmd {
	if c == nil {
		return nil
	}
	return c.cmd
}

// StartProcessTree 启动监管根并把控制管道绑定到 exact ProcessTree owner。
func (c *DarwinSupervisedCommand) StartProcessTree() (*ProcessTree, error) {
	if c == nil || c.cmd == nil {
		return nil, errors.New("Darwin supervised command is nil")
	}
	c.mu.Lock()
	if c.startCalled {
		c.mu.Unlock()
		return nil, errors.New("Darwin supervised command was already started")
	}
	c.startCalled = true
	c.mu.Unlock()

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
		attachDarwinProcessSupervisor(tree, c)
	}
	if startErr != nil && tree == nil {
		return nil, errors.Join(startErr, readCloseErr, c.Close())
	}
	return tree, errors.Join(startErr, readCloseErr)
}

// Close 关闭父侧控制管道；只在命令尚未移交给 ProcessTree owner 或 owner 已释放时调用。
func (c *DarwinSupervisedCommand) Close() error {
	if c == nil {
		return nil
	}
	return errors.Join(c.closeControlRead(), c.closeControlWrite())
}

// signal 通过稳定父控制管道让监管根对自己的 session/PGID 执行 TERM 或 KILL。
func (c *DarwinSupervisedCommand) signal(value int) error {
	var control byte
	switch value {
	case unixGracefulSignal:
		control = darwinSupervisorGracefulSignal
	case unixForceSignal:
		control = darwinSupervisorForceSignal
	default:
		return fmt.Errorf("unsupported Darwin LSP supervisor signal: %d", value)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeClosed || c.controlWrite == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("Darwin LSP supervisor control owner is closed; signal_sent=false"))
	}
	if _, err := c.controlWrite.Write([]byte{control}); err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("write Darwin LSP supervisor control; signal_sent=false: %w", err))
	}
	return nil
}

func (c *DarwinSupervisedCommand) closeControlRead() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readClosed || c.controlRead == nil {
		return nil
	}
	c.readClosed = true
	if err := c.controlRead.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close Darwin LSP supervisor control reader: %w", err)
	}
	return nil
}

func (c *DarwinSupervisedCommand) closeControlWrite() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeClosed || c.controlWrite == nil {
		return nil
	}
	c.writeClosed = true
	if err := c.controlWrite.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close Darwin LSP supervisor control writer: %w", err)
	}
	return nil
}

// attachDarwinProcessSupervisor 把稳定控制动作挂到正常 owner 或启动失败后保留的 owner。
func attachDarwinProcessSupervisor(tree *ProcessTree, control *DarwinSupervisedCommand) {
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

type darwinSupervisorControlEvent struct {
	value byte
	err   error
	eof   bool
}

type darwinProcessSupervisor struct {
	control       *os.File
	waitDone      <-chan error
	controlEvents <-chan darwinSupervisorControlEvent
	termSignals   chan os.Signal
	ticker        *time.Ticker
	forceTimer    <-chan time.Time
	terminating   bool
}

// runProcessSupervisor 在 Darwin 上运行内部监管模式。
func runProcessSupervisor(args []string) int {
	return runDarwinProcessSupervisor(args)
}

// runDarwinProcessSupervisor 启动真实语言服务器并监听父控制管道、父进程和 CWD 身份。
func runDarwinProcessSupervisor(args []string) int {
	if len(args) < 3 || args[1] != processSupervisorModeArgument || strings.TrimSpace(args[2]) == "" {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=invalid_invocation action=exit\n")
		return 2
	}
	supervisor, err := newDarwinProcessSupervisor(args[2], args[3:]...)
	if err != nil {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=start_failed action=exit\n")
		return 1
	}
	defer supervisor.close()
	return supervisor.run()
}

// newDarwinProcessSupervisor 创建监管状态并启动真实语言服务器。
func newDarwinProcessSupervisor(binary string, args ...string) (*darwinProcessSupervisor, error) {
	control, err := openDarwinSupervisorControl()
	if err != nil {
		return nil, err
	}
	termSignals := make(chan os.Signal, 2)
	signal.Notify(termSignals, syscall.SIGTERM)
	child := exec.Command(binary, args...)
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		signal.Stop(termSignals)
		return nil, errors.Join(fmt.Errorf("start Darwin supervised LSP child: %w", err), control.Close())
	}
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.darwin-supervisor-child-wait", func(context.Context) {
		waitDone <- child.Wait()
	})
	controlEvents := make(chan darwinSupervisorControlEvent, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.darwin-supervisor-control-read", func(context.Context) {
		readDarwinSupervisorControl(control, controlEvents)
	})
	return &darwinProcessSupervisor{
		control:       control,
		waitDone:      waitDone,
		controlEvents: controlEvents,
		termSignals:   termSignals,
		ticker:        time.NewTicker(darwinSupervisorCheckInterval),
	}, nil
}

// openDarwinSupervisorControl 只接受父 owner 通过 ExtraFiles 传入的稳定管道能力。
func openDarwinSupervisorControl() (*os.File, error) {
	control := os.NewFile(darwinSupervisorControlFD, "mcp-lsp-lsp-supervisor-control")
	if control == nil {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=missing_control action=exit\n")
		return nil, errors.New("Darwin LSP supervisor control FD is unavailable")
	}
	info, err := control.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("stat Darwin LSP supervisor control FD: %w", err), control.Close())
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return nil, errors.Join(fmt.Errorf("Darwin LSP supervisor control FD is not a pipe: mode=%s", info.Mode()), control.Close())
	}
	return control, nil
}

// close 释放监管根自身拥有的只读资源。
func (s *darwinProcessSupervisor) close() {
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
func (s *darwinProcessSupervisor) run() int {
	for {
		complete, exitCode := s.runNextEvent()
		if complete {
			return exitCode
		}
	}
}

// runNextEvent 等待一个监管事件并把错误统一投影为退出决定。
func (s *darwinProcessSupervisor) runNextEvent() (bool, int) {
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
func (s *darwinProcessSupervisor) handleOrphanCheck() error {
	if !darwinProcessSupervisorOrphaned(os.Getppid, os.Getwd, os.Stat) {
		return nil
	}
	return s.beginTermination("orphan_deleted_cwd")
}

// handleControl 把父控制协议映射为监管根自己的 TERM/KILL 动作。
func (s *darwinProcessSupervisor) handleControl(event darwinSupervisorControlEvent) error {
	switch {
	case event.err != nil:
		return s.beginTermination("control_error")
	case event.eof:
		return s.beginTermination("parent_control_eof")
	case event.value == darwinSupervisorGracefulSignal:
		return s.beginTermination("parent_term")
	case event.value == darwinSupervisorForceSignal:
		return s.forceGroup("parent_kill")
	default:
		return s.beginTermination("invalid_control")
	}
}

// beginTermination 只发送一次 TERM，并为不合作的语言服务器启动强制回收时限。
func (s *darwinProcessSupervisor) beginTermination(event string) error {
	if s.terminating {
		return nil
	}
	s.terminating = true
	if err := signalDarwinSupervisorGroup(syscall.SIGTERM); err != nil {
		return s.forceGroup("term_failed")
	}
	_, _ = os.Stderr.WriteString("lsp_process_supervisor event=" + event + " action=terminate_group signal_sent=true\n")
	s.forceTimer = time.After(darwinSupervisorForceAfter)
	return nil
}

// forceGroup 对监管根自己的稳定 PGID 发送 KILL；成功后当前进程不会继续执行。
func (s *darwinProcessSupervisor) forceGroup(event string) error {
	_, _ = os.Stderr.WriteString("lsp_process_supervisor event=" + event + " action=force_group signal_requested=true\n")
	if err := signalDarwinSupervisorGroup(syscall.SIGKILL); err != nil {
		_, _ = os.Stderr.WriteString("lsp_process_supervisor event=" + event + " action=force_group signal_sent=false\n")
		return err
	}
	return nil
}

// childExit 在真实语言服务器退出时清理仍留在同一 owner 组的后代并投影退出码。
func (s *darwinProcessSupervisor) childExit(waitErr error) int {
	if darwinSupervisorHasOwnedDescendants() {
		if err := s.forceGroup("descendants_after_child_exit"); err != nil {
			return 1
		}
	}
	return supervisorChildExitCode(waitErr)
}

// readDarwinSupervisorControl 读取单字节父控制协议；EOF 是 exact parent owner 已消失的稳定证据。
func readDarwinSupervisorControl(control *os.File, events chan<- darwinSupervisorControlEvent) {
	for {
		var payload [1]byte
		count, err := control.Read(payload[:])
		if count == 1 {
			events <- darwinSupervisorControlEvent{value: payload[0]}
			continue
		}
		if errors.Is(err, io.EOF) {
			events <- darwinSupervisorControlEvent{eof: true}
			return
		}
		if err != nil {
			events <- darwinSupervisorControlEvent{err: err}
			return
		}
	}
}

// darwinProcessSupervisorOrphaned 只在 PPID 已归 1 且 CWD 明确不存在时返回 true。
// 权限或其他不确定探测错误不能授权破坏性动作。
func darwinProcessSupervisorOrphaned(
	getPPID func() int,
	getCWD func() (string, error),
	statPath func(string) (os.FileInfo, error),
) bool {
	if getPPID() > 1 {
		return false
	}
	cwd, err := getCWD()
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	_, err = statPath(filepath.Clean(cwd))
	return errors.Is(err, os.ErrNotExist)
}

// signalDarwinSupervisorGroup 只向当前监管进程自己的稳定 PGID 发送信号。
func signalDarwinSupervisorGroup(value syscall.Signal) error {
	pid := os.Getpid()
	if pid <= 1 {
		return errors.New("Darwin LSP supervisor PID is invalid")
	}
	if err := syscall.Kill(-pid, value); err != nil {
		return fmt.Errorf("signal Darwin LSP supervisor owned process group: %w", err)
	}
	return nil
}

// darwinSupervisorHasOwnedDescendants 检查监管根退出前是否仍有同 session/PGID 后代。
func darwinSupervisorHasOwnedDescendants() bool {
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
