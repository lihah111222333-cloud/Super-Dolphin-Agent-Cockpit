//go:build windows

package schema

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"syscall"
	"unsafe"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

const (
	helperKillOnJobClose = 0x00002000
)

type processGuard struct {
	handle   windows.Handle
	assigned bool
}

// prepareProcessGuard 在 worker 启动前创建 Job，并强制 worker suspended 启动。
func prepareProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	if cmd == nil {
		return nil, errors.New("process command is nil")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	creationFlags := reflect.ValueOf(cmd.SysProcAttr).Elem().FieldByName("CreationFlags")
	if !creationFlags.IsValid() || !creationFlags.CanSet() || creationFlags.Kind() != reflect.Uint32 {
		return nil, errors.New("Windows SysProcAttr.CreationFlags is unavailable")
	}
	creationFlags.SetUint(windows.CREATE_SUSPENDED)
	handle, err := createKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	return &processGuard{handle: handle}, nil
}

// attachProcessGuard 在恢复 suspended helper 前绑定关闭即终止的 Windows Job Object。
func attachProcessGuard(cmd *exec.Cmd, guard *processGuard) error {
	return attachProcessGuardWithProbe(cmd, guard, nil)
}

// attachProcessGuardWithProbe 逐阶段绑定 suspended worker 与预建 Job，供故障测试注入真实内部失败。
func attachProcessGuardWithProbe(cmd *exec.Cmd, guard *processGuard, probe processGuardAttachProbe) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	if guard == nil || guard.handle == 0 {
		return errors.New("helper job object is not prepared")
	}
	if err := runProcessGuardAttachProbe(probe, processGuardAttachOpenProcess); err != nil {
		return err
	}
	processHandle, err := openGuardedWindowsProcess(cmd)
	if err != nil {
		return err
	}
	return assignAndResumeWindowsProcess(processHandle, guard, probe)
}

// openGuardedWindowsProcess 打开绑定 Job 与恢复 suspended worker 所需的最小权限句柄。
func openGuardedWindowsProcess(cmd *exec.Cmd) (windows.Handle, error) {
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	return processHandle, err
}

// assignAndResumeWindowsProcess 先绑定 Job 并记录 ownership，再恢复 worker 执行。
func assignAndResumeWindowsProcess(processHandle windows.Handle, guard *processGuard, probe processGuardAttachProbe) error {
	if err := windows.AssignProcessToJobObject(guard.handle, processHandle); err != nil {
		return errors.Join(err, windows.CloseHandle(processHandle))
	}
	guard.assigned = true
	if err := runProcessGuardAttachProbe(probe, processGuardAttachAssignJob); err != nil {
		return errors.Join(err, windows.CloseHandle(processHandle))
	}
	resume := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	status, _, _ := resume.Call(uintptr(processHandle))
	closeErr := windows.CloseHandle(processHandle)
	if status != 0 {
		return errors.Join(fmt.Errorf("NtResumeProcess failed with NTSTATUS 0x%x", status), closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

func runProcessGuardAttachProbe(probe processGuardAttachProbe, stage processGuardAttachStage) error {
	if probe == nil {
		return nil
	}
	return probe(stage)
}

func terminateProcessTree(_ *exec.Cmd, guard *processGuard) error {
	if guard == nil || guard.handle == 0 || !guard.assigned {
		return errors.New("helper job object is not attached")
	}
	return windows.TerminateJobObject(guard.handle, 1)
}

// terminateUnattachedProcessTree 通过 Job 终止已绑定树；未绑定时 direct child 仍处于 suspended 状态。
func terminateUnattachedProcessTree(cmd *exec.Cmd, guard *processGuard) error {
	var jobErr error
	if guard != nil && guard.handle != 0 && guard.assigned {
		jobErr = windows.TerminateJobObject(guard.handle, 1)
	}
	var killErr error
	if cmd == nil || cmd.Process == nil {
		killErr = errors.New("process is not started")
	} else if guard == nil || !guard.assigned {
		killErr = cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
	}
	return errors.Join(jobErr, killErr)
}

func waitGuardedProcess(cmd *exec.Cmd, _ *processGuard) error {
	return cmd.Wait()
}

// closeProcessGuard 关闭 Job Object 并将 guard 转入可重复关闭的终态。
func closeProcessGuard(guard *processGuard) error {
	if guard == nil || guard.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(guard.handle)
	guard.handle = 0
	return err
}

func createKillOnCloseJob() (windows.Handle, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: helperKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

// filesystemSnapshotDirectoryWindowsOps 仅为包内测试注入系统调用，生产路径始终使用 Windows API。
type filesystemSnapshotDirectoryWindowsOps struct {
	open  func(string) (windows.Handle, error)
	flush func(windows.Handle) error
	close func(windows.Handle) error
}

// syncFilesystemSnapshotDirectory 使用可写目录句柄刷新目录元数据，保持 rename 后的耐久性边界。
func syncFilesystemSnapshotDirectory(path string) error {
	err := syncFilesystemSnapshotDirectoryWithOps(path, filesystemSnapshotDirectoryWindowsOps{
		open:  openFilesystemSnapshotDirectory,
		flush: windows.FlushFileBuffers,
		close: windows.CloseHandle,
	})
	return wrapSchemaFilesystemError(path, err)
}

// wrapSchemaFilesystemError 将 Windows 文件系统调用的真实 5/1314 提升为 securefs
// typed 错误；旧目录句柄测试 seam 仍保留原始错误，避免把句柄模式误判为 ACL。
func wrapSchemaFilesystemError(path string, err error) error {
	return securefs.WrapErrorForPath(err, path)
}

// syncFilesystemSnapshotDirectoryWithOps 保留 CreateFile、FlushFileBuffers 和 CloseHandle 的完整错误链。
func syncFilesystemSnapshotDirectoryWithOps(path string, ops filesystemSnapshotDirectoryWindowsOps) error {
	if ops.open == nil || ops.flush == nil || ops.close == nil {
		return errors.New("schema snapshot directory sync operations are incomplete")
	}
	handle, err := ops.open(path)
	if err != nil {
		return fmt.Errorf("open schema snapshot directory for fsync: %w", err)
	}
	flushErr := ops.flush(handle)
	closeErr := ops.close(handle)
	if err := errors.Join(flushErr, closeErr); err != nil {
		return fmt.Errorf("fsync schema snapshot directory: %w", err)
	}
	return nil
}

// openFilesystemSnapshotDirectory 请求目录读写句柄；FlushFileBuffers 要求 GENERIC_WRITE。
func openFilesystemSnapshotDirectory(path string) (windows.Handle, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
}

// filesystemWorkerPermissionMetadata 只从 securefs typed 错误生成 wire 字段；普通
// syscall.Errno（包括旧只读目录句柄失败）不会被猜测为 ACL 授权请求。
func filesystemWorkerPermissionMetadata(cause error) (uint32, string) {
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(cause, &permissionErr) || permissionErr == nil {
		return 0, ""
	}
	switch permissionErr.Win32Code() {
	case filesystemWorkerWindowsAccessDeniedCode:
		return filesystemWorkerWindowsAccessDeniedCode, filesystemWorkerWindowsAccessDeniedKind
	case filesystemWorkerWindowsPrivilegeNotHeldCode:
		return filesystemWorkerWindowsPrivilegeNotHeldCode, filesystemWorkerWindowsPrivilegeNotHeldKind
	default:
		return 0, ""
	}
}

// filesystemWorkerPermissionCause 在 parent 端重建最小 typed 错误；wire 不传路径，
// 因而重建错误只包含稳定 operation 与 Win32 code，保留 errors.As 而不泄露机器信息。
func filesystemWorkerPermissionCause(code uint32, kind string) (error, error) {
	if code == 0 && kind == "" {
		return nil, nil
	}
	if err := validateFilesystemWorkerPermissionFields(&filesystemWorkerError{
		WindowsErrorCode: code, WindowsPermissionKind: kind,
	}); err != nil {
		return nil, err
	}
	return securefs.NewWindowsPermissionError("schema filesystem worker", "", syscall.Errno(code)), nil
}
