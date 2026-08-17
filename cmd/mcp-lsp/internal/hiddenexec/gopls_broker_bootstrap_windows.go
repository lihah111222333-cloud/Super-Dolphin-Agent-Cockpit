//go:build windows

package hiddenexec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsGoplsBrokerBootstrapModeArgument = "__super_dolphin_windows_gopls_broker_bootstrap_v1"
	createBreakawayFromJob                  = 0x01000000
	jobObjectLimitBreakawayOK               = 0x00000800
	jobObjectLimitSilentBreakawayOK         = 0x00001000
)

type windowsGoplsBrokerBootstrapPipes struct {
	requestReader  *os.File
	requestWriter  *os.File
	responseReader *os.File
	responseWriter *os.File
}

// WindowsGoplsBrokerBootstrapProcess 持有已验真 self broker 的临时精确权限。
type WindowsGoplsBrokerBootstrapProcess struct {
	PID                int
	StartIdentity      string
	ExecutablePath     string
	ImageSHA256        string
	VolumeSerialNumber uint32
	FileID             uint64

	mu             sync.Mutex
	command        *exec.Cmd
	processHandle  windows.Handle
	requestWriter  io.WriteCloser
	responseReader io.ReadCloser
	finished       bool
	finishErr      error
	released       bool
	releaseErr     error
}

// StartWindowsGoplsBrokerBootstrap 只允许当前 mcp-lsp.exe 的同文件镜像成为持久 broker。
func StartWindowsGoplsBrokerBootstrap() (*WindowsGoplsBrokerBootstrapProcess, error) {
	selfProof, err := attestCurrentWindowsGoplsBrokerExecutable()
	if err != nil {
		return nil, fmt.Errorf("attest Windows gopls broker bootstrap executable: %w", err)
	}
	creationFlags, err := windowsGoplsBrokerBootstrapCreationFlags()
	if err != nil {
		return nil, err
	}
	pipes, err := newWindowsGoplsBrokerBootstrapPipes()
	if err != nil {
		return nil, err
	}
	command := newWindowsGoplsBrokerBootstrapCommand(selfProof.path, pipes, creationFlags)
	if err := command.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("start Windows gopls broker bootstrap: %w", err), pipes.closeAll())
	}
	if err := pipes.closeChildEnds(); err != nil {
		return nil, failStartedWindowsGoplsBrokerBootstrap(command, 0, pipes, err)
	}
	return finishWindowsGoplsBrokerBootstrapStart(command, pipes, selfProof)
}

// RunWindowsGoplsBrokerBootstrapIfRequested 在正常 runtime 前识别唯一内部 broker marker。
func RunWindowsGoplsBrokerBootstrapIfRequested(args []string, run func(io.Reader, io.Writer) int) (handled bool, exitCode int) {
	if len(args) < 2 || args[1] != windowsGoplsBrokerBootstrapModeArgument {
		return false, 0
	}
	if len(args) != 2 || run == nil {
		return true, 1
	}
	if err := rejectCurrentWindowsKillOnCloseJob(); err != nil {
		return true, 1
	}
	if _, err := attestCurrentWindowsGoplsBrokerExecutable(); err != nil {
		return true, 1
	}
	return true, run(os.Stdin, os.Stdout)
}

// RequestWriter 返回 broker 匿名 stdin pipe 的父进程写端。
func (p *WindowsGoplsBrokerBootstrapProcess) RequestWriter() io.WriteCloser {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	writer := p.requestWriter
	p.requestWriter = nil
	return writer
}

// ResponseReader 返回 broker 匿名 stdout pipe 的父进程读端。
func (p *WindowsGoplsBrokerBootstrapProcess) ResponseReader() io.ReadCloser {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	reader := p.responseReader
	p.responseReader = nil
	return reader
}

// KillAndWait 通过启动时保留的精确句柄终止并回收 provisional broker。
func (p *WindowsGoplsBrokerBootstrapProcess) KillAndWait() error {
	if p == nil {
		return errors.New("Windows gopls broker bootstrap authority is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return errors.New("Windows gopls broker bootstrap authority was released")
	}
	if p.finished {
		return p.finishErr
	}
	p.finishErr = errors.Join(
		closeWindowsGoplsBrokerBootstrapIO(p),
		killAndWaitWindowsGoplsBrokerBootstrap(p.command),
		closeWindowsHandle(p.processHandle, "close Windows gopls broker bootstrap verification handle"),
	)
	p.processHandle = 0
	p.finished = true
	return p.finishErr
}

// ReleaseAuthority 最后才释放进程句柄，任一前置关闭失败仍保留精确 kill 权限。
func (p *WindowsGoplsBrokerBootstrapProcess) ReleaseAuthority() error {
	if p == nil {
		return errors.New("Windows gopls broker bootstrap authority is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return p.finishErr
	}
	if p.released {
		return p.releaseErr
	}
	if p.command == nil || p.command.Process == nil {
		return errors.New("Windows gopls broker bootstrap process authority is unavailable")
	}
	if err := closeWindowsGoplsBrokerBootstrapIO(p); err != nil {
		p.releaseErr = err
		return err
	}
	if err := closeWindowsHandle(p.processHandle, "close released Windows gopls broker bootstrap verification handle"); err != nil {
		p.releaseErr = err
		return err
	}
	p.processHandle = 0
	p.releaseErr = p.command.Process.Release()
	p.released = p.releaseErr == nil
	return p.releaseErr
}

// windowsGoplsBrokerBootstrapCreationFlags 只在受控 KILL_ON_CLOSE Job 明确授权时请求 breakaway。
func windowsGoplsBrokerBootstrapCreationFlags() (uint32, error) {
	flags := uint32(createSuspended | createNewProcessGroup | createNoWindow)
	inJob, err := windowsBootstrapProcessInJob(windows.CurrentProcess())
	if err != nil {
		return 0, fmt.Errorf("inspect current Windows Job membership: %w", err)
	}
	if !inJob {
		return flags, nil
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0, fmt.Errorf("query current Windows Job limits: %w", err)
	}
	limits := info.BasicLimitInformation.LimitFlags
	if limits&jobObjectLimitKillOnJobClose == 0 {
		return flags, nil
	}
	if limits&jobObjectLimitSilentBreakawayOK != 0 {
		return flags, nil
	}
	if limits&jobObjectLimitBreakawayOK == 0 {
		return 0, &WindowsJobPolicyError{
			Operation:       "current Windows Job does not allow the approved mcp-lsp broker breakaway",
			LimitFlags:      limits,
			KillOnClose:     true,
			BreakawayOK:     false,
			SilentBreakaway: false,
		}
	}
	return flags | createBreakawayFromJob, nil
}

// newWindowsGoplsBrokerBootstrapCommand 固定 self 路径、唯一 marker 和匿名标准流。
func newWindowsGoplsBrokerBootstrapCommand(path string, pipes *windowsGoplsBrokerBootstrapPipes, flags uint32) *exec.Cmd {
	command := exec.Command(path, windowsGoplsBrokerBootstrapModeArgument)
	command.Stdin = pipes.requestReader
	command.Stdout = pipes.responseWriter
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
	return command
}

// finishWindowsGoplsBrokerBootstrapStart 在 child 执行代码前复核身份、Job 脱离并恢复唯一线程。
func finishWindowsGoplsBrokerBootstrapStart(command *exec.Cmd, pipes *windowsGoplsBrokerBootstrapPipes, selfProof windowsGoplsBrokerExecutableProof) (*WindowsGoplsBrokerBootstrapProcess, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(command.Process.Pid))
	if err != nil {
		return nil, failStartedWindowsGoplsBrokerBootstrap(command, 0, pipes, fmt.Errorf("open Windows gopls broker bootstrap: %w", err))
	}
	process, err := verifyStartedWindowsGoplsBrokerBootstrap(command, pipes, handle, selfProof)
	if err != nil {
		return nil, failStartedWindowsGoplsBrokerBootstrap(command, handle, pipes, err)
	}
	if err := resumeUniqueProcessThread(command.Process.Pid); err != nil {
		return nil, failStartedWindowsGoplsBrokerBootstrap(command, handle, pipes, fmt.Errorf("resume Windows gopls broker bootstrap: %w", err))
	}
	return process, nil
}

// verifyStartedWindowsGoplsBrokerBootstrap 绑定 child 的镜像证明和不可复用启动身份。
func verifyStartedWindowsGoplsBrokerBootstrap(command *exec.Cmd, pipes *windowsGoplsBrokerBootstrapPipes, handle windows.Handle, selfProof windowsGoplsBrokerExecutableProof) (*WindowsGoplsBrokerBootstrapProcess, error) {
	childProof, err := attestWindowsGoplsBrokerExecutable(handle)
	if err != nil {
		return nil, fmt.Errorf("attest started Windows gopls broker bootstrap: %w", err)
	}
	if err := requireSameWindowsGoplsBrokerExecutable(selfProof, childProof); err != nil {
		return nil, err
	}
	startIdentity, err := windowsBootstrapProcessStartIdentity(handle)
	if err != nil {
		return nil, err
	}
	return &WindowsGoplsBrokerBootstrapProcess{
		PID:                command.Process.Pid,
		StartIdentity:      startIdentity,
		ExecutablePath:     childProof.path,
		ImageSHA256:        childProof.sha256,
		VolumeSerialNumber: childProof.volumeSerialNumber,
		FileID:             childProof.fileID,
		command:            command,
		processHandle:      handle,
		requestWriter:      pipes.requestWriter,
		responseReader:     pipes.responseReader,
	}, nil
}

// rejectCurrentWindowsKillOnCloseJob 允许无破坏性外层 Job，但拒绝 broker 留在 KILL_ON_CLOSE Job。
func rejectCurrentWindowsKillOnCloseJob() error {
	inJob, err := windowsBootstrapProcessInJob(windows.CurrentProcess())
	if err != nil {
		return fmt.Errorf("inspect Windows gopls broker child Job membership: %w", err)
	}
	if !inJob {
		return nil
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(0, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return fmt.Errorf("query Windows gopls broker child Job limits: %w", err)
	}
	limits := info.BasicLimitInformation.LimitFlags
	if limits&jobObjectLimitKillOnJobClose != 0 {
		return fmt.Errorf(
			"Windows gopls broker bootstrap remains in a KILL_ON_CLOSE Job: %s",
			windowsGoplsBrokerJobLimitFacts(limits),
		)
	}
	return nil
}

// windowsGoplsBrokerJobLimitFacts 输出不含路径、PID 或句柄的 Job 策略事实，供启动失败审计。
func windowsGoplsBrokerJobLimitFacts(limits uint32) string {
	return fmt.Sprintf(
		"limit_flags=0x%08x kill_on_close=%t breakaway_ok=%t silent_breakaway_ok=%t",
		limits,
		limits&jobObjectLimitKillOnJobClose != 0,
		limits&jobObjectLimitBreakawayOK != 0,
		limits&jobObjectLimitSilentBreakawayOK != 0,
	)
}

// newWindowsGoplsBrokerBootstrapPipes 创建父写子读和子写父读两组匿名 pipe。
func newWindowsGoplsBrokerBootstrapPipes() (*windowsGoplsBrokerBootstrapPipes, error) {
	requestReader, requestWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create Windows gopls broker request pipe: %w", err)
	}
	responseReader, responseWriter, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("create Windows gopls broker response pipe: %w", err), requestReader.Close(), requestWriter.Close())
	}
	return &windowsGoplsBrokerBootstrapPipes{requestReader: requestReader, requestWriter: requestWriter, responseReader: responseReader, responseWriter: responseWriter}, nil
}

// closeChildEnds 在 CreateProcess 完成继承后关闭父进程中的 child pipe 副本。
func (p *windowsGoplsBrokerBootstrapPipes) closeChildEnds() error {
	return errors.Join(closeWindowsGoplsBrokerPipeFile(&p.requestReader), closeWindowsGoplsBrokerPipeFile(&p.responseWriter))
}

// closeAll 在启动或复核失败时关闭全部仍由父进程持有的匿名 pipe。
func (p *windowsGoplsBrokerBootstrapPipes) closeAll() error {
	return errors.Join(closeWindowsGoplsBrokerPipeFile(&p.requestReader), closeWindowsGoplsBrokerPipeFile(&p.requestWriter), closeWindowsGoplsBrokerPipeFile(&p.responseReader), closeWindowsGoplsBrokerPipeFile(&p.responseWriter))
}

// closeWindowsGoplsBrokerPipeFile 幂等关闭一个 bootstrap pipe 文件。
func closeWindowsGoplsBrokerPipeFile(file **os.File) error {
	if file == nil || *file == nil {
		return nil
	}
	err := (*file).Close()
	*file = nil
	return err
}

// failStartedWindowsGoplsBrokerBootstrap 精确终止并等待未通过复核的 suspended child。
func failStartedWindowsGoplsBrokerBootstrap(command *exec.Cmd, handle windows.Handle, pipes *windowsGoplsBrokerBootstrapPipes, cause error) error {
	return errors.Join(cause, pipes.closeAll(), killAndWaitWindowsGoplsBrokerBootstrap(command), closeWindowsHandle(handle, "close rejected Windows gopls broker bootstrap verification handle"))
}

// killAndWaitWindowsGoplsBrokerBootstrap 通过 os.Process 启动句柄杀死并回收精确 child。
func killAndWaitWindowsGoplsBrokerBootstrap(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return errors.New("Windows gopls broker bootstrap process authority is unavailable")
	}
	killErr := command.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	return errors.Join(killErr, cleanupProcessWaitError(command.Wait()))
}

// closeWindowsGoplsBrokerBootstrapIO 关闭 provisional broker 的父进程 pipe 端点。
func closeWindowsGoplsBrokerBootstrapIO(process *WindowsGoplsBrokerBootstrapProcess) error {
	var requestErr, responseErr error
	if process.requestWriter != nil {
		requestErr = process.requestWriter.Close()
		process.requestWriter = nil
	}
	if process.responseReader != nil {
		responseErr = process.responseReader.Close()
		process.responseReader = nil
	}
	return errors.Join(requestErr, responseErr)
}
