//go:build windows

package schema

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"golang.org/x/sys/windows"
)

func TestFilesystemWorkerPermissionWireRoundTripWindows(t *testing.T) {
	const rawPath = `C:\secret\schema-helper.exe`
	workerErr := classifiedWorkerError(
		CodeProcessExited,
		"schema helper filesystem operation",
		securefs.WrapErrorForPath(syscall.Errno(5), rawPath),
		InitializationFailureTransient,
	)
	if workerErr.WindowsErrorCode != filesystemWorkerWindowsAccessDeniedCode ||
		workerErr.WindowsPermissionKind != filesystemWorkerWindowsAccessDeniedKind {
		t.Fatalf("permission fields = %d/%q, want 5/access_denied", workerErr.WindowsErrorCode, workerErr.WindowsPermissionKind)
	}
	if strings.Contains(workerErr.WindowsPermissionKind, rawPath) || strings.Contains(workerErr.Message, rawPath) {
		t.Fatalf("worker error leaked raw path: %#v", workerErr)
	}
	raw, err := json.Marshal(filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		Error: workerErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	var response filesystemWorkerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	got := filesystemWorkerResponseError(response)
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(got, &permissionErr) || permissionErr == nil {
		t.Fatalf("response error = %v, want typed WindowsPermissionError", got)
	}
	if permissionErr.Win32Code() != 5 || permissionErr.Path != "<redacted-path>" {
		t.Fatalf("reconstructed permission error = %#v, want code 5 and redacted path", permissionErr)
	}
}

func TestFilesystemWorkerRawErrnoDoesNotProducePermissionFieldsWindows(t *testing.T) {
	workerErr := classifiedWorkerError(CodeProcessExited, "old directory handle", syscall.Errno(5), InitializationFailureTransient)
	if workerErr.WindowsErrorCode != 0 || workerErr.WindowsPermissionKind != "" {
		t.Fatalf("raw errno produced typed fields: %#v", workerErr)
	}
}

func TestFilesystemWorkerPrivilegeNotHeldWireRoundTripWindows(t *testing.T) {
	workerErr := classifiedWorkerError(
		CodeProcessExited,
		"schema helper filesystem operation",
		securefs.WrapErrorForPath(syscall.Errno(1314), `C:\secret\schema-helper.exe`),
		InitializationFailureTransient,
	)
	raw, err := json.Marshal(filesystemWorkerResponse{
		Version: filesystemWorkerVersion, Operation: filesystemWorkerVerify,
		Error: workerErr,
	})
	if err != nil {
		t.Fatal(err)
	}
	var response filesystemWorkerResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	got := filesystemWorkerResponseError(response)
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(got, &permissionErr) || permissionErr == nil || permissionErr.Win32Code() != 1314 {
		t.Fatalf("response error = %v, want typed WindowsPermissionError code 1314", got)
	}
	if response.Error.WindowsPermissionKind != filesystemWorkerWindowsPrivilegeNotHeldKind {
		t.Fatalf("wire kind = %q, want %q", response.Error.WindowsPermissionKind, filesystemWorkerWindowsPrivilegeNotHeldKind)
	}
}

func TestFilesystemSnapshotOldHandleSeamDoesNotProduceTypedPermissionErrorWindows(t *testing.T) {
	err := syncFilesystemSnapshotDirectoryWithOps("ignored", filesystemSnapshotDirectoryWindowsOps{
		open:  func(string) (windows.Handle, error) { return windows.Handle(1), nil },
		flush: func(windows.Handle) error { return windows.ERROR_ACCESS_DENIED },
		close: func(windows.Handle) error { return nil },
	})
	var permissionErr *securefs.WindowsPermissionError
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("seam error = %v, want ERROR_ACCESS_DENIED", err)
	}
	if errors.As(err, &permissionErr) {
		t.Fatalf("old handle seam was classified as typed ACL error: %v", err)
	}
}

func TestSchemaFilesystemWrapPromotesRealWindowsPermissionWindows(t *testing.T) {
	err := wrapSchemaFilesystemError(`C:\private\schema-cache`, windows.ERROR_PRIVILEGE_NOT_HELD)
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		t.Fatalf("wrapped real permission error = %v, want typed WindowsPermissionError", err)
	}
	if permissionErr.Win32Code() != 1314 || permissionErr.PermissionKind() != securefs.WindowsPrivilegeNotHeld {
		t.Fatalf("wrapped permission error = code=%d kind=%v, want 1314/privilege_not_held", permissionErr.Win32Code(), permissionErr.PermissionKind())
	}
}

// TestSyncFilesystemSnapshotDirectoryWindows 验证 owner-only 临时目录可用可写句柄完成目录 flush。
func TestSyncFilesystemSnapshotDirectoryWindows(t *testing.T) {
	if err := syncFilesystemSnapshotDirectory(t.TempDir()); err != nil {
		t.Fatalf("syncFilesystemSnapshotDirectory() error = %v", err)
	}
}

// TestSyncFilesystemSnapshotDirectoryWindowsPreservesErrors 验证 CreateFile、FlushFileBuffers、CloseHandle 的拒绝错误不被吞掉。
func TestSyncFilesystemSnapshotDirectoryWindowsPreservesErrors(t *testing.T) {
	tests := []struct {
		name       string
		openErr    error
		flushErr   error
		closeErr   error
		wantClosed bool
	}{
		{name: "create", openErr: windows.ERROR_ACCESS_DENIED},
		{name: "flush", flushErr: windows.ERROR_ACCESS_DENIED, wantClosed: true},
		{name: "close", closeErr: windows.ERROR_ACCESS_DENIED, wantClosed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			closed := false
			err := syncFilesystemSnapshotDirectoryWithOps("ignored", filesystemSnapshotDirectoryWindowsOps{
				open: func(string) (windows.Handle, error) {
					if test.openErr != nil {
						return windows.InvalidHandle, test.openErr
					}
					return windows.Handle(1), nil
				},
				flush: func(windows.Handle) error { return test.flushErr },
				close: func(windows.Handle) error {
					closed = true
					return test.closeErr
				},
			})
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Fatalf("error = %v, want ERROR_ACCESS_DENIED in chain", err)
			}
			if closed != test.wantClosed {
				t.Fatalf("close called = %v, want %v", closed, test.wantClosed)
			}
		})
	}
}

// TestWindowsHelperManifestAndSnapshotPublish 验证 manifest、staging/final 发布及中间文件清理。
func TestWindowsHelperManifestAndSnapshotPublish(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, HelperFileName("windows"))
	if err := os.WriteFile(helper, []byte{'M', 'Z', 'w', 'i', 'n'}, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := HelperIdentity{
		AppCommit: "windows-durability-test",
		GoVersion: runtime.Version(),
		GOOS:      "windows",
		GOARCH:    runtime.GOARCH,
	}
	manifest := helper + HelperManifestSuffix
	if err := WriteHelperManifest(helper, manifest, identity); err != nil {
		t.Fatalf("WriteHelperManifest() error = %v", err)
	}
	if err := VerifyHelperPackage(helper, manifest, identity); err != nil {
		t.Fatalf("VerifyHelperPackage() error = %v", err)
	}
	assertWindowsSnapshotPathAbsent(t, manifest+filesystemSnapshotPublishSuffix)

	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	t.Setenv("TMPDIR", root)
	token := strings.Repeat("a", filesystemSnapshotTokenBytes*2)
	snapshot := filesystemSnapshotIdentity{
		Version:         filesystemSnapshotVersion,
		Directory:       filepath.Join(os.TempDir(), filesystemSnapshotPrefix+token),
		Token:           token,
		HelperGOOS:      "windows",
		OwnerPID:        os.Getpid(),
		OwnerStartToken: "windows-durability-test",
		OwnerExecutable: os.Args[0],
	}
	t.Cleanup(func() { _ = removeOwnedFilesystemSnapshot(snapshot) })
	path, err := writeExecutableSnapshot([]byte("durable-helper"), snapshot)
	if err != nil {
		t.Fatalf("writeExecutableSnapshot() error = %v", err)
	}
	wantPath := filepath.Join(snapshot.Directory, HelperFileName("windows"))
	if path != wantPath {
		t.Fatalf("snapshot path = %q, want %q", path, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("published snapshot = %v", err)
	}
	assertWindowsSnapshotPathAbsent(t, filesystemSnapshotStagingDirectory(snapshot))
	assertWindowsSnapshotPathAbsent(t, filepath.Join(snapshot.Directory, filesystemSnapshotMarker)+filesystemSnapshotPublishSuffix)
	assertWindowsSnapshotPathAbsent(t, wantPath+filesystemSnapshotPublishSuffix)
}

func assertWindowsSnapshotPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %q = %v, want absent", path, err)
	}
}

// TestWindowsProcessGuardSuspendsAssignsThenResumes 以 Windows 专用源码守卫锁定
// CREATE_SUSPENDED、Job 归属、恢复与失败清理的顺序；非 Windows 不编译本测试。
func TestWindowsProcessGuardSuspendsAssignsThenResumes(t *testing.T) {
	source, err := os.ReadFile("platform_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	suspend := strings.Index(text, "windows.CREATE_SUSPENDED")
	assign := strings.Index(text, "windows.AssignProcessToJobObject")
	resume := strings.Index(text, "NtResumeProcess")
	if suspend < 0 || assign < 0 || resume < 0 || assign >= resume {
		t.Fatalf("Windows process guard order missing: suspend=%d assign=%d resume=%d", suspend, assign, resume)
	}
	closeOnFailure := strings.Index(text[resume:], "windows.CloseHandle(handle)")
	if closeOnFailure < 0 {
		t.Fatalf("Windows process guard order missing: suspend=%d assign=%d resume=%d close=%d", suspend, assign, resume, closeOnFailure)
	}
}

func TestStopAndReapClosesWindowsGuardOnTimeout(t *testing.T) {
	cmd := exec.Command("ping.exe", "-n", "30", "127.0.0.1")
	guard, err := prepareProcessGuard(cmd)
	if err != nil {
		t.Fatalf("prepareProcessGuard() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		_ = closeProcessGuard(guard)
		t.Fatalf("cmd.Start() error = %v", err)
	}
	if err := attachProcessGuard(cmd, guard); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = closeProcessGuard(guard)
		t.Fatalf("attachProcessGuard() error = %v", err)
	}
	t.Cleanup(func() {
		_ = closeProcessGuard(guard)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	err = stopAndReap(
		cmd,
		guard,
		make(chan error),
		CodeTimeout,
		"fixture timed out",
		context.DeadlineExceeded,
		nil,
	)
	if ErrorCode(err) != CodeReapFailed {
		t.Fatalf("stopAndReap() code = %q, want %q; error=%v", ErrorCode(err), CodeReapFailed, err)
	}
	if guard.handle != 0 {
		t.Fatalf("guard handle = %v, want closed handle", guard.handle)
	}
	if err := closeProcessGuard(guard); err != nil {
		t.Fatalf("closeProcessGuard() after timeout error = %v", err)
	}
	waitResult := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema.windows-test.wait", func(context.Context) {
		waitResult <- cmd.Wait()
	})
	select {
	case waitErr := <-waitResult:
		if waitErr == nil {
			t.Fatal("guarded process exited successfully after forced termination")
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("guarded process remained alive after Job Object termination and close")
	}
}

func TestWindowsInternalAttachFailuresReapPreparedBoundary(t *testing.T) {
	for _, stage := range []processGuardAttachStage{
		processGuardAttachOpenProcess,
		processGuardAttachAssignJob,
	} {
		t.Run(string(stage), func(t *testing.T) {
			cmd := exec.Command("ping.exe", "-n", "30", "127.0.0.1")
			guard, err := prepareProcessGuard(cmd)
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				_ = closeProcessGuard(guard)
				t.Fatal(err)
			}
			attachErr := errors.New("injected internal attach failure")
			err = attachProcessGuardWithProbe(cmd, guard, func(current processGuardAttachStage) error {
				if current == stage {
					return attachErr
				}
				return nil
			})
			if !errors.Is(err, attachErr) {
				t.Fatalf("attachProcessGuardWithProbe() error = %v", err)
			}
			if err := terminateUnattachedProcessTree(cmd, guard); err != nil {
				t.Fatalf("terminateUnattachedProcessTree() error = %v", err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("terminated suspended process exited successfully")
			}
			if err := closeProcessGuard(guard); err != nil {
				t.Fatalf("closeProcessGuard() error = %v", err)
			}
		})
	}
}
